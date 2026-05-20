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
	if err := c.OpenSession("curral-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, "tmux attach -t curral-foo") {
		t.Fatalf("missing attach: %s", fr.script)
	}
	if !strings.Contains(fr.script, "iTerm2") {
		t.Fatalf("missing iTerm2 target: %s", fr.script)
	}
	if !strings.Contains(fr.script, `set name to "feat/bar"`) {
		t.Fatalf("missing tab title: %s", fr.script)
	}
	// OSC 0 sequence must precede the attach command so the tab re-renders immediately.
	if !strings.Contains(fr.script, `printf '\033]0;feat/bar\007'`) {
		t.Fatalf("missing OSC title sequence: %s", fr.script)
	}
}

func TestITermOpenSessionOmitsTitleWhenEmpty(t *testing.T) {
	fr := &fakeRunner{}
	c := &itermClient{runner: fr}
	if err := c.OpenSession("curral-foo", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fr.script, "set name to") {
		t.Fatalf("should not set name when title empty: %s", fr.script)
	}
}

func TestITermEscapesAppleScript(t *testing.T) {
	fr := &fakeRunner{}
	c := &itermClient{runner: fr}
	if err := c.OpenSession("curral-foo", `branch"with\special`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, `branch\"with\\special`) {
		t.Fatalf("backslash/quote not escaped: %s", fr.script)
	}
}
