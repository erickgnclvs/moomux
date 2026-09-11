package terminal

import (
	"strings"
	"testing"
)

// noTTYs stands in for "nothing attached to that tmux session", so tests
// that only care about the create path don't shell out to a real tmux.
func noTTYs(string) []tmuxClient { return nil }

// ttysOf builds the attached-client list from ttys alone, for terminals
// that identify a tab by tty.
func ttysOf(ttys ...string) []tmuxClient {
	var cs []tmuxClient
	for _, t := range ttys {
		cs = append(cs, tmuxClient{TTY: t})
	}
	return cs
}

type fakeRunner struct {
	script  string   // last script run
	scripts []string // every script run, in order
	out     string   // out to return on the first call; outs takes over from the second
	outs    []string
	err     error
}

func (f *fakeRunner) Run(script string) (string, error) {
	f.script = script
	f.scripts = append(f.scripts, script)
	if n := len(f.scripts); n > 1 && len(f.outs) >= n-1 {
		return f.outs[n-2], f.err
	}
	return f.out, f.err
}

func TestITermOpenSessionAttachesAndSetsTitle(t *testing.T) {
	fr := &fakeRunner{}
	c := &itermClient{runner: fr, clients: noTTYs}
	if _, err := c.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	// The target must be single-quoted: it is typed into an interactive
	// shell, and zsh's EQUALS expansion turns a bare leading "=" into a
	// command-path lookup ("zsh: moomux-foo not found").
	if !strings.Contains(fr.script, `tmux attach -t '=moomux-foo'`) {
		t.Fatalf("missing quoted attach: %s", fr.script)
	}
	if !strings.Contains(fr.script, "com.googlecode.iterm2") {
		t.Fatalf("missing iTerm2 target: %s", fr.script)
	}
	if !strings.Contains(fr.script, `set name to "feat/bar"`) {
		t.Fatalf("missing tab title: %s", fr.script)
	}
}

func TestITermOpenSessionEscapesTmuxSession(t *testing.T) {
	// tmuxSession is currently always moomux-<name>-<hash> (never
	// attacker-controlled), but the AppleScript write-text argument gets
	// the same escaping as the title for defense-in-depth.
	fr := &fakeRunner{}
	c := &itermClient{runner: fr, clients: noTTYs}
	if _, err := c.OpenSession(`moomux-foo"; do shell script "rm`, "bar"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, `tmux attach -t '=moomux-foo\"; do shell script \"rm'`) {
		t.Fatalf("tmux session not escaped: %s", fr.script)
	}
}

func TestITermOpenSessionOmitsTitleWhenEmpty(t *testing.T) {
	fr := &fakeRunner{}
	c := &itermClient{runner: fr, clients: noTTYs}
	if _, err := c.OpenSession("moomux-foo", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fr.script, "set name to") {
		t.Fatalf("should not set name when title empty: %s", fr.script)
	}
}

// The tab a session is already open in is found by joining tmux's attached
// client tty to iTerm2's per-session tty — not by a remembered handle, which
// wouldn't survive a restart of either side.
func TestITermOpenFindsExistingTabByTTY(t *testing.T) {
	fr := &fakeRunner{out: "sess-uuid"}
	c := &itermClient{runner: fr, clients: func(string) []tmuxClient { return ttysOf("/dev/ttys011") }}
	hint, err := c.OpenSession("moomux-foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	if hint != "" {
		t.Fatalf("want no hint, got %q", hint)
	}
	if len(fr.scripts) != 1 {
		t.Fatalf("want exactly one applescript call, got %d", len(fr.scripts))
	}
	if !strings.Contains(fr.script, `tty of sess is "/dev/ttys011"`) {
		t.Fatalf("missing tty lookup: %s", fr.script)
	}
	if strings.Contains(fr.script, "tmux attach") {
		t.Fatalf("should not re-attach when the tab still exists: %s", fr.script)
	}
}

// With nothing attached to the tmux session there is no tab to raise, so a
// fresh one is created.
func TestITermOpenCreatesWhenNothingAttached(t *testing.T) {
	fr := &fakeRunner{out: "new-tab-id"}
	c := &itermClient{runner: fr, clients: noTTYs}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if len(fr.scripts) != 1 {
		t.Fatalf("want a single create call, no select attempt, got %d", len(fr.scripts))
	}
	if !strings.Contains(fr.script, "tmux attach") {
		t.Fatalf("want a create+attach: %s", fr.script)
	}
}

// tmux's attached client can be some other terminal (a plain Terminal.app
// window, another host over ssh), so a tty iTerm2 doesn't hold is a miss,
// not an error — try the next one, then open a tab.
func TestITermOpenSkipsTTYsITermDoesNotHold(t *testing.T) {
	fr := &fakeRunner{out: "notfound", outs: []string{"sess-uuid"}}
	c := &itermClient{runner: fr, clients: func(string) []tmuxClient {
		return ttysOf("/dev/ttys011", "/dev/ttys022")
	}}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if len(fr.scripts) != 2 {
		t.Fatalf("want two select attempts and no create, got %d calls", len(fr.scripts))
	}
	for _, sc := range fr.scripts {
		if strings.Contains(sc, "tmux attach") {
			t.Fatalf("should not have created a tab: %s", sc)
		}
	}
}

func TestITermOpenCreatesWhenNoTabHoldsTheTTY(t *testing.T) {
	fr := &fakeRunner{out: "notfound", outs: []string{"new-tab-id"}}
	c := &itermClient{runner: fr, clients: func(string) []tmuxClient { return ttysOf("/dev/ttys011") }}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if len(fr.scripts) != 2 {
		t.Fatalf("want a select attempt followed by a create, got %d calls", len(fr.scripts))
	}
	if !strings.Contains(fr.scripts[1], `tmux attach -t '=moomux-foo'`) {
		t.Fatalf("second call should create+attach a new tab: %s", fr.scripts[1])
	}
}

func TestITermCloseTabClosesMatchingTab(t *testing.T) {
	fr := &fakeRunner{out: "closed"}
	c := &itermClient{runner: fr, clients: noTTYs}
	if err := c.CloseTab("tab-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, `id of sess is "tab-1"`) {
		t.Fatalf("missing tab id lookup: %s", fr.script)
	}
	if !strings.Contains(fr.script, "close t") {
		t.Fatalf("missing close: %s", fr.script)
	}
	if strings.Contains(fr.script, "activate") {
		t.Fatalf("close should not steal focus: %s", fr.script)
	}
}

func TestITermCloseTabMissingIsNotAnError(t *testing.T) {
	fr := &fakeRunner{out: "notfound"}
	c := &itermClient{runner: fr, clients: noTTYs}
	if err := c.CloseTab("tab-1"); err != nil {
		t.Fatal(err)
	}
}

func TestITermEscapesAppleScript(t *testing.T) {
	fr := &fakeRunner{}
	c := &itermClient{runner: fr, clients: noTTYs}
	if _, err := c.OpenSession("moomux-foo", `branch"with\special`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, `branch\"with\\special`) {
		t.Fatalf("backslash/quote not escaped: %s", fr.script)
	}
}

// FindTab answers "which tab is this session in" without raising it — the
// close path needs the id, not the user's attention.
func TestITermFindTabDoesNotRaise(t *testing.T) {
	fr := &fakeRunner{out: "sess-uuid"}
	c := &itermClient{runner: fr, clients: func(string) []tmuxClient { return ttysOf("/dev/ttys011") }}
	id, err := c.FindTab("moomux-foo")
	if err != nil {
		t.Fatal(err)
	}
	if id != "sess-uuid" {
		t.Fatalf("want the session id, got %q", id)
	}
	for _, forbidden := range []string{"activate", "select t", "select w"} {
		if strings.Contains(fr.script, forbidden) {
			t.Fatalf("FindTab must not %q: %s", forbidden, fr.script)
		}
	}
}

func TestITermFindTabEmptyWhenNothingAttached(t *testing.T) {
	fr := &fakeRunner{out: "notfound"}
	c := &itermClient{runner: fr, clients: noTTYs}
	id, err := c.FindTab("moomux-foo")
	if err != nil || id != "" {
		t.Fatalf("want empty id and no error, got %q / %v", id, err)
	}
	if len(fr.scripts) != 0 {
		t.Fatalf("want no applescript at all, got %d calls", len(fr.scripts))
	}
}
