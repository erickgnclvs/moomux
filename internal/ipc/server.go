package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode"

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
	// PaneCwd answers the current directory of a tmux session's active pane
	// — the one an attached client is showing, where a tapped path was
	// printed — which ReadFile resolves a relative path against first.
	// Optional; without it paths resolve against the worktree.
	// tmux.Client.ActivePaneCwd satisfies this.
	PaneCwd func(tmuxSession string) (string, error)

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
	// A json.Decoder, deliberately, and not a line read: it returns as soon
	// as the JSON value is complete, needing neither a trailing newline nor
	// EOF. A client that writes its request and then waits for the answer
	// on the same connection — without half-closing, which is an ordinary
	// way to write a client — has sent neither. Requiring one made every
	// pull method hang for such a client, with no error and no refusal:
	// total, silent, and on every method at once.
	//
	// Limited, because the decoder holds the whole request in memory before
	// anything can look at it — a SaveFile's size check would otherwise run
	// only after an arbitrarily large upload had been buffered. The limit
	// covers the request alone: a Watch or Attach stream carries on reading
	// r itself, past it.
	lim := &io.LimitedReader{R: r, N: maxRequest}
	dec := json.NewDecoder(lim)
	var req request
	if err := dec.Decode(&req); err != nil {
		if lim.N <= 0 {
			// Answered, so the client reads a reason rather than a
			// connection that closed on it.
			msg := fmt.Sprintf("request is over the %d MB limit", maxRequest>>20)
			_ = json.NewEncoder(c).Encode(response{Err: msg})
		}
		return
	}
	if req.Method == "Watch" {
		// dec.Buffered() and not r alone: the decoder owns whatever it read
		// past the request, so a nudge that arrived in the same packet is
		// sitting in it — dropped at best, and at worst half a JSON value
		// that makes the next decode fail and tears the stream down.
		s.stream(c, io.MultiReader(dec.Buffered(), r))
		return
	}
	if req.Method == "Attach" {
		s.attach(c, attachInput(dec, r), req.Args)
		return
	}
	res, err := s.dispatch(req.Method, req.Args)
	out := response{Result: res}
	if err != nil {
		out.Err = err.Error()
		out.Code = codeFor(err)
	}
	// No HTML escaping: nothing here is embedded in a page, and escaping
	// <, > and & as < turns each into six bytes — a Diff of JSX or
	// HTML goes out several times the size of the patch it carries.
	enc := json.NewEncoder(c)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		slog.Warn("ipc: write response", "method", req.Method, "err", err)
	}
}

// maxSaveFile caps one SaveFile upload. A phone photo is a few MB; this is
// room for a short screen recording without letting a client fill the disk
// in one call.
const maxSaveFile = 32 << 20

// maxRequest caps one request's JSON: a largest SaveFile, base64-inflated
// by 4/3, plus room for the rest of the envelope.
const maxRequest = maxSaveFile/3*4 + 1<<20

// maxSaveName keeps the saved name well inside a filesystem's 255 bytes,
// the random prefix included.
const maxSaveName = 100

// saveFile writes an uploaded file where an agent on this machine can read
// it, and returns its path. A file picked on a phone has no path the agent
// could open, so its bytes come over the wire instead — and the Mac app
// sends its dropped files the same way, so there is one path for both. The
// ceiling is the system's temp sweep (~3 days unread), long after the agent
// looked.
//
// The name is a random prefix plus the original's, reduced to characters
// that need no shell quoting, so the path can go into a prompt as-is and two
// uploads of "image.jpg" never collide.
func saveFile(tmp, name string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("SaveFile: empty file")
	}
	if len(data) > maxSaveFile {
		return "", fmt.Errorf("SaveFile: %s MB is over the %d MB limit", mb(int64(len(data))), maxSaveFile>>20)
	}
	safe := strings.Map(func(r rune) rune {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-') {
			return r
		}
		return '-'
	}, filepath.Base(name))
	if strings.Trim(safe, ".-") == "" {
		safe = "file"
	}
	if len(safe) > maxSaveName {
		// Keep the extension: it is what tells the agent what the file is.
		ext := filepath.Ext(safe)
		if len(ext) > maxSaveName/4 {
			ext = ""
		}
		safe = safe[:maxSaveName-len(ext)] + ext
	}
	dir := filepath.Join(tmp, "moomux-images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// CreateTemp: a random prefix it will not reuse (O_EXCL), mode 0600.
	f, err := os.CreateTemp(dir, "*-"+safe)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// lineSuffix is one piece of a compiler/grep location on the end of a
// path: the ":42" and ":7" of "Foo.swift:42:7", or the bare ":" that
// compilers and rg print after it ("main.go:12:5: undefined: x").
var lineSuffix = regexp.MustCompile(`:\d*$`)

// mb is n bytes in megabytes, rounded up to one decimal place, so a size
// just over a limit never reads as equal to it: "32.1 MB is over the 32 MB
// limit", not "32 MB".
func mb(n int64) string {
	return strconv.FormatFloat(math.Ceil(float64(n)*10/(1<<20))/10, 'f', -1, 64)
}

// stripLocation drops a compiler/grep location from a path that isn't
// there, one piece at a time — trailing ":", then ":col", then ":line" —
// stopping at the first path that exists, so a real file whose name ends
// in ":42" wins over stripping.
func stripLocation(path string) string {
	for range 3 {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			break
		}
		loc := lineSuffix.FindStringIndex(path)
		if loc == nil {
			break
		}
		path = path[:loc[0]]
	}
	return path
}

// resolveFile turns a path as tapped in a session's pane into the file it
// names, applying every rule both ReadFile and ResolveFile answer by, and
// returns an open os.Root holding it (the caller closes it), the file's
// path relative to that root, and the path to name in an error.
//
// A relative path is tried against each of bases in order — the pane's
// cwd, then the worktree — and the first that exists wins. Where it
// resolves is separate from what may be served: only files inside one of
// roots (the worktree and the temp dirs agents write screenshots to) are,
// so a pane cd'd to /etc can't widen that. Containment is checked after
// symlinks are resolved on both sides, so neither "../" nor a symlink
// pointing out gets past it, and a caller that opens the file goes through
// the returned os.Root, so a symlink swapped in after the check cannot
// either. Errors are shown to the user verbatim, so they say what went
// wrong in plain words; the method's name is put in front of them in
// dispatch, since both methods share every one.
func resolveFile(bases, roots []string, path string) (*os.Root, string, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, "", "", errors.New("empty path")
	}
	// Expanded so a ~ path is judged by where it really is: refused as
	// outside, or served when it is inside the worktree.
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[1:])
		}
	}
	if filepath.IsAbs(path) {
		path = stripLocation(path)
	} else {
		rel := path
		for _, base := range bases {
			if base == "" {
				continue
			}
			// The last base tried is what a "does not exist" names.
			path = stripLocation(filepath.Join(base, rel))
			if _, err := os.Lstat(path); err == nil {
				break
			}
		}
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", "", readErr(path, err)
	}
	for _, dir := range roots {
		if dir == "" {
			continue
		}
		dir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(dir, real)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return nil, "", "", readErr(path, err)
		}
		// Checked before any open, not only after: opening a named pipe
		// blocks until something writes to it, and the phone would wait
		// forever.
		info, err := root.Stat(rel)
		if err == nil {
			err = regular(path, info)
		} else {
			err = readErr(path, err)
		}
		if err != nil {
			root.Close()
			return nil, "", "", err
		}
		return root, rel, path, nil
	}
	return nil, "", "", fmt.Errorf("%s is outside this session's worktree", path)
}

// readFile is SaveFile's mirror: the bytes of a file a path in a session's
// pane names, for a front end that cannot read this machine's disk (a phone
// showing a tapped path in Quick Look). It answers the resolved absolute
// path and the contents. Resolution and refusals are resolveFile's.
func readFile(bases, roots []string, path string) (string, []byte, error) {
	root, rel, path, err := resolveFile(bases, roots, path)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	f, info, err := openRegular(root, rel, path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	if info.Size() > maxSaveFile {
		return "", nil, fmt.Errorf("%s MB is over the %d MB limit", mb(info.Size()), maxSaveFile>>20)
	}
	// Limited in case the file grows between the Stat and the read.
	data, err := io.ReadAll(io.LimitReader(f, maxSaveFile+1))
	if err != nil {
		return "", nil, readErr(path, err)
	}
	if len(data) > maxSaveFile {
		return "", nil, fmt.Errorf("%s MB is over the %d MB limit", mb(int64(len(data))), maxSaveFile>>20)
	}
	return filepath.Join(root.Name(), rel), data, nil
}

// resolvePath is ResolveFile: resolveFile's answer as an absolute path,
// for a front end on this machine (the Mac) that opens the file itself.
// Nothing is read, so no size limit applies.
func resolvePath(bases, roots []string, path string) (string, error) {
	root, rel, _, err := resolveFile(bases, roots, path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return filepath.Join(root.Name(), rel), nil
}

// openRegular opens name under root and refuses it unless it is a plain
// file. O_NONBLOCK because the Stat before it is not enough on its own: a
// named pipe swapped in between the two would block a plain open until
// something wrote to it. Reads of a regular file ignore the flag.
func openRegular(root *os.Root, name, path string) (*os.File, os.FileInfo, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, readErr(path, err)
	}
	info, err := f.Stat()
	if err == nil {
		err = regular(path, info)
	} else {
		err = readErr(path, err)
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// regular refuses anything but a plain file, in the phone's words.
func regular(path string, info os.FileInfo) error {
	if info.IsDir() {
		return fmt.Errorf("%s is a folder, not a file", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// readErr words a filesystem error for the phone's alert, which shows it
// as-is: "lstat …: not a directory" means nothing to someone who tapped a
// path.
func readErr(path string, err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return fmt.Errorf("%s does not exist", path)
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%s can't be read — permission denied", path)
	case errors.Is(err, syscall.ELOOP):
		return fmt.Errorf("%s is a symlink that loops back on itself", path)
	case strings.Contains(err.Error(), "path escapes from parent"):
		// os.Root's refusal: a symlink changed after the containment check.
		return fmt.Errorf("%s is outside this session's worktree", path)
	}
	return fmt.Errorf("%s can't be read: %w", path, err)
}

// attachInput is the client's keystroke stream once an Attach request has
// been read: whatever the decoder pulled in past the request, then the
// connection itself.
//
// Minus the newline that terminated the request, which is not a keystroke.
// A Decoder stops at the end of the JSON value and leaves the terminator in
// its buffer, so without this it becomes the first byte written to the pty —
// an Enter typed into the agent's pane on every attach, which in an agent
// pane submits whatever was sitting in the input box.
//
// The terminator is stripped from the first read, not from dec.Buffered():
// over TCP — the tailnet front door, and a client in another language that
// may write the JSON and its newline as two sends — the decoder can stop at
// the closing brace with nothing buffered, leaving the newline to arrive on
// the connection itself. Stripping only what a Read returned keeps this from
// blocking for a keystroke it might then eat.
//
// One terminator, and never a bare "\r" — an encoder writes "\n", while a
// terminal sends CR for Return, so a lone CR is a keypress, not framing.
func attachInput(dec *json.Decoder, r io.Reader) io.Reader {
	rest, _ := io.ReadAll(dec.Buffered())
	return &unterminated{r: io.MultiReader(bytes.NewReader(rest), r)}
}

// unterminated drops one leading newline from the first non-empty read.
type unterminated struct {
	r    io.Reader
	done bool
}

func (u *unterminated) Read(p []byte) (int, error) {
	n, err := u.r.Read(p)
	if u.done || n == 0 {
		return n, err
	}
	u.done = true
	for _, nl := range [][]byte{[]byte("\r\n"), []byte("\n")} {
		if after, ok := bytes.CutPrefix(p[:n], nl); ok {
			return copy(p, after), err
		}
	}
	return n, err
}

// stream pushes snapshots until the client hangs up. The write error on a
// closed connection is what ends it — there's no unsubscribe.
//
// The client's half of the connection isn't dead air: each line it sends is
// a nudge, asking the source for a snapshot now rather than at its next
// tick (see sessionview.Source.Nudge). Reading it is also how a client that
// hangs up releases this goroutine immediately, rather than at the next
// snapshot write.
func (s *Server) stream(c net.Conn, r io.Reader) {
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

// fileScope is where ReadFile and ResolveFile look for session id's paths:
// the bases a relative path resolves against, and the roots a file must be
// inside to be served.
// fileErr puts the method's name in front of a shared file error, the way
// SaveFile's read ("SaveFile: empty file"): ReadFile and ResolveFile give
// the same refusals, so the helpers leave the name to the caller.
func fileErr(method string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", method, err)
}

func (s *Server) fileScope(id string) (bases, roots []string, err error) {
	sessions := s.Backend.Sessions()
	i := slices.IndexFunc(sessions, func(s session.Session) bool { return s.ID == id })
	if i < 0 {
		return nil, nil, fmt.Errorf("no session %q — it may have been deleted", id)
	}
	sess := sessions[i]
	// The pane's cwd first: an agent that ran `cd Sources` prints paths
	// relative to that. No tmux answer (parked, no hook) is just the
	// worktree.
	var cwd string
	if s.PaneCwd != nil && sess.TmuxSession != "" {
		cwd, _ = s.PaneCwd(sess.TmuxSession)
	}
	// SaveFile's dir rather than all of os.TempDir(), which on macOS is
	// every app's per-user temp files; /tmp is where agents write
	// screenshots.
	return []string{cwd, sess.WorktreePath},
		[]string{sess.WorktreePath, filepath.Join(os.TempDir(), "moomux-images"), "/tmp"}, nil
}

func (s *Server) dispatch(method string, a Args) (Result, error) {
	b := s.Backend
	switch method {
	case "Config":
		if s.Config == nil {
			return Result{}, errors.New("server has no config")
		}
		return cfgResult(s.Config()), nil
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
	case "SaveFile":
		path, err := saveFile(os.TempDir(), a.Name, a.Data)
		return Result{Path: path}, err
	case "ReadFile":
		bases, roots, err := s.fileScope(a.ID)
		if err != nil {
			return Result{}, fileErr(method, err)
		}
		path, data, err := readFile(bases, roots, a.Path)
		return Result{Path: path, Data: data}, fileErr(method, err)
	case "ResolveFile":
		bases, roots, err := s.fileScope(a.ID)
		if err != nil {
			return Result{}, fileErr(method, err)
		}
		path, err := resolvePath(bases, roots, a.Path)
		return Result{Path: path}, fileErr(method, err)
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
	case "Capture":
		return Result{Screens: b.Capture(a.IDs)}, nil
	case "Review":
		hint, err := b.Review(a.ID)
		return Result{Hint: hint}, err
	case "Diff":
		p, ok, err := b.Diff(a.ID)
		return Result{Patch: p.Text, Base: p.Base, Truncated: p.Truncated, OK: ok}, err
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
	case "ReorderSessions":
		return Result{}, b.ReorderSessions(a.IDs)
	case "MoveSession":
		// Deprecated: ReorderSessions replaced this, taking the caller's
		// fully-resolved order rather than a delta the core has to resolve
		// against a session list the client may be displaying differently.
		// Kept because the macOS app still calls it and lives in another
		// repo with no CI link back here, so dropping the method would break
		// its reordering silently at runtime. It resolves the delta against
		// the core's own order, which is exactly the imprecision that
		// motivated the new method — remove this once moomux-mac sends an
		// order instead of a delta.
		return Result{}, s.moveSession(b, a.ID, a.Delta)
	case "MoveProject":
		return s.mutResult(b.MoveProject(a.Name, a.Delta))

	case "CreateFolder":
		return s.mutResult(b.CreateFolder(a.Name))
	case "SetSessionFolder":
		// Unlike the other session Set*/Rename methods (sessionResult),
		// this one can also create a folder — a config mutation — on its
		// first use, so the response needs both a Session and (mutResult's)
		// Cfg snapshot, not just one or the other.
		sess, err := b.SetSessionFolder(a.ID, a.Name)
		res, _ := s.mutResult(nil)
		res.Session = &sess
		return res, err
	case "RenameFolder":
		return s.mutResult(b.RenameFolder(a.Name, a.NewName))
	case "SetFolderCollapsed":
		return s.mutResult(b.SetFolderCollapsed(a.Name, a.On))
	case "DeleteFolder":
		return s.mutResult(b.DeleteFolder(a.Name))
	case "ReorderFolders":
		return s.mutResult(b.ReorderFolders(a.Names))
	case "SetProjectCollapsed":
		return s.mutResult(b.SetProjectCollapsed(a.Project, a.On))

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

// moveSession shifts one session by delta within its project and persists
// the result — the delta-based reorder the deprecated MoveSession method
// still exposes. Out-of-bounds is a no-op, not an error, matching the
// behaviour that method has always had.
//
// It moves in the same blocks the list is drawn in (sessionview.Reorder),
// so a caller that knows nothing about folders still can't drop a session
// into the middle of one — the resulting order would only be regrouped on
// the next render anyway.
func (s *Server) moveSession(b tui.Backend, id string, delta int) error {
	sessions := b.Sessions()
	project := ""
	for _, sess := range sessions {
		if sess.ID == id {
			project = sess.Project
			break
		}
	}
	if project == "" {
		return fmt.Errorf("unknown session %q", id)
	}
	var folders map[string]config.FolderMeta
	if s.Config != nil {
		cfg := s.Config()
		// Folders are one global namespace now; cfg.Projects[*].Folders is
		// the migration source and reads nil after config.Load, so a lookup
		// there would silently hand BuildRows an empty map and let this
		// shim drop a session into the middle of a folder again.
		folders = cfg.Folders
	}
	ids, ok := sessionview.Reorder(sessionview.BuildRows(sessions, folders, project), id, delta, nil)
	if !ok {
		return nil
	}
	return b.ReorderSessions(ids)
}

// mutResult adapts the config-mutating Backend methods (all bare error
// returns) by attaching the post-mutation config snapshot to a successful
// response — see Client.mut, which applies it in place instead of making a
// second "Config" round trip for every settings change.
func (s *Server) mutResult(err error) (Result, error) {
	if err != nil || s.Config == nil {
		return Result{}, err
	}
	return cfgResult(s.Config()), nil
}

// cfgResult wraps a config snapshot for the wire, adding the derived
// project emoji table so a non-Go front end doesn't need its own copy of
// config.ProjectEmojiPalette.
func cfgResult(cfg config.Config) Result {
	emoji := make(map[string]string, len(cfg.Projects))
	for name := range cfg.Projects {
		emoji[name] = cfg.ProjectEmoji(name)
	}
	return Result{Cfg: &cfg, ProjectEmoji: emoji}
}
