package claudehook

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureKillCommandCreatesFile(t *testing.T) {
	home := t.TempDir()
	changed, err := EnsureKillCommand(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first install")
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "commands", "kill.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != killCommand {
		t.Fatalf("unexpected content: %s", data)
	}
}

func TestEnsureKillCommandIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureKillCommand(home); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureKillCommand(home)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second install should be a no-op")
	}
}

func TestEnsureKillCommandSkipsNoopWrite(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureKillCommand(home); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "commands", "kill.md")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	backdated := before.ModTime().Add(-time.Hour)
	if err := os.Chtimes(path, backdated, backdated); err != nil {
		t.Fatal(err)
	}

	if changed, err := EnsureKillCommand(home); err != nil {
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

func TestEnsureKillCommandOverwritesStaleContent(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "commands", "kill.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale content from an older moomux build"), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := EnsureKillCommand(home)
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
	if string(data) != killCommand {
		t.Fatalf("unexpected content: %s", data)
	}
}

func TestEnsureTagCommandCreatesFile(t *testing.T) {
	home := t.TempDir()
	changed, err := EnsureTagCommand(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first install")
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "commands", "tag.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != tagCommand {
		t.Fatalf("unexpected content: %s", data)
	}
}

func TestEnsureTagCommandIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureTagCommand(home); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureTagCommand(home)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second install should be a no-op")
	}
}

func TestEnsureSpawnCommandCreatesFile(t *testing.T) {
	home := t.TempDir()
	changed, err := EnsureSpawnCommand(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first install")
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "commands", "spawn.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != spawnCommand {
		t.Fatalf("unexpected content: %s", data)
	}
}

func TestEnsureSpawnCommandIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureSpawnCommand(home); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureSpawnCommand(home)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second install should be a no-op")
	}
}

func TestEnsureCommandLeavesAUsersOwnCommandAlone(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "commands", "kill.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "my own /kill: stop the dev server\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureKillCommand(home)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v, want untouched", changed, err)
	}
	if got, _ := os.ReadFile(path); string(got) != mine {
		t.Fatalf("user's command overwritten: %s", got)
	}
}
