package ipc

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/sessionview"
	"github.com/erickgnclvs/moomux/internal/watcher"
	"sync/atomic"
)

// boolPtr is CreateSession's dangerous argument: non-nil forces the value
// regardless of the project's own Dangerous setting.
func boolPtr(b bool) *bool { return &b }

// fakeBackend records what it was called with and returns canned values, so
// a round trip can assert both directions of the wire.
type fakeBackend struct {
	sessions  []session.Session
	created   session.CreateRequest
	createErr error
	renamed   session.Session

	addProjectWarning string
	addProjectErr     error
	onAddProject      func(string, config.Project)
	cfg               *config.Config
	agentOptions      []config.AgentOption
	mu                sync.Mutex
}

func (f *fakeBackend) SuggestedProject() (string, string) { return "", "" }

func (f *fakeBackend) Sessions() []session.Session { return f.sessions }
func (f *fakeBackend) Projects() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cfg != nil {
		return f.cfg.OrderedProjectNames() // reads the same map AddProject writes
	}
	return []string{"moomux", "other"}
}
func (f *fakeBackend) TmuxAliveAll() map[string]bool {
	return map[string]bool{"moomux:a": true, "moomux:b": false}
}

func (f *fakeBackend) CreateSession(req session.CreateRequest) (session.Session, string, error) {
	f.created = req
	if f.createErr != nil {
		return session.Session{}, "", f.createErr
	}
	return session.Session{ID: "moomux:new", Name: req.Name}, "run: tmux attach -t x", nil
}
func (f *fakeBackend) OpenSession(id string) (string, error) { return "opened " + id, nil }
func (f *fakeBackend) DeleteSession(id string) (string, error) {
	return "", errors.New("worktree dirty")
}
func (f *fakeBackend) KillTmux(string) error                    { return nil }
func (f *fakeBackend) WorktreeStatus(string) (bool, bool, bool) { return true, false, true }
func (f *fakeBackend) ChangeSummary(string) (int, int, bool)    { return 7, 3, true }
func (f *fakeBackend) PRStatus(string) (prstatus.Info, bool) {
	return prstatus.Info{State: "OPEN", Mergeable: "MERGEABLE", CI: "PASSING"}, true
}
func (f *fakeBackend) SetSessionStatusTitle(string, watcher.State) error { return nil }
func (f *fakeBackend) SetSessionTags(id, ticket, pr string) (session.Session, error) {
	return session.Session{ID: id, Ticket: ticket, PR: pr}, nil
}
func (f *fakeBackend) SetSessionPrompt(id, p string) (session.Session, error) {
	return session.Session{ID: id, Prompt: p}, nil
}
func (f *fakeBackend) SetSessionAgent(id, a string, d bool) (session.Session, error) {
	return session.Session{ID: id, Agent: a, Dangerous: d}, nil
}
func (f *fakeBackend) RenameSession(id, name string) (session.Session, error) {
	f.renamed = session.Session{ID: id, Name: name}
	return f.renamed, nil
}
func (f *fakeBackend) SetSessionArchived(id string, on bool) (session.Session, error) {
	return session.Session{ID: id, Archived: on}, nil
}
func (f *fakeBackend) MoveSession(string, int) error { return nil }
func (f *fakeBackend) MoveProject(string, int) error { return nil }
func (f *fakeBackend) AddProject(name string, p config.Project) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onAddProject != nil {
		f.onAddProject(name, p)
	}
	return f.addProjectWarning, f.addProjectErr
}
func (f *fakeBackend) InitProjectAndAdd(string, config.Project) error { return nil }
func (f *fakeBackend) AddPlainProject(string, config.Project) error   { return nil }
func (f *fakeBackend) UpdateProject(string, config.Project) error     { return nil }
func (f *fakeBackend) RemoveProject(string) error                     { return nil }
func (f *fakeBackend) SetTheme(theme, appearance string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cfg != nil {
		f.cfg.Theme = theme
		f.cfg.Appearance = appearance
	}
	return nil
}
func (f *fakeBackend) SetAutoSubmitDefault(bool) error { return nil }
func (f *fakeBackend) SetSortRecentFirst(bool) error   { return nil }
func (f *fakeBackend) SetAutoTmux(bool) error          { return nil }
func (f *fakeBackend) SetCompactDetail(bool) error     { return nil }

// ConfigSnapshot satisfies tui.Backend; unused here — these tests exercise
// the wire protocol (Client/Server), not tui.Update()'s Msg.Cfg handling.
func (f *fakeBackend) ConfigSnapshot() config.Config { return config.Config{} }

type fakeWatcher struct {
	snaps  []sessionview.Snapshot
	nudges atomic.Int64
}

func (w *fakeWatcher) Nudge() { w.nudges.Add(1) }

func (w *fakeWatcher) Run(ctx context.Context, out chan<- sessionview.Snapshot) {
	for _, s := range w.snaps {
		select {
		case out <- s:
		case <-ctx.Done():
			return
		}
	}
}

// start brings up a server on a temp socket and returns a connected client.
func start(t *testing.T, b *fakeBackend, cfg *config.Config, src sessionview.Source) (*Client, *trackingListener) {
	t.Helper()
	// Short path on purpose: unix socket paths are capped near 104 bytes on
	// macOS, and t.TempDir() under /var/folders is long enough to matter.
	dir := filepath.Join("/tmp", fmt.Sprintf("moomux-ipc-%d-%d", os.Getpid(), time.Now().UnixNano()))
	sock := filepath.Join(dir, "moomux.sock")
	raw, err := Listen(sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ln := &trackingListener{Listener: raw}
	t.Cleanup(func() { ln.kill(); os.RemoveAll(dir) })
	b.cfg = cfg
	srv := &Server{Backend: b, Config: snapshotter(b, cfg), AgentOptions: func() []config.AgentOption { return b.agentOptions }, Source: src}
	go srv.Serve(ln)
	return &Client{Socket: sock}, ln
}

// snapshotter mimics app.App.ConfigSnapshot: a copy taken under the fake's
// lock, so the server never hands out a pointer into mutating state.
func snapshotter(b *fakeBackend, cfg *config.Config) func() config.Config {
	if cfg == nil {
		return nil
	}
	return func() config.Config {
		b.mu.Lock()
		defer b.mu.Unlock()
		c := *cfg
		c.Projects = maps.Clone(cfg.Projects)
		return c
	}
}

// trackingListener remembers accepted connections so a test can simulate the
// server process dying. Closing the listener alone doesn't do it — live
// connections stay open and the client never notices.
type trackingListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func (l *trackingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.conns = append(l.conns, c)
		l.mu.Unlock()
	}
	return c, err
}

// connCount reports how many connections have been accepted so far.
func (l *trackingListener) connCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.conns)
}

// kill drops the listener and every connection it handed out.
func (l *trackingListener) kill() {
	l.Listener.Close()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		c.Close()
	}
	l.conns = nil
}

// serveOn starts another server on an existing path, for restart tests.
func serveOn(t *testing.T, sock string, b *fakeBackend, cfg *config.Config, src sessionview.Source) *trackingListener {
	t.Helper()
	raw, err := Listen(sock)
	if err != nil {
		t.Fatalf("Listen(%s): %v", sock, err)
	}
	ln := &trackingListener{Listener: raw}
	t.Cleanup(ln.kill)
	b.cfg = cfg
	go (&Server{Backend: b, Config: snapshotter(b, cfg), Source: src}).Serve(ln)
	return ln
}

func TestRoundTrip(t *testing.T) {
	created := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions: []session.Session{
			{ID: "moomux:a", Project: "moomux", Name: "a", Branch: "alan/a", CreatedAt: created, Agent: "codex", Dangerous: true, Order: 42},
		},
		agentOptions: []config.AgentOption{
			{Name: "claude", Models: []string{"default", "sonnet"}, Thinking: []string{"default", "think"}},
			{Name: "opencode", Thinking: []string{"default", "think"}},
		},
	}
	c, _ := start(t, b, &config.Config{Theme: "dracula"}, nil)

	t.Run("agent options survive the wire", func(t *testing.T) {
		got, err := c.AgentOptions()
		if err != nil {
			t.Fatalf("AgentOptions: %v", err)
		}
		if !reflect.DeepEqual(got, b.agentOptions) {
			t.Fatalf("AgentOptions() = %+v, want %+v", got, b.agentOptions)
		}
	})

	t.Run("themes survive the wire", func(t *testing.T) {
		got, err := c.Themes()
		if err != nil {
			t.Fatalf("Themes: %v", err)
		}
		if !reflect.DeepEqual(got, config.Themes()) {
			t.Fatalf("Themes() = %+v, want %+v", got, config.Themes())
		}
	})

	t.Run("sessions survive the wire", func(t *testing.T) {
		got := c.Sessions()
		if len(got) != 1 || got[0] != b.sessions[0] {
			t.Fatalf("Sessions() = %+v, want %+v", got, b.sessions)
		}
	})

	t.Run("args reach the backend", func(t *testing.T) {
		req := session.CreateRequest{
			Project: "moomux", Name: "feat", Agent: "claude", Ticket: "T-1", PR: "https://x/y/pull/1",
			BaseBranch: "main", Model: "opus", Thinking: "high", Prompt: "do it",
			AutoSubmit: true, OpenTerminal: true, Dangerous: boolPtr(true),
		}
		s, hint, err := c.CreateSession(req)
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if s.ID != "moomux:new" || hint != "run: tmux attach -t x" {
			t.Fatalf("got (%+v, %q)", s, hint)
		}
		// Every field of the request must survive the round trip: the core
		// runs the whole create transaction off it, so a field dropped on
		// the wire is a step silently skipped. DeepEqual because Dangerous
		// is a *bool, where != would compare pointer identity.
		if !reflect.DeepEqual(b.created, req) {
			t.Fatalf("backend saw %+v, want %+v", b.created, req)
		}
	})

	t.Run("SetSessionAgent's dangerous survives the wire, including an explicit false", func(t *testing.T) {
		// SetSessionAgent shares CreateSession's Args.Dangerous *bool field,
		// but unlike CreateSession it's never "unset" — the client always
		// sends an explicit pointer, so an explicit false must not collapse
		// to the same wire value as an absent field (which the server reads
		// as false too, but for a different reason).
		s, err := c.SetSessionAgent("moomux:a", "codex", true)
		if err != nil {
			t.Fatalf("SetSessionAgent: %v", err)
		}
		if !s.Dangerous {
			t.Fatalf("SetSessionAgent(true) round-tripped as %+v, want Dangerous=true", s)
		}
		s, err = c.SetSessionAgent("moomux:a", "codex", false)
		if err != nil {
			t.Fatalf("SetSessionAgent: %v", err)
		}
		if s.Dangerous {
			t.Fatalf("SetSessionAgent(false) round-tripped as %+v, want Dangerous=false", s)
		}
	})

	t.Run("multi-value returns", func(t *testing.T) {
		if dirty, unpushed, ok := c.WorktreeStatus("moomux:a"); !dirty || unpushed || !ok {
			t.Fatalf("WorktreeStatus = %v %v %v, want true false true", dirty, unpushed, ok)
		}
		if files, commits, ok := c.ChangeSummary("moomux:a"); files != 7 || commits != 3 || !ok {
			t.Fatalf("ChangeSummary = %d %d %v, want 7 3 true", files, commits, ok)
		}
	})

	t.Run("errors propagate", func(t *testing.T) {
		_, err := c.DeleteSession("moomux:a")
		if err == nil || err.Error() != "worktree dirty" {
			t.Fatalf("DeleteSession err = %v, want \"worktree dirty\"", err)
		}
	})

	t.Run("config", func(t *testing.T) {
		cfg, err := c.Config()
		if err != nil || cfg == nil || cfg.Theme != "dracula" {
			t.Fatalf("Config() = %+v, %v", cfg, err)
		}
	})
}

// TestMutMakesOneRoundTripNotTwo guards against a config-mutating call (here
// SetTheme) dialing twice — once for the mutation, once more to re-fetch
// Config — instead of the server attaching its post-mutation snapshot to the
// same response. Every settings toggle paid for a second dial+encode+decode
// of the whole Projects map before this. ConfigSnapshot (rather than a live
// *config.Config the mutation refreshes in place) is what tui.Update() reads
// the result from — see Client.mut and tui.Backend.ConfigSnapshot's doc
// comments for why the client no longer keeps a shared pointer in sync
// itself.
func TestMutMakesOneRoundTripNotTwo(t *testing.T) {
	cfg := &config.Config{Theme: "dracula"}
	b := &fakeBackend{}
	c, ln := start(t, b, cfg, nil)

	before := ln.connCount()
	if err := c.SetTheme("gruvbox", ""); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}
	if got := ln.connCount() - before; got != 1 {
		t.Fatalf("SetTheme made %d connections, want 1", got)
	}
	if got := c.ConfigSnapshot().Theme; got != "gruvbox" {
		t.Fatalf("ConfigSnapshot().Theme = %q, want the mutation's own response to have cached it", got)
	}
}

// view builds a one-entry Views map, the way the core would.
func view(id string, st watcher.State) map[string]sessionview.View {
	return map[string]sessionview.View{id: {
		ID: id, State: st, Label: sessionview.Label(st), Quip: sessionview.Quip(id, st),
	}}
}

// TestWatchStreams is the whole point of the stream: a snapshot arrives on
// the client side byte-identical to what the core built, labels and quips
// included, so a front end renders it rather than re-deriving it.
func TestWatchStreams(t *testing.T) {
	snaps := []sessionview.Snapshot{
		{Views: view("moomux:a", watcher.Working), PollTime: time.Now()},
		{Views: view("moomux:a", watcher.NeedsInput), PollTime: time.Now(), Err: "sqlite locked"},
	}
	c, _ := start(t, &fakeBackend{}, &config.Config{}, &fakeWatcher{snaps: snaps})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan sessionview.Snapshot, 4)
	go c.Run(ctx, out)

	for i, want := range snaps {
		select {
		case got := <-out:
			if got.Views["moomux:a"] != want.Views["moomux:a"] {
				t.Fatalf("snapshot %d view = %+v, want %+v", i, got.Views["moomux:a"], want.Views["moomux:a"])
			}
			if got.Err != want.Err {
				t.Fatalf("snapshot %d err = %q, want %q", i, got.Err, want.Err)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for snapshot %d", i)
		}
	}
}

func TestListenRejectsLiveSocket(t *testing.T) {
	c, _ := start(t, &fakeBackend{}, &config.Config{}, nil)
	if _, err := Listen(c.Socket); err == nil {
		t.Fatal("Listen on a live socket succeeded; want refusal so a second serve can't steal it")
	}
}

func TestUnknownMethod(t *testing.T) {
	c, _ := start(t, &fakeBackend{}, &config.Config{}, nil)
	if _, err := c.call("Nope", Args{}); err == nil {
		t.Fatal("unknown method returned no error")
	}
}

// TestSentinelErrorSurvivesWire covers the "add a project that isn't a git
// repo" flow: tui/update.go branches on errors.Is(err, gitwt.ErrNotGitRepo)
// to offer "git init it / add as plain". A plain string error would make
// that dialog unreachable over the socket.
func TestSentinelErrorSurvivesWire(t *testing.T) {
	b := &fakeBackend{
		addProjectErr:     fmt.Errorf("%w: /tmp/nope", gitwt.ErrNotGitRepo),
		addProjectWarning: "⚠ inside ~/Desktop",
	}
	c, _ := start(t, b, &config.Config{}, nil)

	warning, err := c.AddProject("p", config.Project{})
	if err == nil {
		t.Fatal("AddProject returned no error")
	}
	// The warning rides the same failed response: it's what the init/plain
	// dialog shows about the path, and it's a fact about the server's
	// machine, so a client must not be left to work it out itself.
	if warning != "⚠ inside ~/Desktop" {
		t.Errorf("warning = %q, want the core's note to survive alongside the error", warning)
	}
	if !errors.Is(err, gitwt.ErrNotGitRepo) {
		t.Errorf("errors.Is(err, ErrNotGitRepo) = false for %[1]T %[1]q; the init/plain dialog is unreachable", err)
	}
	if want := "not a git repository: /tmp/nope"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

// TestConfigSnapshotReflectsMutation covers ConfigSnapshot — the TUI calls
// this from a tea.Cmd closure right after a mutation, to hand Update() a
// fresh post-mutation config to apply on its own goroutine (see
// tui.Backend's doc comment on ConfigSnapshot). Without a real round trip
// here, a newly added project would never reach the TUI's list no matter how
// many times the server is asked.
func TestConfigSnapshotReflectsMutation(t *testing.T) {
	served := &config.Config{Projects: map[string]config.Project{"old": {Kind: "plain"}}}
	b := &fakeBackend{onAddProject: func(name string, p config.Project) {
		served.Projects[name] = p // what App.saveProject does to a.Cfg
	}}
	c, _ := start(t, b, served, nil)

	if _, err := c.AddProject("new", config.Project{Kind: "plain"}); err != nil {
		t.Fatal(err)
	}
	if snap := c.ConfigSnapshot(); snap.Projects["new"].Kind == "" {
		t.Errorf("ConfigSnapshot() = %v after AddProject; the TUI's project list would be stale", snap.Projects)
	}
}

func TestListenTightensPermissions(t *testing.T) {
	c, _ := start(t, &fakeBackend{}, &config.Config{}, nil)
	fi, err := os.Stat(c.Socket)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket mode = %#o, want 0600: any local user could dial it and call StartFirstPrompt", perm)
	}
}

func TestListenRefusesNonSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path); err == nil {
		t.Error("Listen replaced a regular file; a mistyped -socket would delete it")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "keep me\n" {
		t.Errorf("file was destroyed: %q, %v", string(b), err)
	}
}

// TestWatchEndsWhenWatcherStops guards against stream parking forever on a
// channel no one closes — the shape every real Source.Run has.
func TestWatchEndsWhenWatcherStops(t *testing.T) {
	c, _ := start(t, &fakeBackend{}, &config.Config{}, &fakeWatcher{snaps: nil})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out := make(chan sessionview.Snapshot, 1)
	done := make(chan struct{})
	go func() { c.Run(ctx, out); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancel; the stream goroutine leaks")
	}
}

func TestConfigRejectsNilFromServer(t *testing.T) {
	c, _ := start(t, &fakeBackend{}, nil, nil)
	if cfg, err := c.Config(); err == nil {
		t.Errorf("Config() = %v, nil; runProgram would nil-deref on it", cfg)
	}
}

// TestConcurrentConfigAccessIsRaceFree covers what goroutine-per-connection
// introduced: app.App has no lock around Cfg, and the local TUI never needed
// one because AddProject runs synchronously in Bubble Tea's Update loop.
// Served concurrently, `a.Cfg.Projects[name] = p` racing App.Projects() is a
// fatal concurrent map read/write that kills the server and every attached
// front end. Run under -race.
func TestConcurrentConfigAccessIsRaceFree(t *testing.T) {
	served := &config.Config{Projects: map[string]config.Project{}}
	b := &fakeBackend{onAddProject: func(name string, p config.Project) {
		served.Projects[name] = p // exactly what App.saveProject does to a.Cfg
	}}
	c, _ := start(t, b, served, nil)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.AddProject(fmt.Sprintf("p%d", i), config.Project{Kind: "plain"})
		}()
		wg.Add(2)
		go func() { defer wg.Done(); c.Sessions() }()
		go func() { defer wg.Done(); c.Config() }()
	}
	wg.Wait()
}

// TestSessionsSurviveTransportFailure covers the destructive shape of a
// failed call: tui.Backend's Sessions() can't return an error — it's called
// from the render path — so a nil on a dropped connection empties the whole
// list mid-render. Last-good beats empty.
func TestSessionsSurviveTransportFailure(t *testing.T) {
	want := []session.Session{{ID: "moomux:a", Name: "a"}, {ID: "moomux:b", Name: "b"}}
	b := &fakeBackend{sessions: want}
	c, ln := start(t, b, &config.Config{Projects: map[string]config.Project{"p": {}}}, nil)

	if got := c.Sessions(); len(got) != 2 {
		t.Fatalf("warm-up Sessions() = %d, want 2", len(got))
	}
	// Kill the server out from under the client.
	ln.kill()

	if got := c.Sessions(); len(got) != len(want) {
		t.Errorf("Sessions() after disconnect = %d entries, want %d cached; an empty list wipes m.states", len(got), len(want))
	}
}

// TestWatchReconnects covers a server restart: the stream must come back on
// its own, and must report the gap rather than leaving stale states on
// screen looking live.
func TestWatchReconnects(t *testing.T) {
	snap := sessionview.Snapshot{Views: map[string]sessionview.View{"moomux:a": {ID: "moomux:a", State: watcher.Working}}, PollTime: time.Now()}
	b := &fakeBackend{}
	// blockingWatcher keeps the stream open after its snapshot, the way a
	// real watcher does, so the connection only ends when the server stops.
	c, ln := start(t, b, &config.Config{}, &blockingWatcher{snap: snap})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out := make(chan sessionview.Snapshot, 16)
	go c.Run(ctx, out)

	if s := recvSnap(t, ctx, out, func(s sessionview.Snapshot) bool { return s.Views != nil }); s.Views["moomux:a"].State != watcher.Working {
		t.Fatalf("first snapshot = %v", s.Views)
	}

	// Drop the server the way a dying process would: listener and every
	// live connection.
	ln.kill()
	if s := recvSnap(t, ctx, out, func(s sessionview.Snapshot) bool { return s.Err != "" }); s.Err == "" {
		t.Fatal("no error snapshot after the server went away; the TUI would show stale states as live")
	}

	serveOn(t, c.Socket, b, &config.Config{}, &blockingWatcher{snap: snap})
	if s := recvSnap(t, ctx, out, func(s sessionview.Snapshot) bool { return s.Views != nil }); s.Views["moomux:a"].State != watcher.Working {
		t.Fatalf("did not reconnect; snapshot = %v", s.Views)
	}
}

type blockingWatcher struct{ snap sessionview.Snapshot }

func (w *blockingWatcher) Nudge() {}

func (w *blockingWatcher) Run(ctx context.Context, out chan<- sessionview.Snapshot) {
	select {
	case out <- w.snap:
	case <-ctx.Done():
		return
	}
	<-ctx.Done()
}

func recvSnap(t *testing.T, ctx context.Context, out <-chan sessionview.Snapshot, match func(sessionview.Snapshot) bool) sessionview.Snapshot {
	t.Helper()
	for {
		select {
		case s := <-out:
			if match(s) {
				return s
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for a matching snapshot")
		}
	}
}

// countingWatcher records how many times Run was called and keeps the
// stream open, emitting snap to whoever is listening until ctx ends.
type countingWatcher struct {
	mu   sync.Mutex
	runs int
	snap sessionview.Snapshot
}

func (w *countingWatcher) Run(ctx context.Context, out chan<- sessionview.Snapshot) {
	w.mu.Lock()
	w.runs++
	w.mu.Unlock()
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			select {
			case out <- w.snap:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (w *countingWatcher) Nudge() {}

func (w *countingWatcher) runCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.runs
}

// TestWatchSharesOneWatcher is the CPU fix: a source run per connection
// meant every attached front end paid for its own directory rescans, its own
// sqlite3 subprocess per database per tick, and (on macOS, where fsnotify's
// kqueue backend opens a descriptor per watched file) its own several
// hundred file descriptors — all producing identical snapshots.
func TestWatchSharesOneWatcher(t *testing.T) {
	w := &countingWatcher{snap: sessionview.Snapshot{
		Views:    view("moomux:a", watcher.Working),
		PollTime: time.Now(),
	}}
	c, _ := start(t, &fakeBackend{}, &config.Config{}, w)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Two independent clients, both streaming at once.
	for i := 0; i < 2; i++ {
		out := make(chan sessionview.Snapshot, 4)
		go c.Run(ctx, out)
		select {
		case snap := <-out:
			if snap.Views["moomux:a"].State != watcher.Working {
				t.Fatalf("client %d: got %v", i, snap.Views)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("client %d: no snapshot", i)
		}
	}

	if n := w.runCount(); n != 1 {
		t.Errorf("Source.Run called %d times for 2 clients, want 1", n)
	}
}

// TestWatchNudgeReachesSource covers the client's half of the stream: a
// nudge travels back up the live connection so a front end can ask for a
// snapshot now — after parking a session, say — instead of waiting out the
// core's tick.
func TestWatchNudgeReachesSource(t *testing.T) {
	w := &fakeWatcher{snaps: []sessionview.Snapshot{{Views: view("moomux:a", watcher.Working), PollTime: time.Now()}}}
	c, _ := start(t, &fakeBackend{}, &config.Config{}, w)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan sessionview.Snapshot, 4)
	go c.Run(ctx, out)
	select {
	case <-out: // connected
	case <-ctx.Done():
		t.Fatal("no first snapshot; never connected")
	}

	c.Nudge()
	for ctx.Err() == nil {
		if w.nudges.Load() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("nudge never reached the source")
}
