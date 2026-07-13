package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
