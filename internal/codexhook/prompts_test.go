package codexhook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureKillPromptCreatesFile(t *testing.T) {
	home := t.TempDir()
	changed, err := EnsureKillPrompt(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first install")
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "prompts", "kill.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != killPrompt {
		t.Fatalf("unexpected content: %s", data)
	}
}

func TestEnsureKillPromptIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureKillPrompt(home); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureKillPrompt(home)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second install should be a no-op")
	}
}

func TestEnsureKillPromptSkipsNoopWrite(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureKillPrompt(home); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".codex", "prompts", "kill.md")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	backdated := before.ModTime().Add(-time.Hour)
	if err := os.Chtimes(path, backdated, backdated); err != nil {
		t.Fatal(err)
	}

	if changed, err := EnsureKillPrompt(home); err != nil {
		t.Fatal(err)
	} else if changed {
		t.Fatal("expected changed=false for a no-op call")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(backdated) {
		t.Fatalf("expected no-op call to leave the file untouched, mtime changed from %v to %v", backdated, after.ModTime())
	}
}

func TestEnsureKillPromptOverwritesStaleContent(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "prompts", "kill.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale content from an older moomux build"), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := EnsureKillPrompt(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true when existing content differs")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != killPrompt {
		t.Fatalf("unexpected content: %s", data)
	}
}

func TestEnsureTagPromptCreatesFile(t *testing.T) {
	home := t.TempDir()
	changed, err := EnsureTagPrompt(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first install")
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "prompts", "tag.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != tagPrompt {
		t.Fatalf("unexpected content: %s", data)
	}
}

func TestEnsureTagPromptIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureTagPrompt(home); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureTagPrompt(home)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second install should be a no-op")
	}
}

func TestEnsureSpawnPromptCreatesFile(t *testing.T) {
	home := t.TempDir()
	changed, err := EnsureSpawnPrompt(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first install")
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "prompts", "spawn.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != spawnPrompt {
		t.Fatalf("unexpected content: %s", data)
	}
}

func TestEnsureSpawnPromptIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureSpawnPrompt(home); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureSpawnPrompt(home)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second install should be a no-op")
	}
}

func TestEnsureCommandsInstallCurrentSkillsAndLegacyPrompts(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name    string
		install func(string) (bool, error)
	}{
		{"kill", EnsureKillCommand},
		{"tag", EnsureTagCommand},
		{"spawn", EnsureSpawnCommand},
		{"reseed", EnsureReseedCommand},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed, err := tt.install(home)
			if err != nil {
				t.Fatal(err)
			}
			if !changed {
				t.Fatal("expected changed=true on first install")
			}

			skillPath := filepath.Join(home, ".agents", "skills", tt.name, "SKILL.md")
			data, err := os.ReadFile(skillPath)
			if err != nil {
				t.Fatalf("expected current Codex skill at %s: %v", skillPath, err)
			}
			if !strings.Contains(string(data), "name: "+tt.name+"\n") {
				t.Fatalf("skill has wrong or missing name frontmatter: %s", data)
			}
			if !strings.Contains(string(data), "description: ") {
				t.Fatalf("skill is missing description frontmatter: %s", data)
			}
			if tt.name == "spawn" && strings.Contains(string(data), "$ARGUMENTS") {
				t.Fatalf("spawn skill must use invocation input, not the legacy $ARGUMENTS placeholder: %s", data)
			}
			if !strings.Contains(string(data), "require_escalated") {
				t.Fatalf("%s skill must escape the sandbox for moomux's shared state: %s", tt.name, data)
			}
			if !strings.Contains(string(data), "moomux "+tt.name) && tt.name != "kill" {
				t.Fatalf("%s skill must invoke the corresponding moomux command: %s", tt.name, data)
			}
			if tt.name == "kill" && !strings.Contains(string(data), "moomux park") {
				t.Fatalf("kill skill must invoke moomux park: %s", data)
			}

			promptPath := filepath.Join(home, ".codex", "prompts", tt.name+".md")
			if _, err := os.Stat(promptPath); err != nil {
				t.Fatalf("expected legacy Codex prompt at %s: %v", promptPath, err)
			}

			changed, err = tt.install(home)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Fatal("second install should be a no-op")
			}
		})
	}
}
