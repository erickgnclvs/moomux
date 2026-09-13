package terminal

import (
	"errors"
	"testing"
)

func withAttachedClient(t *testing.T, names []string) {
	t.Helper()
	withAttachedClients(t, map[int][]string{4242: names}, 4242)
}

// withAttachedClients stubs several attached clients at once, each with its
// own ancestry.
func withAttachedClients(t *testing.T, byPID map[int][]string, pids ...int) {
	t.Helper()
	origPIDs := attachedClientPIDs
	origAncestors := processAncestors
	t.Cleanup(func() {
		attachedClientPIDs = origPIDs
		processAncestors = origAncestors
	})
	attachedClientPIDs = func() ([]int, error) { return pids, nil }
	processAncestors = func(pid int) []string { return byPID[pid] }
}

func TestDetectFromProcessTreeFindsITerm(t *testing.T) {
	withAttachedClient(t, []string{"zsh", "login", "iTerm2"})
	if _, ok := detectFromProcessTree().(*itermClient); !ok {
		t.Fatalf("expected *itermClient, got %T", detectFromProcessTree())
	}
}

func TestDetectFromProcessTreeFindsAppleTerminal(t *testing.T) {
	withAttachedClient(t, []string{"zsh", "login", "Terminal"})
	got := detectFromProcessTree()
	wo, ok := got.(*windowOpener)
	if !ok || wo.binary != "open" {
		t.Fatalf("expected windowOpener{open}, got %#v", got)
	}
}

func TestDetectFromProcessTreeReturnsNilForUnknown(t *testing.T) {
	withAttachedClient(t, []string{"zsh", "sshd"})
	if got := detectFromProcessTree(); got != nil {
		t.Fatalf("expected nil, got %T", got)
	}
}

func TestDetectFromProcessTreeReturnsNilWhenClientLookupFails(t *testing.T) {
	origPIDs := attachedClientPIDs
	t.Cleanup(func() { attachedClientPIDs = origPIDs })
	attachedClientPIDs = func() ([]int, error) { return nil, errors.New("no attached client") }

	if got := detectFromProcessTree(); got != nil {
		t.Fatalf("expected nil, got %T", got)
	}
}

func TestParsePS(t *testing.T) {
	ppid, name, ok := parsePS("    1 /Users/moo/Library/Application Support/iTerm2/iTermServer-3.7.1\n")
	if !ok || ppid != 1 || name != "iTermServer-3.7.1" {
		t.Fatalf("got (%d, %q, %v)", ppid, name, ok)
	}
	if _, name, _ := parsePS("87003 -/bin/zsh\n"); name != "zsh" {
		t.Fatalf("login shell: got %q", name)
	}
	if _, _, ok := parsePS("garbage"); ok {
		t.Fatal("want !ok for an unparseable line")
	}
}

func TestClientPIDsByActivity(t *testing.T) {
	got := clientPIDsByActivity("1789307864 30650\n1789310155 78402\n\n")
	if len(got) != 2 || got[0] != 78402 || got[1] != 30650 {
		t.Fatalf("want [78402 30650] (most recent first), got %v", got)
	}
	if got := clientPIDsByActivity(""); len(got) != 0 {
		t.Fatalf("want no clients, got %v", got)
	}
}

// iTerm2 3.7 hosts panes under iTermServer-<version>; the bare app name
// never shows up in the ancestry.
func TestDetectFromProcessTreeFindsITermServer(t *testing.T) {
	withAttachedClient(t, []string{"zsh", "login", "iTermServer-3.7.1"})
	if _, ok := detectFromProcessTree().(*itermClient); !ok {
		t.Fatalf("expected *itermClient, got %T", detectFromProcessTree())
	}
}

// Two emulators attached at once: the tab belongs in the one the user was
// last active in, not whichever tmux happens to list first.
func TestDetectFromProcessTreePrefersMostRecentlyActiveClient(t *testing.T) {
	clients := map[int][]string{
		1: {"zsh", "login", "ghostty"},
		2: {"zsh", "login", "iTermServer-3.7.1"},
	}
	withAttachedClients(t, clients, 2, 1) // already ordered by activity
	if _, ok := detectFromProcessTree().(*itermClient); !ok {
		t.Fatalf("expected *itermClient, got %T", detectFromProcessTree())
	}
}

// A session attached from both a local emulator and something windowless
// (mosh, ssh) has to find the emulator whichever order tmux lists them in —
// the local one is the only client that can be handed a new tab.
func TestDetectFromProcessTreeScansEveryAttachedClient(t *testing.T) {
	clients := map[int][]string{
		1: {"sh", "mosh-server"},
		2: {"zsh", "login", "ghostty"},
	}
	for _, order := range [][]int{{1, 2}, {2, 1}} {
		withAttachedClients(t, clients, order...)
		if got := detectFromProcessTree(); got == nil {
			t.Fatalf("order %v: expected ghostty opener, got nil", order)
		}
	}
}

// LocalClient is what lets the front end ignore a stale "you are remote"
// env var: moomux's own environment is frozen at pane creation, but the
// client attached to its tmux session is not.
func TestLocalClient(t *testing.T) {
	withAttachedClients(t, map[int][]string{1: {"zsh", "login", "ghostty"}}, 1)
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	if !LocalClient() {
		t.Fatal("a ghostty-hosted client is attached; want local")
	}

	withAttachedClients(t, map[int][]string{1: {"sh", "mosh-server"}}, 1)
	if LocalClient() {
		t.Fatal("only a windowless client is attached; want remote")
	}

	withAttachedClients(t, map[int][]string{1: {"zsh", "login", "ghostty"}}, 1)
	t.Setenv("TMUX", "")
	if LocalClient() {
		t.Fatal("not inside tmux; there is no attached client to speak of")
	}
}
