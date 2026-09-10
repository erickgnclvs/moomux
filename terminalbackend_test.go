package main

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tui"
)

// stubBackend records which core call an open went through. Everything else
// on tui.Backend is unused here; the embedded nil interface would panic if
// this test ever reached it, which is the point.
type stubBackend struct {
	tui.Backend
	sessions   []session.Session
	calls      []string
	gotReq     session.CreateRequest
	createHint string
	// order, when shared with a fakeOpener, records core calls and terminal
	// calls in one sequence — the close path's correctness is which side
	// runs when, not just that both ran.
	order *[]string
}

func (s *stubBackend) note(what string) {
	if s.order != nil {
		*s.order = append(*s.order, what)
	}
}

func (s *stubBackend) EnsureTmux(id string) (string, error) {
	s.calls = append(s.calls, "EnsureTmux:"+id)
	return "", nil
}

func (s *stubBackend) OpenSession(id string) (string, error) {
	s.calls = append(s.calls, "OpenSession:"+id)
	return "", nil
}

func (s *stubBackend) Sessions() []session.Session { return s.sessions }

func (s *stubBackend) CreateSession(req session.CreateRequest) (session.Session, string, error) {
	s.gotReq = req
	s.calls = append(s.calls, "CreateSession")
	return session.Session{ID: "demo:a", Name: "a", TmuxSession: "moomux-a"}, s.createHint, nil
}

func (s *stubBackend) KillTmux(id string) error {
	s.calls = append(s.calls, "KillTmux:"+id)
	s.note("KillTmux")
	return nil
}

func (s *stubBackend) DeleteSession(id string) (string, error) {
	s.calls = append(s.calls, "DeleteSession:"+id)
	s.note("DeleteSession")
	return "gone", nil
}

// fakeOpener is a TerminalOpener that also finds and closes tabs, i.e. what
// every real terminal with a tab concept implements.
type fakeOpener struct {
	opened []string // tmux session names

	found  string   // what FindTab reports
	closed []string // tab ids passed to CloseTab
	order  *[]string
}

func (f *fakeOpener) OpenSession(tmuxSession, title string) (string, error) {
	f.opened = append(f.opened, tmuxSession)
	return "", nil
}

// Opening a session asks the core only to bring tmux up (EnsureTmux) and
// spawns the window here. The core's env — a launchd `moomux serve` has no
// TERM_PROGRAM at all — says nothing about the machine the user is looking
// at, so it is never asked to.
func TestTerminalBackendOpensLocallyNotInCore(t *testing.T) {
	b := &stubBackend{sessions: []session.Session{
		{ID: "demo:a", Name: "a", TmuxSession: "moomux-a"},
	}}
	term := &fakeOpener{}
	l := &terminalBackend{Backend: b, term: term}

	if _, err := l.OpenSession("demo:a"); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if len(b.calls) != 1 || b.calls[0] != "EnsureTmux:demo:a" {
		t.Fatalf("want only EnsureTmux over the wire, got %v", b.calls)
	}
	if len(term.opened) != 1 || term.opened[0] != "moomux-a" {
		t.Fatalf("want the local terminal opened on moomux-a, got %v", term.opened)
	}
}

// A session the core has but the local listing doesn't means no tmux name,
// so there is nothing to open. That has to be an error: there is no
// core-side terminal to fall back to, and opening nothing silently reads
// to the user as a dead keypress.
func TestTerminalBackendOpenErrorsWhenSessionIsNotListed(t *testing.T) {
	b := &stubBackend{}
	term := &fakeOpener{}
	l := &terminalBackend{Backend: b, term: term}

	if _, err := l.OpenSession("demo:a"); err == nil {
		t.Fatalf("want an error naming the situation")
	}
	if len(term.opened) != 0 {
		t.Fatalf("want nothing opened, got %v", term.opened)
	}
	for _, c := range b.calls {
		if c == "OpenSession:demo:a" {
			t.Fatalf("must not fall back to the core, which opens no terminals: %v", b.calls)
		}
	}
}

// A client that is itself an SSH window can't open a tab either — the
// emulator is on a third machine. The difference from before is only who
// asks the question: this process knows it's remote, the core never did.
func TestTerminalBackendRemoteClientJustHints(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/ttys001")
	b := &stubBackend{sessions: []session.Session{
		{ID: "demo:a", Name: "a", TmuxSession: "moomux-a"},
	}}
	term := &fakeOpener{}
	l := &terminalBackend{Backend: b, term: term}

	hint, err := l.OpenSession("demo:a")
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if len(term.opened) != 0 {
		t.Fatalf("want no terminal spawned over SSH, got %v", term.opened)
	}
	if hint != "tmux attach -t moomux-a" {
		t.Fatalf("want an attach hint, got %q", hint)
	}
}

func (f *fakeOpener) FindTab(tmuxSession string) (string, error) {
	f.note("FindTab")
	return f.found, nil
}

func (f *fakeOpener) note(what string) {
	if f.order != nil {
		*f.order = append(*f.order, what)
	}
}

func (f *fakeOpener) CloseTab(tabID string) error {
	f.note("CloseTab")
	f.closed = append(f.closed, tabID)
	return nil
}

// Parking closes the tab here — the core does no tab work for anyone. The
// lookup must happen before the core kills tmux (tmux is half the join
// terminal.TabFinder does) and the close after it.
//
// This is also what `moomux park` does, through parkSession: a detached
// worker with stderr to /dev/null, so it's the one front end whose terminal
// work would go missing unnoticed.
func TestTerminalBackendKillTmuxClosesTabAroundTheKill(t *testing.T) {
	var order []string
	b := &stubBackend{order: &order, sessions: []session.Session{
		{ID: "demo:a", Name: "a", TmuxSession: "moomux-a"},
	}}
	term := &fakeOpener{found: "sess-uuid", order: &order}
	l := &terminalBackend{Backend: b, term: term}

	if err := l.KillTmux("demo:a"); err != nil {
		t.Fatalf("KillTmux: %v", err)
	}
	if want := "FindTab KillTmux CloseTab"; strings.Join(order, " ") != want {
		t.Fatalf("want %q, got %q", want, strings.Join(order, " "))
	}
	if len(term.closed) != 1 || term.closed[0] != "sess-uuid" {
		t.Fatalf("want the found tab closed, got %v", term.closed)
	}
	if len(b.calls) != 1 || b.calls[0] != "KillTmux:demo:a" {
		t.Fatalf("want the park to still go to the core, got %v", b.calls)
	}
}

// Nothing attached: no tab to close, and the park still happens.
func TestTerminalBackendKillTmuxWithNoTab(t *testing.T) {
	b := &stubBackend{sessions: []session.Session{
		{ID: "demo:a", Name: "a", TmuxSession: "moomux-a"},
	}}
	term := &fakeOpener{}
	l := &terminalBackend{Backend: b, term: term}

	if err := l.KillTmux("demo:a"); err != nil {
		t.Fatalf("KillTmux: %v", err)
	}
	if len(term.closed) != 0 {
		t.Fatalf("want nothing closed, got %v", term.closed)
	}
	if len(b.calls) != 1 {
		t.Fatalf("want the park to still go to the core, got %v", b.calls)
	}
}

// Same rule as the core: the live lookup wins over a remembered handle, and
// a lookup that finds nothing closes nothing — a remembered kitty/wezterm
// id can name a stranger's tab after the terminal restarts. The handle is
// dropped either way, so a reopen doesn't chase a closed tab.
func TestTerminalBackendKillTmuxPrefersLookupOverRememberedHandle(t *testing.T) {
	b := &stubBackend{sessions: []session.Session{
		{ID: "demo:a", Name: "a", TmuxSession: "moomux-a"},
	}}
	term := &fakeOpener{found: "from-lookup"}
	l := &terminalBackend{Backend: b, term: term}

	if err := l.KillTmux("demo:a"); err != nil {
		t.Fatalf("KillTmux: %v", err)
	}
	if len(term.closed) != 1 || term.closed[0] != "from-lookup" {
		t.Fatalf("want the freshly found tab closed, got %v", term.closed)
	}
}

func TestTerminalBackendKillTmuxClosesNothingWhenLookupFindsNothing(t *testing.T) {
	b := &stubBackend{sessions: []session.Session{
		{ID: "demo:a", Name: "a", TmuxSession: "moomux-a"},
	}}
	term := &fakeOpener{}
	l := &terminalBackend{Backend: b, term: term}

	if err := l.KillTmux("demo:a"); err != nil {
		t.Fatalf("KillTmux: %v", err)
	}
	if len(term.closed) != 0 {
		t.Fatalf("want nothing closed, got %v", term.closed)
	}
}

// Deleting from a socket client has to close the tab for the same reason
// parking does, in the same order.
func TestTerminalBackendDeleteSessionClosesTabAroundTheDelete(t *testing.T) {
	var order []string
	b := &stubBackend{order: &order, sessions: []session.Session{
		{ID: "demo:a", Name: "a", TmuxSession: "moomux-a"},
	}}
	term := &fakeOpener{found: "sess-uuid", order: &order}
	l := &terminalBackend{Backend: b, term: term}

	hint, err := l.DeleteSession("demo:a")
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if hint != "gone" {
		t.Fatalf("want the core's hint passed through, got %q", hint)
	}
	if want := "FindTab DeleteSession CloseTab"; strings.Join(order, " ") != want {
		t.Fatalf("want %q, got %q", want, strings.Join(order, " "))
	}
	if len(term.closed) != 1 || term.closed[0] != "sess-uuid" {
		t.Fatalf("want the found tab closed, got %v", term.closed)
	}
}

// wantBackendMethods is the reviewed method set of tui.Backend. It exists
// because terminalBackend is a decorator: it overrides the methods with
// terminal side effects and inherits the rest, and the compiler cannot tell
// the difference between "inherited deliberately" and "forgotten". Adding a
// Backend method fails this test, which is the prompt to decide which kind
// it is.
//
// Overridden here, because each opens or closes a terminal:
//
//	CreateSession, OpenSession, KillTmux, DeleteSession
//
// Everything else is core-only work and passes straight through.
var wantBackendMethods = []string{
	"AddPlainProject", "AddProject", "ChangeSummary", "ConfigSnapshot",
	"CreateSession", "DeleteSession", "EnsureTmux", "InitProjectAndAdd",
	"KillTmux", "MoveProject", "MoveSession", "OpenSession", "RemoveProject",
	"RenameSession", "Sessions", "SetAutoSubmitDefault", "SetAutoTmux",
	"SetCompactDetail", "SetSessionAgent", "SetSessionArchived",
	"SetSessionPrompt", "SetSessionTags", "SetSortRecentFirst", "SetTheme",
	"SuggestedProject", "UpdateProject", "WorktreeStatus",
}

func TestTerminalBackendCoversEveryBackendMethod(t *testing.T) {
	iface := reflect.TypeOf((*tui.Backend)(nil)).Elem()
	var got []string
	for i := range iface.NumMethod() {
		got = append(got, iface.Method(i).Name)
	}
	sort.Strings(got)
	want := append([]string(nil), wantBackendMethods...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tui.Backend's method set changed.\ngot:  %v\nwant: %v\n\n"+
			"Decide whether the new method opens or closes a terminal. If it does, "+
			"override it on terminalBackend so it happens on the machine with the "+
			"terminal; either way, add it to wantBackendMethods.", got, want)
	}
}

// Creating a session with a terminal is the most common way to open one, so
// it happens here like every other terminal.
func TestTerminalBackendCreateSessionOpensLocally(t *testing.T) {
	b := &stubBackend{createHint: "hooks installed"}
	term := &fakeOpener{}
	l := &terminalBackend{Backend: b, term: term}

	s, hint, err := l.CreateSession(session.CreateRequest{Project: "demo", Name: "a", OpenTerminal: true})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if !b.gotReq.OpenTerminal {
		t.Fatalf("OpenTerminal must reach the core, which uses it for LastOpened and the hint")
	}
	if len(term.opened) != 1 || term.opened[0] != "moomux-a" {
		t.Fatalf("want the local terminal opened on moomux-a, got %v", term.opened)
	}
	if s.ID != "demo:a" {
		t.Fatalf("want the core's session passed through, got %+v", s)
	}
	if hint != "hooks installed" {
		t.Fatalf("want the core's hint kept, got %q", hint)
	}
}

// "Open in background" means nobody opens a terminal, and the core's
// manual-attach hint is the right thing to show.
func TestTerminalBackendCreateSessionInBackgroundIsUntouched(t *testing.T) {
	b := &stubBackend{}
	term := &fakeOpener{}
	l := &terminalBackend{Backend: b, term: term}

	if _, _, err := l.CreateSession(session.CreateRequest{Project: "demo", Name: "a"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if b.gotReq.OpenTerminal {
		t.Fatalf("nothing is opening a terminal; the core should still hint how to attach")
	}
	if len(term.opened) != 0 {
		t.Fatalf("want no terminal opened, got %v", term.opened)
	}
}
