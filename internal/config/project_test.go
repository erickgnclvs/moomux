package config

import (
	"path/filepath"
	"testing"
)

func TestIsPlainAndUsesWorktree(t *testing.T) {
	cases := []struct {
		p            Project
		plain, wtree bool
	}{
		{Project{Kind: "git"}, false, true},
		{Project{}, false, true}, // legacy configs have no kind
		{Project{Kind: "plain"}, true, false},
		{Project{Kind: "git", NoWorktree: true}, false, false},
	}
	for _, c := range cases {
		if got := c.p.IsPlain(); got != c.plain {
			t.Errorf("IsPlain(%+v) = %v", c.p, got)
		}
		if got := c.p.UsesWorktree(); got != c.wtree {
			t.Errorf("UsesWorktree(%+v) = %v", c.p, got)
		}
	}
}

// TestProjectEmojiMoomuxSpecialCase guards the hardcoded moomux -> cow glyph:
// without it, moomux's hash could land on any palette entry, same as any
// other unconfigured project.
func TestProjectEmojiMoomuxSpecialCase(t *testing.T) {
	c := &Config{Projects: map[string]Project{}}
	for _, name := range []string{"moomux", "MooMux"} {
		if got := c.ProjectEmoji(name); got != "🐄" {
			t.Errorf("ProjectEmoji(%q) = %q, want 🐄", name, got)
		}
	}
	// An explicit per-project override still wins over the special case.
	c.Projects["moomux"] = Project{Emoji: "🚀"}
	if got := c.ProjectEmoji("moomux"); got != "🚀" {
		t.Errorf("ProjectEmoji(moomux) with override = %q, want 🚀", got)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := DefaultPath(); got != filepath.Join("/xdg", "moomux", "config.toml") {
		t.Fatalf("got %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := DefaultPath(); !filepath.IsAbs(got) || filepath.Base(got) != "config.toml" {
		t.Fatalf("got %q", got)
	}
}
