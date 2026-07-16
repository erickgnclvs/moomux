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
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tui"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// screens maps a scenario name to the key sequence that drives a freshly
// created Model from the list screen into that scenario. "list" needs no
// keys. Each entry is sent as a tea.KeyMsg: the named keys below map to
// their special tea.KeyType, anything else is typed as literal runes (so a
// whole word like "demo/Documents/foo" types into the focused input in one
// step).
var screens = map[string][]string{
	"list":                   {},
	"new-session":            {"n"},
	"new-project":            {"P"},
	"tag":                    {"t"},
	"confirm-delete":         {"d"},
	"confirm-delete-project": {"D"},
	"archived":               {"A"},
	"help":                   {"?"},
	// Submits the new-project form with a path under ~/Documents that isn't
	// a git repo, landing on the "skip git" choice screen with its macOS
	// Files-and-Folders warning (see internal/tui/tcc.go). "$HOME" is
	// expanded to the real home dir at runtime so the warning actually
	// triggers regardless of machine.
	"project-init-choice": {"P", "demo2", "tab", "$HOME/Documents/projects", "enter"},
}

var namedKeys = map[string]tea.KeyType{
	"tab":       tea.KeyTab,
	"shift+tab": tea.KeyShiftTab,
	"enter":     tea.KeyEnter,
	"esc":       tea.KeyEsc,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
}

func keyMsgFor(s string) tea.KeyMsg {
	if kt, ok := namedKeys[s]; ok {
		return tea.KeyMsg{Type: kt}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// drive sends msg through Update, then synchronously runs any returned
// tea.Cmd and feeds its resulting message back in — needed for scenarios
// like project-init-choice where the form submission dispatches an async
// backend call (AddProject) whose result message drives the mode switch.
func drive(m *tui.Model, msg tea.Msg) {
	_, cmd := m.Update(msg)
	runCmd(m, cmd)
}

func runCmd(m *tui.Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmd(m, c)
		}
		return
	}
	_, next := m.Update(msg)
	runCmd(m, next)
}

type fakeBackend struct {
	sessions []session.Session
}

func (f *fakeBackend) CreateSession(project, name, agent, existingBranch, ticket string) (session.Session, string, error) {
	return session.Session{}, "", nil
}
func (f *fakeBackend) OpenSession(id string) (string, error)    { return "", nil }
func (f *fakeBackend) DeleteSession(id string) error            { return nil }
func (f *fakeBackend) KillTmux(id string) error                 { return nil }
func (f *fakeBackend) MoveSession(id string, delta int) error   { return nil }
func (f *fakeBackend) MoveProject(name string, delta int) error { return nil }
func (f *fakeBackend) SetSessionTags(id, ticket, pr string) (session.Session, error) {
	return session.Session{}, nil
}
func (f *fakeBackend) SetSessionArchived(id string, archived bool) (session.Session, error) {
	return session.Session{}, nil
}
func (f *fakeBackend) TmuxAliveAll() map[string]bool                         { return map[string]bool{} }
func (f *fakeBackend) Sessions() []session.Session                           { return f.sessions }
func (f *fakeBackend) Projects() []string                                    { return nil }
func (f *fakeBackend) AddProject(name string, p config.Project) error        { return gitwt.ErrNotGitRepo }
func (f *fakeBackend) InitProjectAndAdd(name string, p config.Project) error { return nil }
func (f *fakeBackend) AddPlainProject(name string, p config.Project) error   { return nil }
func (f *fakeBackend) RemoveProject(name string) error                       { return nil }

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

	home, _ := os.UserHomeDir()

	m.Update(tea.WindowSizeMsg{Width: *width, Height: *height})
	for _, k := range keys {
		drive(m, keyMsgFor(strings.ReplaceAll(k, "$HOME", home)))
	}

	fmt.Print(m.View())
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
