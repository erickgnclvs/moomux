package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tui"
	"github.com/erickgnclvs/moomux/internal/watcher"
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
	Watcher      watcher.Watcher // optional; powers the "Watch" stream
}

// Listen removes any stale socket at path and starts listening on it.
func Listen(path string) (net.Listener, error) {
	// The socket is an unauthenticated control channel: anyone who can dial
	// it can call StartFirstPrompt (type and submit arbitrary text into the
	// owner's agent pane) or CreateSession (which runs the worktree-create
	// userscripts). Go binds unix sockets 0755 by default, so the chmod to
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
	var req request
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&req); err != nil {
		return
	}
	if req.Method == "Watch" {
		s.stream(c)
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

// stream pushes watcher snapshots until the client hangs up. The write
// error on a closed connection is what ends it — there's no unsubscribe.
func (s *Server) stream(c net.Conn) {
	if s.Watcher == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan watcher.Snapshot, 4)
	go s.Watcher.Run(ctx, ch)
	// Watch the connection so a client that hangs up releases this goroutine
	// and its watcher immediately, rather than at the next snapshot write.
	go func() {
		io.Copy(io.Discard, c) // returns when the peer closes
		cancel()
	}()
	enc := json.NewEncoder(c)
	for {
		// Watcher.Run may return without closing ch, so ranging over it
		// alone would park here forever holding the connection open.
		select {
		case snap, ok := <-ch:
			if !ok {
				return
			}
			w := snapshotWire{States: snap.States, PollTime: snap.PollTime}
			if snap.Err != nil {
				w.Err = snap.Err.Error()
			}
			if err := enc.Encode(w); err != nil {
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
	case "Sessions":
		return Result{Sessions: b.Sessions()}, nil
	case "Projects":
		return Result{Strings: b.Projects()}, nil
	case "TmuxAliveAll":
		return Result{Alive: b.TmuxAliveAll()}, nil

	case "CreateSession":
		sess, hint, err := b.CreateSession(a.Project, a.Name, a.Agent, a.Branch, a.Ticket, a.OpenTerminal, a.Dangerous, a.BaseBranch, a.Model, a.Thinking)
		return Result{Session: &sess, Hint: hint}, err
	case "StartFirstPrompt":
		return Result{}, b.StartFirstPrompt(a.TmuxSession, a.Prompt, a.AutoSubmit)
	case "OpenSession":
		hint, err := b.OpenSession(a.ID)
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
	case "PRStatus":
		info, ok := b.PRStatus(a.ID)
		return Result{PR: &info, OK: ok}, nil

	case "SetSessionStatusTitle":
		return Result{}, b.SetSessionStatusTitle(a.ID, a.State)
	case "SetSessionTags":
		return sessionResult(b.SetSessionTags(a.ID, a.Ticket, a.PR))
	case "SetSessionPrompt":
		return sessionResult(b.SetSessionPrompt(a.ID, a.Prompt))
	case "SetSessionAgent":
		return sessionResult(b.SetSessionAgent(a.ID, a.Agent, a.AgentDangerous))
	case "RenameSession":
		return sessionResult(b.RenameSession(a.ID, a.Name))
	case "SetSessionArchived":
		return sessionResult(b.SetSessionArchived(a.ID, a.On))
	case "MoveSession":
		return Result{}, b.MoveSession(a.ID, a.Delta)
	case "MoveProject":
		return s.mutResult(b.MoveProject(a.Name, a.Delta))

	case "AddProject":
		return s.mutResult(b.AddProject(a.Name, a.Proj))
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
