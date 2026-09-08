package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := ExpandHome("~/repo"); got != filepath.Join(home, "repo") {
		t.Fatalf("got %q", got)
	}
	if got := ExpandHome("/abs/path"); got != "/abs/path" {
		t.Fatalf("got %q", got)
	}
	if got := ExpandHome("~"); got != home {
		t.Fatalf("got %q", got)
	}
	// "~foo" names another user's home in shell syntax; it must not be
	// resolved against the current user's home.
	if got := ExpandHome("~foo/repo"); got != "~foo/repo" {
		t.Fatalf("got %q", got)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[projects.eg_system]
repo          = "~/Development/eg_system"
branch_prefix = "erickgoncalves"
base_branch   = "main"

[projects.other]
repo        = "~/Development/other"
base_branch = "main"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(cfg.Projects))
	}
	p := cfg.Projects["eg_system"]
	if p.BranchPrefix != "erickgoncalves" {
		t.Fatalf("BranchPrefix = %q", p.BranchPrefix)
	}
	if p.BaseBranch != "main" {
		t.Fatalf("BaseBranch = %q", p.BaseBranch)
	}
}

func TestLoadExpandsHome(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[projects.x]
repo        = "~/foo"
base_branch = "main"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Projects["x"].Repo; got == "~/foo" {
		t.Fatalf("expected ~ expanded, got %q", got)
	}
}

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.toml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if cfg == nil || cfg.Projects == nil {
		t.Fatalf("expected non-nil config with empty projects")
	}
	if len(cfg.Projects) != 0 {
		t.Fatalf("expected empty projects, got %d", len(cfg.Projects))
	}
}

func TestSaveRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.toml")
	cfg := &Config{Projects: map[string]Project{
		"a": {Repo: "/tmp/a", BaseBranch: "main"},
	}}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Projects["a"].Repo != "/tmp/a" {
		t.Fatalf("repo = %q", got.Projects["a"].Repo)
	}
}

func TestSaveFailureKeepsExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.toml")
	cfg := &Config{Projects: map[string]Project{
		"a": {Repo: "/tmp/a", BaseBranch: "main"},
	}}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	// A write that can't complete (read-only dir) must leave the previous
	// config intact rather than truncating it.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	cfg.Projects["b"] = Project{Repo: "/tmp/b"}
	if err := Save(path, cfg); err == nil {
		t.Fatal("expected save to fail in read-only dir")
	}

	_ = os.Chmod(dir, 0o755)
	got, err := Load(path)
	if err != nil {
		t.Fatalf("previous config was corrupted: %v", err)
	}
	if got.Projects["a"].Repo != "/tmp/a" {
		t.Fatalf("previous config lost: %+v", got.Projects)
	}
}

// TestConcurrentSavesDoNotRaceOnTempFile mirrors the session store's test:
// multiple moomux processes can Save the same config.toml around the same
// time. A shared fixed ".tmp" name lets one process's rename steal or
// delete another's in-flight temp file; a per-invocation temp file must not.
func TestConcurrentSavesDoNotRaceOnTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	const writers = 8
	const rounds = 20
	var wg sync.WaitGroup
	errCh := make(chan error, writers*rounds)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				name := fmt.Sprintf("p-w%d-r%d", w, r)
				cfg := &Config{Projects: map[string]Project{name: {Repo: "/tmp/" + name}}}
				if err := Save(path, cfg); err != nil {
					errCh <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Save failed: %v", err)
	}
}

func TestReloadRefreshesInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	cfg := &Config{Projects: map[string]Project{"a": {Repo: "/tmp/a", BaseBranch: "main"}}}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	// A second writer (another moomux process sharing this config.toml)
	// adds a project after cfg was loaded.
	other, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Projects["b"] = Project{Repo: "/tmp/b", BaseBranch: "main"}
	if err := Save(path, other); err != nil {
		t.Fatal(err)
	}

	before := cfg // same pointer must be reused, not swapped
	if err := Reload(path, cfg); err != nil {
		t.Fatal(err)
	}
	if cfg != before {
		t.Fatal("Reload must refresh the same *Config, not return a different one")
	}
	if _, ok := cfg.Projects["a"]; !ok {
		t.Fatal("original project a lost after reload")
	}
	if _, ok := cfg.Projects["b"]; !ok {
		t.Fatal("concurrently-added project b not picked up by reload")
	}
}

func TestProjectAgentNameDefaultsToClaude(t *testing.T) {
	p := Project{}
	if got := p.AgentName(); got != "claude" {
		t.Fatalf("expected claude, got %q", got)
	}
}

func TestProjectAgentNameReturnsSetValue(t *testing.T) {
	tests := []string{"codex", "opencode"}
	for _, agent := range tests {
		p := Project{Agent: agent}
		if got := p.AgentName(); got != agent {
			t.Fatalf("expected %q, got %q", agent, got)
		}
	}
}

func TestOrderedProjectNamesUsesOrderThenAlphabetical(t *testing.T) {
	cfg := &Config{
		Projects: map[string]Project{
			"a": {}, "b": {}, "c": {},
		},
		Order: []string{"c", "a"},
	}
	got := cfg.OrderedProjectNames()
	want := []string{"c", "a", "b"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestOrderedProjectNamesDropsStaleEntries(t *testing.T) {
	cfg := &Config{
		Projects: map[string]Project{"a": {}},
		Order:    []string{"removed", "a"},
	}
	got := cfg.OrderedProjectNames()
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %v, want [a]", got)
	}
}

func TestProjectAgentRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.toml")
	cfg := &Config{Projects: map[string]Project{
		"codex_proj": {Repo: "/tmp/codex", Agent: "codex", BaseBranch: "main"},
	}}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Projects["codex_proj"].Agent != "codex" {
		t.Fatalf("Agent = %q", got.Projects["codex_proj"].Agent)
	}
}

func TestCloneDeepCopiesProjectFolders(t *testing.T) {
	cfg := Config{Projects: map[string]Project{
		"demo": {Repo: "/repo", Folders: map[string]FolderMeta{"auth": {}}},
	}}

	clone := cfg.Clone()
	clone.Projects["demo"].Folders["auth"] = FolderMeta{Collapsed: true}

	// A shallow Clone leaves every copy sharing one folder map, so App's
	// live config and each front end's snapshot would write over each other
	// (and race, since the TUI reads its own copy unlocked).
	if cfg.Projects["demo"].Folders["auth"].Collapsed {
		t.Fatal("writing to the clone's folder map mutated the original — Folders is aliased, not cloned")
	}
}
