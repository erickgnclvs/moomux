package antigravity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Antigravity discovers a skill by its directory layout and frontmatter, and
// only then exposes it as /<name>. Get either wrong and the slash command
// simply never appears — no error anywhere — so pin both.
func TestEnsureCommandsWriteDiscoverableSkills(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name    string
		install func(string) (bool, error)
	}{
		{"kill", EnsureKillCommand},
		{"tag", EnsureTagCommand},
		{"spawn", EnsureSpawnCommand},
		{"reseed", EnsureReseedCommand},
	} {
		changed, err := tc.install(home)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !changed {
			t.Fatalf("%s: changed = false on first install", tc.name)
		}

		path := filepath.Join(home, ".gemini", "config", "skills", tc.name, "SKILL.md")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		want := "---\nname: " + tc.name + "\ndescription: "
		if !strings.HasPrefix(string(body), want) {
			t.Fatalf("%s: frontmatter = %q, want prefix %q", tc.name, string(body), want)
		}
		if !strings.Contains(string(body), "moomux ") {
			t.Fatalf("%s: body names no moomux command:\n%s", tc.name, body)
		}

		// Idempotent: a reinstall must not report a change, so a moomux
		// startup doesn't look like it rewrote the user's config.
		if changed, err := tc.install(home); err != nil || changed {
			t.Fatalf("%s: reinstall changed = %v, err = %v", tc.name, changed, err)
		}
	}
}
