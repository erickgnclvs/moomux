package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTCCWarning(t *testing.T) {
	if runtime.GOOS != "darwin" {
		if w := tccWarning("/whatever"); w != "" {
			t.Fatalf("expected no warning on %s, got %q", runtime.GOOS, w)
		}
		t.Skip("TCC folder checks only apply on darwin")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		path  string
		warns bool
	}{
		{"documents root", filepath.Join(home, "Documents"), true},
		{"documents subdir", filepath.Join(home, "Documents", "projects"), true},
		{"desktop subdir", filepath.Join(home, "Desktop", "foo"), true},
		{"downloads subdir", filepath.Join(home, "Downloads", "foo"), true},
		{"unrelated dir", filepath.Join(home, "dev", "moomux"), false},
		{"lookalike prefix", filepath.Join(home, "DocumentsArchive"), false},
		{"empty path", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tccWarning(c.path) != ""
			if got != c.warns {
				t.Errorf("tccWarning(%q): got warning=%v, want %v", c.path, got, c.warns)
			}
		})
	}
}
