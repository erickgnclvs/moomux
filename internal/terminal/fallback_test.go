package terminal

import (
	"bytes"
	"strings"
	"testing"
)

func TestFallbackPrintsAttachCommand(t *testing.T) {
	var buf bytes.Buffer
	f := &fallbackOpener{out: &buf}
	if err := f.OpenSession("curral-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "tmux attach -t curral-foo") {
		t.Fatalf("expected attach command in output, got: %s", got)
	}
}
