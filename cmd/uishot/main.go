// Command uishot renders a moomux TUI screen to stdout as raw ANSI, using a
// fake backend and canned sample data — no real projects, git repos, or tmux
// sessions required. Pair it with scripts/screenshot.sh, which wraps the
// ANSI capture in a pty (so lipgloss emits color), converts it to HTML, and
// renders that HTML to a PNG with a headless browser.
//
// See CLAUDE.md ("UI changes") for when to run this.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tui"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// screens maps a scenario name to the key sequence (sent as individual
// tea.KeyMsg) that drives a freshly created Model from the list screen into
// that scenario. "list" needs no keys.
var screens = map[string][]string{
	"list":                   {},
	"new-session":            {"n"},
	"new-project":            {"P"},
	"tag":                    {"t"},
	"confirm-delete":         {"d"},
	"confirm-delete-project": {"D"},
	"archived":               {"A"},
	"diff":                   {"v"},
}

type fakeBackend struct {
	sessions []session.Session
}

func (f *fakeBackend) CreateSession(project, name, agent, existingBranch, ticket string) (session.Session, string, error) {
	return session.Session{}, "", nil
}
func (f *fakeBackend) OpenSession(id string) (string, error)  { return "", nil }
func (f *fakeBackend) DeleteSession(id string) error          { return nil }
func (f *fakeBackend) KillTmux(id string) error               { return nil }
func (f *fakeBackend) MoveSession(id string, delta int) error { return nil }
func (f *fakeBackend) SetSessionTags(id, ticket, pr string) (session.Session, error) {
	return session.Session{}, nil
}
func (f *fakeBackend) SetSessionArchived(id string, archived bool) (session.Session, error) {
	return session.Session{}, nil
}
func (f *fakeBackend) Diff(id string) (string, error) { return sampleDiff, nil }
func (f *fakeBackend) DiffStat(id string) (session.DiffStat, error) {
	switch id {
	case "demo:feature-auth":
		return session.DiffStat{Files: 3, Additions: 82, Deletions: 14}, nil
	case "demo:bugfix-timeout":
		return session.DiffStat{Files: 1, Additions: 6, Deletions: 2}, nil
	default:
		return session.DiffStat{}, nil
	}
}
func (f *fakeBackend) TmuxAliveAll() map[string]bool                         { return map[string]bool{} }
func (f *fakeBackend) Sessions() []session.Session                           { return f.sessions }
func (f *fakeBackend) Projects() []string                                    { return nil }
func (f *fakeBackend) AddProject(name string, p config.Project) error        { return nil }
func (f *fakeBackend) InitProjectAndAdd(name string, p config.Project) error { return nil }
func (f *fakeBackend) AddPlainProject(name string, p config.Project) error   { return nil }
func (f *fakeBackend) RemoveProject(name string) error                       { return nil }

// sampleDiff is canned `git diff` output for the diff-view screenshot.
const sampleDiff = `diff --git a/internal/auth/login.go b/internal/auth/login.go
index 3f9a1c2..b7e40d8 100644
--- a/internal/auth/login.go
+++ b/internal/auth/login.go
@@ -12,9 +12,17 @@ type Session struct {
 	Token     string
 	ExpiresAt time.Time
 }

-func Login(user, pass string) (*Session, error) {
-	if user == "" {
-		return nil, errors.New("missing user")
+func Login(user, pass string) (*Session, error) {
+	if user == "" || pass == "" {
+		return nil, ErrMissingCredentials
+	}
+	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
+	if err != nil {
+		return nil, fmt.Errorf("hash password: %w", err)
 	}
-	return &Session{Token: newToken()}, nil
+	return &Session{
+		Token:     newToken(),
+		ExpiresAt: time.Now().Add(24 * time.Hour),
+	}, nil
 }
diff --git a/internal/auth/errors.go b/internal/auth/errors.go
new file mode 100644
index 0000000..a1b2c3d
--- /dev/null
+++ b/internal/auth/errors.go
@@ -0,0 +1,7 @@
+package auth
+
+import "errors"
+
+// ErrMissingCredentials is returned when a login is missing a user or pass.
+var ErrMissingCredentials = errors.New("missing credentials")
diff --git a/internal/auth/login_test.go b/internal/auth/login_test.go
index 5c6d7e8..9f0a1b2 100644
--- a/internal/auth/login_test.go
+++ b/internal/auth/login_test.go
@@ -3,6 +3,10 @@ package auth
 import "testing"

 func TestLogin(t *testing.T) {
-	if _, err := Login("alice", "pw"); err != nil {
+	if _, err := Login("alice", "hunter2"); err != nil {
 		t.Fatalf("unexpected error: %v", err)
 	}
+	if _, err := Login("", ""); err == nil {
+		t.Fatal("expected error for empty credentials")
+	}
 }
`

func sampleSessions() []session.Session {
	now := time.Now().UTC()
	return []session.Session{
		{
			ID:           "demo:feature-auth",
			Project:      "demo",
			Name:         "feature-auth",
			Branch:       "feature/auth",
			WorktreePath: "/tmp/demo/feature-auth",
			TmuxSession:  "moomux-feature-auth",
			CreatedAt:    now,
			Agent:        "claude",
			Ticket:       "https://tracker.example/TICK-123",
		},
		{
			ID:           "demo:bugfix-timeout",
			Project:      "demo",
			Name:         "bugfix-timeout",
			Branch:       "bugfix/timeout",
			WorktreePath: "/tmp/demo/bugfix-timeout",
			TmuxSession:  "moomux-bugfix-timeout",
			CreatedAt:    now,
			Agent:        "codex",
			PR:           "https://github.com/example/repo/pull/42",
		},
		{
			ID:           "demo:old-spike",
			Project:      "demo",
			Name:         "old-spike",
			Branch:       "spike/old-idea",
			WorktreePath: "/tmp/demo/old-spike",
			TmuxSession:  "moomux-old-spike",
			CreatedAt:    now,
			Agent:        "claude",
			Archived:     true,
		},
	}
}

func main() {
	screen := flag.String("screen", "list", fmt.Sprintf("screen to render: %s", screenNames()))
	width := flag.Int("width", 100, "terminal width")
	height := flag.Int("height", 32, "terminal height")
	flag.Parse()

	keys, ok := screens[*screen]
	if !ok {
		fmt.Fprintf(os.Stderr, "uishot: unknown screen %q (want one of: %s)\n", *screen, screenNames())
		os.Exit(1)
	}

	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{sessions: sampleSessions()}
	statusCh := make(chan watcher.Snapshot)
	m := tui.New(cfg, be, statusCh, func() {})

	m.Update(tea.WindowSizeMsg{Width: *width, Height: *height})
	// Init dispatches background work (e.g. the per-session diff-stat scan);
	// drain it so the detail pane shows real numbers, then drive the keys and
	// drain each one's follow-up command (e.g. the diff-view async load).
	drain(m, m.Init())
	for _, k := range keys {
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		drain(m, cmd)
	}

	fmt.Print(m.View())
}

// drain runs cmd and feeds any resulting message back into the model,
// recursing into batched and follow-up commands. Commands that don't return
// promptly (e.g. the status-watcher channel read or the flash ticker) are
// skipped via a short timeout, so the harness never blocks on the real
// event loop it isn't running.
func drain(m *tui.Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := runWithTimeout(cmd)
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drain(m, c)
		}
		return
	}
	_, next := m.Update(msg)
	drain(m, next)
}

func runWithTimeout(cmd tea.Cmd) tea.Msg {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(300 * time.Millisecond):
		return nil
	}
}

func screenNames() string {
	names := make([]string, 0, len(screens))
	for name := range screens {
		names = append(names, name)
	}
	sort.Strings(names)
	out := ""
	for i, name := range names {
		if i > 0 {
			out += ", "
		}
		out += name
	}
	return out
}
