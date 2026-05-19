package iterm

import (
	"strings"
	"testing"
)

type fakeRunner struct {
	script string
}

func (f *fakeRunner) Run(script string) (string, error) {
	f.script = script
	return "", nil
}

func TestOpenTabComposesScript(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.OpenTab("curral-foo"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, "tmux attach -t curral-foo") {
		t.Fatalf("script missing attach: %s", fr.script)
	}
	if !strings.Contains(fr.script, "iTerm2") {
		t.Fatalf("script missing iTerm2 target: %s", fr.script)
	}
}
