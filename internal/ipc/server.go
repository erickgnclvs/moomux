package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/sessionview"
	"github.com/erickgnclvs/moomux/internal/tui"
)

// Server exposes a tui.Backend over a unix socket.
type Server struct {
	Backend tui.Backend
	// Config returns a snapshot of the backend's config, served to clients
	// so they can render without reading the config file themselves. It's a
	// func rather than a *config.Config because the value is live: App
	// mutates it under its own lock, and handing out the pointer would put
	// every reader here in a race with AddProject. app.App.ConfigSnapshot
	// satisfies this.
	Config func() config.Config
	// AgentOptions returns the agent/model/thinking-level tables a
	// new-session picker offers, served the same way as Config so a client
	// never keeps its own copy. app.App.AgentOptions satisfies this.
	AgentOptions func() []config.AgentOption
	// Source is the derived per-session state stream — effective state,
	// labels, quips, git/PR status, recovered prompts — computed once here
	// rather than by each client. Optional; powers the "Watch" stream.
	Source sessionview.Source

	// subMu guards the fan-out below. Source.Run is started once, on the
	// first "Watch" client, and every later client is added as a subscriber
	// to that one run rather than starting its own.
	subMu       sync.Mutex
	subs        map[chan sessionview.Snapshot]struct{}
	watcherOnce bool
}

// subscribe registers a channel for snapshots, starting the source on the
// first caller.
//
// One run per connection was a whole duplicate polling stack per client: its
// own directory rescans, its own sqlite3 subprocess per database per tick,
// and — on macOS, where fsnotify's kqueue backend opens a descriptor per
// file in a watched directory — its own several hundred file descriptors.
// Two front ends attached at once (the TUI and the Mac app) paid all of it
// twice, for byte-identical snapshots.
func (s *Server) subscribe() chan sessionview.Snapshot {
	// Buffered so one client that stops reading (or is slow to write to)
	// can't stall delivery to the others; send is non-blocking regardless.
	ch := make(chan sessionview.Snapshot, 8)
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if s.subs == nil {
		s.subs = map[chan sessionview.Snapshot]struct{}{}
	}
	s.subs[ch] = struct{}{}
	if !s.watcherOnce {
		s.watcherOnce = true
		go s.fanOut()
	}
	return ch
}

func (s *Server) unsubscribe(ch chan sessionview.Snapshot) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	delete(s.subs, ch)
}

// fanOut runs the source for the life of the server and copies every
// snapshot to each current subscriber. It deliberately never stops: the
// watchers are cheap to leave running relative to tearing one down and
// standing a fresh one up on every reconnect, and a server with no clients
// is idle anyway.
func (s *Server) fanOut() {
	in := make(chan sessionview.Snapshot, 8)
	go s.Source.Run(context.Background(), in)
	for snap := range in {
		s.subMu.Lock()
		for ch := range s.subs {
			// Dropping a snapshot for a subscriber that's still behind is
			// safe: snapshots are absolute state, not deltas, so the next
			// one it does read supersedes whatever it missed.
			select {
			case ch <- snap:
			default:
			}
		}
		s.subMu.Unlock()
	}
}

// Listen removes any stale socket at path and starts listening on it.
func Listen(path string) (net.Listener, error) {
	// The socket is an unauthenticated control channel: anyone who can dial
	// it can call CreateSession — which runs the worktree-create userscripts
	// and types a caller-supplied first prompt into the new agent's pane. Go binds unix sockets 0755 by default, so the chmod to
	// 0600 below is what actually gates it — connect(2) needs write
	// permission on the socket. 0700 here only helps when this call is what
	// creates the directory; the default one already exists (it holds
	// moomux.log), and widening or narrowing a shared dir isn't ours to do.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// A leftover socket from a crashed server blocks bind; a live one is
	// caught by dialing it first rather than by clobbering it blindly.
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return nil, fmt.Errorf("%s: already in use by a running moomux serve", path)
	}
	// Only ever unlink an actual socket: net.Dial fails on a regular file, so
	// the liveness check above doesn't protect one. A mistyped
	// `-socket ~/.config/moomux/config.toml` must not delete the config.
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("%s exists and is not a socket; refusing to replace it", path)
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func (s *Server) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		// One connection per call keeps calls independent: a slow
		// CreateSession can't block a status poll behind it.
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	// One reader for the life of the connection, handed on to stream: a
	// second bufio.Reader would drop whatever the first buffered past the
	// request line, so a nudge arriving in the same read as the Watch
	// request would vanish and the client would wait out a whole tick.
	r := bufio.NewReader(c)
	var req request
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return
	}
	if req.Method == "Watch" {
		s.stream(c, r)
		return
	}
	res, err := s.dispatch(req.Method, req.Args)
	out := response{Result: res}
	if err != nil {
		out.Err = err.Error()
		out.Code = codeFor(err)
	}
	if err := json.NewEncoder(c).Encode(out); err != nil {
		slog.Warn("ipc: write response", "method", req.Method, "err", err)
	}
}

// stream pushes snapshots until the client hangs up. The write error on a
// closed connection is what ends it — there's no unsubscribe.
//
// The client's half of the connection isn't dead air: each line it sends is
// a nudge, asking the source for a snapshot now rather than at its next
// tick (see sessionview.Source.Nudge). Reading it is also how a client that
// hangs up releases this goroutine immediately, rather than at the next
// snapshot write.
func (s *Server) stream(c net.Conn, r *bufio.Reader) {
	if s.Source == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := s.subscribe()
	defer s.unsubscribe(ch)
	go func() {
		defer cancel() // returns when the peer closes
		dec := json.NewDecoder(r)
		for {
			var n nudgeRequest
			if err := dec.Decode(&n); err != nil {
				return
			}
			if n.Nudge {
				s.Source.Nudge()
			}
		}
	}()
	enc := json.NewEncoder(c)
	for {
		select {
		case snap := <-ch:
			if err := enc.Encode(snap); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) dispatch(method string, a Args) (Result, error) {
	b := s.Backend
	switch method {
	case "Config":
		if s.Config == nil {
			return Result{}, errors.New("server has no config")
		}
		cfg := s.Config()
		return Result{Cfg: &cfg}, nil
	case "AgentOptions":
		if s.AgentOptions == nil {
			return Result{}, errors.New("server has no agent options")
		}
		return Result{Agents: s.AgentOptions()}, nil
	case "Themes":
		// No Server hook, unlike AgentOptions: that table lives in
		// internal/app, which this package can't import. This one is static
		// data in config, which it already does.
		return Result{Themes: config.Themes()}, nil
	case "Sessions":
		return Result{Sessions: b.Sessions()}, nil
	case "SuggestedProject":
		name, repo := b.SuggestedProject()
		return Result{Name: name, Hint: repo}, nil

	case "CreateSession":
		if a.Req == nil {
			return Result{}, errors.New("CreateSession: missing request")
		}
		sess, hint, err := b.CreateSession(*a.Req)
		return Result{Session: &sess, Hint: hint}, err
	case "OpenSession":
		hint, err := b.OpenSession(a.ID)
		return Result{Hint: hint}, err
	case "EnsureTmux":
		hint, err := b.EnsureTmux(a.ID)
		return Result{Hint: hint}, err
	case "DeleteSession":
		hint, err := b.DeleteSession(a.ID)
		return Result{Hint: hint}, err
	case "KillTmux":
		return Result{}, b.KillTmux(a.ID)

	case "WorktreeStatus":
		dirty, unpushed, ok := b.WorktreeStatus(a.ID)
		return Result{Dirty: dirty, Unpushed: unpushed, OK: ok}, nil
	case "ChangeSummary":
		files, commits, ok := b.ChangeSummary(a.ID)
		return Result{Files: files, Commits: commits, OK: ok}, nil

	case "SetSessionTags":
		return sessionResult(b.SetSessionTags(a.ID, a.Ticket, a.PR))
	case "SetSessionPrompt":
		return sessionResult(b.SetSessionPrompt(a.ID, a.Prompt))
	case "SetSessionAgent":
		return sessionResult(b.SetSessionAgent(a.ID, a.Agent, a.Dangerous != nil && *a.Dangerous))
	case "RenameSession":
		return sessionResult(b.RenameSession(a.ID, a.Name))
	case "SetSessionArchived":
		return sessionResult(b.SetSessionArchived(a.ID, a.On))
	case "MoveSession":
		return Result{}, b.MoveSession(a.ID, a.Delta)
	case "MoveProject":
		return s.mutResult(b.MoveProject(a.Name, a.Delta))

	case "AddProject":
		warning, err := b.AddProject(a.Name, a.Proj)
		res, err := s.mutResult(err)
		res.Hint = warning
		return res, err
	case "InitProjectAndAdd":
		return s.mutResult(b.InitProjectAndAdd(a.Name, a.Proj))
	case "AddPlainProject":
		return s.mutResult(b.AddPlainProject(a.Name, a.Proj))
	case "UpdateProject":
		return s.mutResult(b.UpdateProject(a.Name, a.Proj))
	case "RemoveProject":
		return s.mutResult(b.RemoveProject(a.Name))

	case "SetTheme":
		return s.mutResult(b.SetTheme(a.Theme, a.Appearance))
	case "SetAutoSubmitDefault":
		return s.mutResult(b.SetAutoSubmitDefault(a.On))
	case "SetSortRecentFirst":
		return s.mutResult(b.SetSortRecentFirst(a.On))
	case "SetAutoTmux":
		return s.mutResult(b.SetAutoTmux(a.On))
	case "SetCompactDetail":
		return s.mutResult(b.SetCompactDetail(a.On))
	}
	return Result{}, fmt.Errorf("unknown method %q", method)
}

// sessionResult adapts the several Set*/Rename methods that all return
// (session.Session, error).
func sessionResult(sess session.Session, err error) (Result, error) {
	return Result{Session: &sess}, err
}

// mutResult adapts the config-mutating Backend methods (all bare error
// returns) by attaching the post-mutation config snapshot to a successful
// response — see Client.mut, which applies it in place instead of making a
// second "Config" round trip for every settings change.
func (s *Server) mutResult(err error) (Result, error) {
	if err != nil || s.Config == nil {
		return Result{}, err
	}
	cfg := s.Config()
	return Result{Cfg: &cfg}, nil
}
