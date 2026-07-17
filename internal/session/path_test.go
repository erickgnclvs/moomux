package session

import (
	"path/filepath"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := DefaultPath(); got != filepath.Join("/xdg", "moomux", "sessions.json") {
		t.Fatalf("got %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := DefaultPath(); !filepath.IsAbs(got) || filepath.Base(got) != "sessions.json" {
		t.Fatalf("got %q", got)
	}
}
