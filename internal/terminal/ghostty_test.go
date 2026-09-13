package terminal

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestGhosttyOpenSessionOpensTabInFrontWindow(t *testing.T) {
	withGhosttyTabStore(t, nil)
	fr := &fakeRunner{out: "tab-1"}
	fb := &fakeOpener{}
	c := &ghosttyClient{runner: fr, fallback: fb}
	if _, err := c.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, "new tab in front window") {
		t.Fatalf("expected a new tab, not a new window: %s", fr.script)
	}
	// The target must be single-quoted: Ghostty runs the configured
	// command through a shell, and zsh's EQUALS expansion turns a bare
	// leading "=" into a command-path lookup.
	if !strings.Contains(fr.script, `attach -t '=moomux-foo'`) {
		t.Fatalf("missing quoted attach: %s", fr.script)
	}
	if fb.tmuxSession != "" {
		t.Fatalf("fallback used despite a working script")
	}
}

// Ghostty runs the command in its own environment via `bash --noprofile
// --norc`, so a Dock-launched Ghostty has the bare system PATH and a plain
// "tmux" is not found on a Homebrew install.
func TestGhosttyRunsTmuxByAbsolutePath(t *testing.T) {
	withGhosttyTabStore(t, nil)
	fr := &fakeRunner{out: "tab-1"}
	c := &ghosttyClient{runner: fr, fallback: &fakeOpener{}}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	want, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux on PATH to resolve")
	}
	if !strings.Contains(fr.script, "command:\"'"+want+"' attach") {
		t.Fatalf("want absolute tmux path %s: %s", want, fr.script)
	}
}

func TestGhosttyOpenTabReturnsNewTabID(t *testing.T) {
	fr := &fakeRunner{out: "tab-9bc66d800\n"}
	c := &ghosttyClient{runner: fr, fallback: &fakeOpener{}}
	id, _, err := c.OpenTab("", "moomux-foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	if id != "tab-9bc66d800" {
		t.Fatalf("got tab id %q", id)
	}
}

func TestGhosttyOpenTabSelectsExistingTab(t *testing.T) {
	fr := &fakeRunner{out: "found"}
	c := &ghosttyClient{runner: fr, fallback: &fakeOpener{}}
	id, _, err := c.OpenTab("tab-1", "moomux-foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	if id != "tab-1" {
		t.Fatalf("got tab id %q, want the one we passed in", id)
	}
	if len(fr.scripts) != 1 {
		t.Fatalf("expected only a select, got %d scripts", len(fr.scripts))
	}
	if !strings.Contains(fr.script, "select tab t") {
		t.Fatalf("missing select: %s", fr.script)
	}
}

func TestGhosttyOpenTabOpensFreshTabWhenGone(t *testing.T) {
	fr := &fakeRunner{out: "notfound", outs: []string{"tab-2"}}
	c := &ghosttyClient{runner: fr, fallback: &fakeOpener{}}
	id, _, err := c.OpenTab("tab-1", "moomux-foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	if id != "tab-2" {
		t.Fatalf("got tab id %q, want the newly created tab", id)
	}
}

// A script failure means AppleScript isn't usable (macos-applescript off,
// automation permission denied, no front window to add a tab to) — falling
// back to a new ghostty window is better than opening nothing.
func TestGhosttyFallsBackWhenScriptFails(t *testing.T) {
	fr := &fakeRunner{err: errors.New("not authorized")}
	fb := &fakeOpener{hint: "fallback hint"}
	c := &ghosttyClient{runner: fr, fallback: fb}
	hint, err := c.OpenSession("moomux-foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	if fb.tmuxSession != "moomux-foo" || fb.title != "bar" {
		t.Fatalf("fallback called with wrong args: %+v", fb)
	}
	if hint != "fallback hint" {
		t.Fatalf("want fallback hint, got %q", hint)
	}
}

func TestGhosttyCloseTabDoesNotActivate(t *testing.T) {
	fr := &fakeRunner{out: "closed"}
	c := &ghosttyClient{runner: fr, fallback: &fakeOpener{}}
	if err := c.CloseTab("tab-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, "close tab t") {
		t.Fatalf("missing close: %s", fr.script)
	}
	if strings.Contains(fr.script, "activate") {
		t.Fatalf("close should not steal focus: %s", fr.script)
	}
}

func TestGhosttyEscapesTmuxSession(t *testing.T) {
	fr := &fakeRunner{out: "tab-1"}
	c := &ghosttyClient{runner: fr, fallback: &fakeOpener{}}
	if _, err := c.OpenSession(`moomux-foo"; do shell script "rm`, "bar"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, `attach -t '=moomux-foo\"; do shell script \"rm'`) {
		t.Fatalf("tmux session not escaped: %s", fr.script)
	}
}

// withGhosttyTabStore replaces the tmux-backed tab memory with an in-map
// one, so these tests neither need a tmux server nor touch a real session's
// environment.
func withGhosttyTabStore(t *testing.T, seed map[string]string) map[string]string {
	t.Helper()
	store := map[string]string{}
	for k, v := range seed {
		store[k] = v
	}
	origGet, origSet := ghosttyStoredTab, ghosttyRememberTab
	t.Cleanup(func() { ghosttyStoredTab, ghosttyRememberTab = origGet, origSet })
	ghosttyStoredTab = func(s string) string { return store[s] }
	ghosttyRememberTab = func(s, id string) { store[s] = id }
	return store
}

// Opening a session twice must land in the tab it already has, not pile up
// a new one on every press.
func TestGhosttyOpenSessionReusesRememberedTab(t *testing.T) {
	store := withGhosttyTabStore(t, nil)

	fr := &fakeRunner{out: "tab-1"}
	c := &ghosttyClient{runner: fr, fallback: &fakeOpener{}}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if store["moomux-foo"] != "tab-1" {
		t.Fatalf("tab id not remembered: %v", store)
	}

	fr = &fakeRunner{out: "found"}
	c = &ghosttyClient{runner: fr, fallback: &fakeOpener{}}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fr.script, "new tab") {
		t.Fatalf("opened a second tab instead of selecting tab-1: %s", fr.script)
	}
	if !strings.Contains(fr.script, "tab-1") {
		t.Fatalf("didn't select the remembered tab: %s", fr.script)
	}
}

// A remembered tab the user has since closed (or that a Ghostty restart
// invalidated) must fall through to a fresh tab, and the new id replace it.
func TestGhosttyOpenSessionReplacesStaleTab(t *testing.T) {
	store := withGhosttyTabStore(t, map[string]string{"moomux-foo": "tab-gone"})

	fr := &fakeRunner{out: "notfound", outs: []string{"tab-2"}}
	c := &ghosttyClient{runner: fr, fallback: &fakeOpener{}}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if store["moomux-foo"] != "tab-2" {
		t.Fatalf("stale id not replaced: %v", store)
	}
}

func TestGhosttyFindTabReturnsRememberedTab(t *testing.T) {
	withGhosttyTabStore(t, map[string]string{"moomux-foo": "tab-7"})
	c := &ghosttyClient{runner: &fakeRunner{}, fallback: &fakeOpener{}}
	got, err := c.FindTab("moomux-foo")
	if err != nil || got != "tab-7" {
		t.Fatalf("got (%q, %v)", got, err)
	}
	if got, _ := c.FindTab("moomux-other"); got != "" {
		t.Fatalf("want no tab for an unknown session, got %q", got)
	}
}
