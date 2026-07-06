package terminal

import (
	"strings"
	"testing"
)

func TestFallbackReturnsAttachHint(t *testing.T) {
	f := &fallbackOpener{}
	hint, err := f.OpenSession("moomux-foo", "feat/bar")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hint, "tmux attach -t moomux-foo") {
		t.Fatalf("expected attach command in hint, got: %s", hint)
	}
}
