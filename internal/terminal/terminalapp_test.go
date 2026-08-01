package terminal

import (
	"strings"
	"testing"
)

func TestTerminalAppOpenSessionAttachesAndSetsTitle(t *testing.T) {
	fr := &fakeRunner{}
	c := &terminalAppClient{runner: fr}
	if _, err := c.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	// Same single-quoting requirement as iTerm's write text: do script runs
	// the line through the window's interactive shell, and zsh's EQUALS
	// expansion turns a bare leading "=" into a command-path lookup.
	if !strings.Contains(fr.script, `tmux attach -t '=moomux-foo'`) {
		t.Fatalf("missing quoted attach: %s", fr.script)
	}
	if !strings.Contains(fr.script, "Terminal") {
		t.Fatalf("missing Terminal target: %s", fr.script)
	}
	if !strings.Contains(fr.script, `set custom title of win to "feat/bar"`) {
		t.Fatalf("missing tab title: %s", fr.script)
	}
}

func TestTerminalAppOpenSessionOmitsTitleWhenEmpty(t *testing.T) {
	fr := &fakeRunner{}
	c := &terminalAppClient{runner: fr}
	if _, err := c.OpenSession("moomux-foo", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fr.script, "set custom title") {
		t.Fatalf("should not set title when empty: %s", fr.script)
	}
}

func TestTerminalAppEscapesAppleScript(t *testing.T) {
	fr := &fakeRunner{}
	c := &terminalAppClient{runner: fr}
	if _, err := c.OpenSession("moomux-foo", `branch"with\special`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, `branch\"with\\special`) {
		t.Fatalf("backslash/quote not escaped: %s", fr.script)
	}
}
