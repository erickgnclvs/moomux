package claudehook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readClaudeJSON(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read .claude.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal .claude.json: %v", err)
	}
	return m
}

// entryFor reads root.projects[dir] — the nesting Claude Code's own trust
// checks actually read (root[dir] directly is a location Claude Code never
// consults, however plausible it looks).
func entryFor(t *testing.T, root map[string]any, dir string) map[string]any {
	t.Helper()
	projects, ok := root["projects"].(map[string]any)
	if !ok {
		t.Fatalf("no projects object: %v", root)
	}
	entry, ok := projects[dir].(map[string]any)
	if !ok {
		t.Fatalf("no projects entry for %q: %v", dir, projects)
	}
	return entry
}

func TestTrustDirectoryCreatesEntry(t *testing.T) {
	home := t.TempDir()
	dir := "/repo/worktrees/feature-x"

	if err := TrustDirectory(home, dir); err != nil {
		t.Fatal(err)
	}

	entry := entryFor(t, readClaudeJSON(t, home), dir)
	if entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("hasTrustDialogAccepted = %v, want true", entry["hasTrustDialogAccepted"])
	}
	if entry["hasCompletedProjectOnboarding"] != true {
		t.Fatalf("hasCompletedProjectOnboarding = %v, want true", entry["hasCompletedProjectOnboarding"])
	}
	// A separate dialog from the folder-trust one above — gates on a CLAUDE.md
	// that imports files from outside the project (e.g. a global CLAUDE.md
	// importing from $HOME/.claude), and blocks first input exactly the same
	// way if left unset.
	if entry["hasClaudeMdExternalIncludesApproved"] != true {
		t.Fatalf("hasClaudeMdExternalIncludesApproved = %v, want true", entry["hasClaudeMdExternalIncludesApproved"])
	}
}

func TestTrustDirectoryPreservesOtherProjectsAndFields(t *testing.T) {
	home := t.TempDir()
	existing := `{
		"projects": {
			"/other/project": {"hasTrustDialogAccepted": false, "allowedTools": ["Bash"]},
			"/repo/worktrees/feature-x": {"hasTrustDialogAccepted": false, "projectOnboardingSeenCount": 3}
		}
	}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := TrustDirectory(home, "/repo/worktrees/feature-x"); err != nil {
		t.Fatal(err)
	}

	root := readClaudeJSON(t, home)
	other := entryFor(t, root, "/other/project")
	if other["hasTrustDialogAccepted"] != false {
		t.Fatalf("unrelated project entry was touched: %v", other)
	}
	entry := entryFor(t, root, "/repo/worktrees/feature-x")
	if entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("hasTrustDialogAccepted = %v, want true", entry["hasTrustDialogAccepted"])
	}
	if entry["projectOnboardingSeenCount"] != float64(3) {
		t.Fatalf("existing field projectOnboardingSeenCount was dropped: %v", entry)
	}
}

// TestTrustDirectoryNoRewriteWhenAlreadyTrusted pins the guard that keeps
// OpenSession off ~/.claude.json's read-modify-write path: a live Claude Code
// process rewrites that file constantly, so a redundant write here would
// clobber whatever it wrote since we read it.
func TestTrustDirectoryNoRewriteWhenAlreadyTrusted(t *testing.T) {
	home := t.TempDir()
	dir := "/repo/worktrees/feature-x"
	if err := TrustDirectory(home, dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Something else (Claude itself) writes the file between opens.
	const live = `{"projects":{"/repo/worktrees/feature-x":{"hasTrustDialogAccepted":true,"hasCompletedProjectOnboarding":true,"hasClaudeMdExternalIncludesApproved":true,"history":["a live edit"]}}}`
	if err := os.WriteFile(path, []byte(live), before.Mode().Perm()); err != nil {
		t.Fatal(err)
	}

	if err := TrustDirectory(home, dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != live {
		t.Fatalf("re-trust rewrote the file, losing the concurrent write:\n got %s\nwant %s", got, live)
	}
}
