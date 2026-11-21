package codexhook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readHooks(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal hooks.json: %v", err)
	}
	return m
}

func hookCommands(t *testing.T, settings map[string]any, event string) []string {
	t.Helper()
	hooks, _ := settings["hooks"].(map[string]any)
	entries, _ := hooks[event].([]any)
	var cmds []string
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for _, rc := range asSlice(entry["hooks"]) {
			c, ok := rc.(map[string]any)
			if !ok {
				continue
			}
			if cmd, _ := c["command"].(string); cmd != "" {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

func TestEnsureHooksCreatesHooksJSON(t *testing.T) {
	home := t.TempDir()
	changed, err := EnsureHooks(home)
	if err != nil {
		t.Fatalf("EnsureHooks: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true for a brand-new install")
	}
	settings := readHooks(t, home)
	if got := hookCommands(t, settings, "PermissionRequest"); len(got) != 1 || got[0] != "moomux hook codex set" {
		t.Fatalf("PermissionRequest hooks = %v", got)
	}
	if got := hookCommands(t, settings, "PreToolUse"); len(got) != 1 || got[0] != "moomux hook codex clear" {
		t.Fatalf("PreToolUse hooks = %v", got)
	}
	if got := hookCommands(t, settings, "UserPromptSubmit"); len(got) != 1 || got[0] != "moomux hook codex clear" {
		t.Fatalf("UserPromptSubmit hooks = %v", got)
	}
}

func TestEnsureHooksIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureHooks(home); err != nil {
		t.Fatalf("first EnsureHooks: %v", err)
	}
	changed, err := EnsureHooks(home)
	if err != nil {
		t.Fatalf("second EnsureHooks: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false when hooks are already installed")
	}
	settings := readHooks(t, home)
	if got := hookCommands(t, settings, "PermissionRequest"); len(got) != 1 {
		t.Fatalf("expected no duplicate PermissionRequest hook, got %v", got)
	}
}

// This runs on every OpenSession (see App.repairNeedsInputHooks), often
// while some other Codex session is live and reading this same global
// file — a redundant write is a needless window for it to observe a
// half-written hooks.json, so a no-op call must not touch the file.
func TestEnsureHooksSkipsNoopWrite(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureHooks(home); err != nil {
		t.Fatalf("first EnsureHooks: %v", err)
	}
	path := filepath.Join(home, ".codex", "hooks.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// mtime resolution can be coarse; back-date the file so a real rewrite
	// would be detectable even on filesystems with 1s granularity.
	backdated := before.ModTime().Add(-time.Hour)
	if err := os.Chtimes(path, backdated, backdated); err != nil {
		t.Fatal(err)
	}

	changed, err := EnsureHooks(home)
	if err != nil {
		t.Fatalf("second EnsureHooks: %v", err)
	}
	if changed {
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

func TestEnsureHooksPreservesExistingConfig(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Mirrors a real ~/.codex/hooks.json already carrying some other tool's
	// hook (this machine has one from moshi-hook) — EnsureHooks must not
	// clobber it.
	existing := `{
		"otherSetting": true,
		"hooks": {
			"SessionStart": [{"hooks": [{"type": "command", "command": "echo hi"}]}]
		}
	}`
	if err := os.WriteFile(filepath.Join(home, ".codex", "hooks.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureHooks(home); err != nil {
		t.Fatalf("EnsureHooks: %v", err)
	}
	settings := readHooks(t, home)
	if settings["otherSetting"] != true {
		t.Fatalf("expected unrelated setting to survive, got %v", settings["otherSetting"])
	}
	if got := hookCommands(t, settings, "SessionStart"); len(got) != 1 || got[0] != "echo hi" {
		t.Fatalf("expected existing SessionStart hook to survive, got %v", got)
	}
	if got := hookCommands(t, settings, "PermissionRequest"); len(got) != 1 || got[0] != "moomux hook codex set" {
		t.Fatalf("PermissionRequest hooks = %v", got)
	}
}

// TestEnsureHooksReportsChangedWhenANewEventIsAdded guards the scenario that
// motivated the changed return value in the first place: an older moomux
// build already installed a subset of these hooks, and a newer build adds
// one more. Users need to know to re-run `/hooks` for the newly added
// entry — which only happens if EnsureHooks correctly reports changed=true
// here, not the false a naive "was anything missing before I started" check
// would miss once the new event is added to the source.
func TestEnsureHooksReportsChangedWhenANewEventIsAdded(t *testing.T) {
	home := t.TempDir()
	// Simulate a file installed by a version of EnsureHooks that only knew
	// about two events, by writing exactly what it would have produced.
	partial := `{
		"hooks": {
			"PermissionRequest": [{"hooks": [{"type": "command", "command": "moomux hook codex set"}]}],
			"PreToolUse": [{"hooks": [{"type": "command", "command": "moomux hook codex clear"}]}]
		}
	}`
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "hooks.json"), []byte(partial), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := EnsureHooks(home)
	if err != nil {
		t.Fatalf("EnsureHooks: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true when UserPromptSubmit is newly added to an already-partially-installed file")
	}
	settings := readHooks(t, home)
	if got := hookCommands(t, settings, "UserPromptSubmit"); len(got) != 1 || got[0] != "moomux hook codex clear" {
		t.Fatalf("UserPromptSubmit hooks = %v", got)
	}
}

func TestEnsureHooksKeepsAnExistingFilesMode(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureHooks(home); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600 kept", fi.Mode().Perm())
	}
}
