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
