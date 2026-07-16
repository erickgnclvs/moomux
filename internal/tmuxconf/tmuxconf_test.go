package tmuxconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAlreadyAppliedMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux.conf")
	if AlreadyApplied(path) {
		t.Fatal("expected false for a nonexistent file")
	}
}

func TestApplyThenAlreadyApplied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux.conf")
	if err := os.WriteFile(path, []byte("set -g status off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if AlreadyApplied(path) {
		t.Fatal("expected false before Apply")
	}
	if err := Apply(path); err != nil {
		t.Fatal(err)
	}
	if !AlreadyApplied(path) {
		t.Fatal("expected true after Apply")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "set -g status off") {
		t.Fatal("Apply must not clobber existing content")
	}
	if !strings.Contains(string(data), "set -g mouse on") {
		t.Fatal("Apply must append the recommended snippet")
	}
}

func TestApplyCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "tmux.conf")
	if err := Apply(path); err != nil {
		t.Fatal(err)
	}
	if !AlreadyApplied(path) {
		t.Fatal("expected true after Apply on a fresh file")
	}
}

func TestAlreadyAppliedDetectsManualEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux.conf")
	content := "set -g status off\n\n" + Marker + "\nset -g mouse on\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !AlreadyApplied(path) {
		t.Fatal("expected true when the user already added the marker manually")
	}
}
