package terminal

import (
	"strings"
	"testing"
)

type fakeRunner struct{ script string }

func (f *fakeRunner) Run(script string) (string, error) {
	f.script = script
	return "", nil
}

func TestITermOpenSessionAttachesAndSetsTitle(t *testing.T) {
	fr := &fakeRunner{}
	c := &itermClient{runner: fr}
	if _, err := c.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	// The target must be single-quoted: it is typed into an interactive
	// shell, and zsh's EQUALS expansion turns a bare leading "=" into a
	// command-path lookup ("zsh: moomux-foo not found").
	if !strings.Contains(fr.script, `tmux attach -t '=moomux-foo'`) {
		t.Fatalf("missing quoted attach: %s", fr.script)
	}
	if !strings.Contains(fr.script, "iTerm2") {
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
	c := &itermClient{runner: fr}
	if _, err := c.OpenSession(`moomux-foo"; do shell script "rm`, "bar"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, `tmux attach -t '=moomux-foo\"; do shell script \"rm'`) {
		t.Fatalf("tmux session not escaped: %s", fr.script)
	}
}

func TestITermOpenSessionOmitsTitleWhenEmpty(t *testing.T) {
	fr := &fakeRunner{}
	c := &itermClient{runner: fr}
	if _, err := c.OpenSession("moomux-foo", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fr.script, "set name to") {
		t.Fatalf("should not set name when title empty: %s", fr.script)
	}
}

func TestITermOpenSessionEscapesSingleQuoteInTmuxSession(t *testing.T) {
	// tmuxSession is currently always moomux-<name>-<hash> (never
	// attacker-controlled, per sanitizeName), but write text hands this
	// straight to a shell inside a single-quoted string — an embedded "'"
	// would otherwise close that string early and let anything after it run
	// as a separate shell command.
	fr := &fakeRunner{}
	c := &itermClient{runner: fr}
	if _, err := c.OpenSession("moomux-foo'; touch pwned; echo '", "bar"); err != nil {
		t.Fatal(err)
	}
	// Expect the standard close/escape/reopen trick, doubled for AppleScript's
	// own backslash escaping: '\\'' in the source decodes to '\'' at runtime,
	// once per embedded single quote in the input (two, here).
	if got := strings.Count(fr.script, `'\\''`); got != 2 {
		t.Fatalf("expected 2 escaped single quotes, got %d: %s", got, fr.script)
	}
}

func TestITermEscapesAppleScript(t *testing.T) {
	fr := &fakeRunner{}
	c := &itermClient{runner: fr}
	if _, err := c.OpenSession("moomux-foo", `branch"with\special`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, `branch\"with\\special`) {
		t.Fatalf("backslash/quote not escaped: %s", fr.script)
	}
}
