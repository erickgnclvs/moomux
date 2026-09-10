package terminal

import (
	"errors"
	"strings"
	"testing"
)

func TestGhosttyOpenSessionOpensTabInFrontWindow(t *testing.T) {
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
	if !strings.Contains(fr.script, `tmux attach -t '=moomux-foo'`) {
		t.Fatalf("missing quoted attach: %s", fr.script)
	}
	if fb.tmuxSession != "" {
		t.Fatalf("fallback used despite a working script")
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
	if !strings.Contains(fr.script, `tmux attach -t '=moomux-foo\"; do shell script \"rm'`) {
		t.Fatalf("tmux session not escaped: %s", fr.script)
	}
}
