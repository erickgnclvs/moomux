//go:build e2e

// Package e2e exercises the real App wired to the real git and tmux
// binaries (no fake runners, no fake backend) — the integration surface the
// TUI sits on top of. It requires `git` and `tmux` on $PATH and creates real
// worktrees and tmux sessions under a temp dir, cleaning them up afterward.
//
// Run with: go test -tags e2e ./e2e/...
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/app"
	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tmux"
)

// noopOpener stands in for terminal.Detect() so tests never try to actually
// spawn/focus a terminal window, regardless of what terminal the test runs
// under locally or in CI.
type noopOpener struct{}

func (noopOpener) OpenSession(tmuxSession, title string) (string, error) { return "", nil }

// newTestApp builds an App with real Tmux/Git clients rooted at an isolated
// temp dir, and registers cleanup that kills any tmux sessions the test
// leaves behind (whether or not the test itself deleted them).
func newTestApp(t *testing.T) *app.App {
	t.Helper()
	dir := t.TempDir()
	a := &app.App{
		Cfg:          &config.Config{Projects: map[string]config.Project{}},
		CfgPath:      filepath.Join(dir, "config.toml"),
		Store:        &session.Store{Path: filepath.Join(dir, "sessions.json")},
		Tmux:         tmux.New(),
		Terminal:     noopOpener{},
		Git:          gitwt.New(),
		WorktreeRoot: filepath.Join(dir, "worktrees"),
	}
	if err := a.Store.Load(); err != nil {
		t.Fatalf("store load: %v", err)
	}
	t.Cleanup(func() {
		for _, s := range a.Store.All() {
			_ = a.Tmux.KillSession(s.TmuxSession)
		}
	})
	return a
}

// initRepo creates a fresh git repo with one empty commit on `base`.
func initRepo(t *testing.T, base string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := gitwt.Init(repo, base); err != nil {
		t.Fatalf("gitwt.Init: %v", err)
	}
	return repo
}

// addBranch creates a local branch at HEAD without checking it out.
func addBranch(t *testing.T, repo, name string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", repo, "branch", name).CombinedOutput(); err != nil {
		t.Fatalf("git branch %s: %v (%s)", name, err, out)
	}
}

func gitBranchExists(t *testing.T, repo, name string) bool {
	t.Helper()
	err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+name).Run()
	return err == nil
}

func gitWorktreeListContains(t *testing.T, repo, path string) bool {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v (%s)", err, out)
	}
	return strings.Contains(string(out), path)
}

func tmuxHasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

func tmuxPaneCount(t *testing.T, name string) int {
	t.Helper()
	out, err := exec.Command("tmux", "list-panes", "-t", name, "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("tmux list-panes: %v", err)
	}
	return len(strings.Fields(strings.TrimSpace(string(out))))
}

func tmuxPaneCwd(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("tmux", "list-panes", "-t", name, "-F", "#{pane_current_path}").Output()
	if err != nil {
		t.Fatalf("tmux list-panes: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return lines[0]
}

func TestCreateSession_GitProject_NewBranch(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)

	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main", BranchPrefix: "eg"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	s, hint, err := a.CreateSession("demo", "feature-x", "", "", "TICK-1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if hint != "" {
		t.Fatalf("hint = %q, want empty", hint)
	}
	if s.Branch != "eg/feature-x" {
		t.Fatalf("branch = %q", s.Branch)
	}
	if !s.NewBranch {
		t.Fatalf("expected NewBranch=true")
	}
	if s.TmuxSession != "moomux-feature-x" {
		t.Fatalf("tmux session = %q", s.TmuxSession)
	}
	if s.Agent != "claude" {
		t.Fatalf("agent = %q, want claude default", s.Agent)
	}
	if s.Ticket != "TICK-1" {
		t.Fatalf("ticket = %q", s.Ticket)
	}

	if _, err := os.Stat(s.WorktreePath); err != nil {
		t.Fatalf("worktree missing on disk: %v", err)
	}
	if !gitBranchExists(t, repo, "eg/feature-x") {
		t.Fatalf("branch eg/feature-x not created in repo")
	}
	if !gitWorktreeListContains(t, repo, s.WorktreePath) {
		t.Fatalf("git worktree list doesn't show %s", s.WorktreePath)
	}
	if !tmuxHasSession(s.TmuxSession) {
		t.Fatalf("tmux session %s not running", s.TmuxSession)
	}
	if got := tmuxPaneCount(t, s.TmuxSession); got != 2 {
		t.Fatalf("pane count = %d, want 2", got)
	}
	if got := tmuxPaneCwd(t, s.TmuxSession); got != s.WorktreePath {
		t.Fatalf("pane cwd = %q, want %q", got, s.WorktreePath)
	}

	// Persisted to disk, not just in memory.
	reloaded := &session.Store{Path: a.Store.Path}
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, ok := reloaded.Get(s.ID)
	if !ok || got.Branch != s.Branch {
		t.Fatalf("session not persisted correctly: %+v ok=%v", got, ok)
	}

	if !a.TmuxAliveAll()[s.ID] {
		t.Fatalf("TmuxAliveAll reports session dead")
	}
}

func TestCreateSession_ExistingBranch_DeleteKeepsBranch(t *testing.T) {
	repo := initRepo(t, "main")
	addBranch(t, repo, "feature-y")
	a := newTestApp(t)

	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main", BranchPrefix: "eg"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	s, _, err := a.CreateSession("demo", "", "", "feature-y", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if s.Name != "feature-y" {
		t.Fatalf("derived name = %q, want feature-y", s.Name)
	}
	// Existing-branch sessions use the branch as-is, ignoring BranchPrefix.
	if s.Branch != "feature-y" {
		t.Fatalf("branch = %q, want feature-y (no prefix)", s.Branch)
	}
	if s.NewBranch {
		t.Fatalf("expected NewBranch=false for an existing branch")
	}

	if err := a.DeleteSession(s.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if !gitBranchExists(t, repo, "feature-y") {
		t.Fatalf("existing branch feature-y should survive session delete")
	}
	if _, err := os.Stat(s.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still on disk: %v", err)
	}
	if tmuxHasSession(s.TmuxSession) {
		t.Fatalf("tmux session still running after delete")
	}
}

func TestCreateSession_NameCollisionAutoSuffix(t *testing.T) {
	repo := initRepo(t, "main")
	addBranch(t, repo, "feature/login")
	addBranch(t, repo, "hotfix/login")
	a := newTestApp(t)

	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	s1, _, err := a.CreateSession("demo", "", "", "feature/login", "")
	if err != nil {
		t.Fatalf("CreateSession 1: %v", err)
	}
	if s1.Name != "login" {
		t.Fatalf("s1 name = %q, want login", s1.Name)
	}

	s2, _, err := a.CreateSession("demo", "", "", "hotfix/login", "")
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}
	if s2.Name != "login-2" {
		t.Fatalf("s2 name = %q, want login-2", s2.Name)
	}
	if s2.WorktreePath == s1.WorktreePath {
		t.Fatalf("collided worktree paths")
	}
}

func TestCreateSession_PlainProject(t *testing.T) {
	plainDir := filepath.Join(t.TempDir(), "scratch")
	a := newTestApp(t)

	if err := a.AddPlainProject("scratch", config.Project{Repo: plainDir}); err != nil {
		t.Fatalf("AddPlainProject: %v", err)
	}

	s, _, err := a.CreateSession("scratch", "work", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if s.Branch != "" || s.NewBranch {
		t.Fatalf("plain project session should have no branch: %+v", s)
	}
	if s.WorktreePath != plainDir {
		t.Fatalf("worktree path = %q, want project repo %q", s.WorktreePath, plainDir)
	}
	if got := tmuxPaneCwd(t, s.TmuxSession); got != plainDir {
		t.Fatalf("pane cwd = %q, want %q", got, plainDir)
	}

	if err := a.DeleteSession(s.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := os.Stat(plainDir); err != nil {
		t.Fatalf("plain project dir should survive session delete: %v", err)
	}
	if tmuxHasSession(s.TmuxSession) {
		t.Fatalf("tmux session still running after delete")
	}
}

func TestCreateSession_Errors(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)
	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	if _, _, err := a.CreateSession("missing-project", "x", "", "", ""); err == nil {
		t.Fatalf("expected error for unknown project")
	}
	if _, _, err := a.CreateSession("demo", "", "", "", ""); err == nil {
		t.Fatalf("expected error when name and existingBranch are both empty")
	}
}

func TestCreateSession_OpenCodePortAllocation(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)
	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	s1, _, err := a.CreateSession("demo", "oc1", "opencode", "", "")
	if err != nil {
		t.Fatalf("CreateSession 1: %v", err)
	}
	if s1.AgentPort != 4096 {
		t.Fatalf("s1 port = %d, want 4096", s1.AgentPort)
	}

	s2, _, err := a.CreateSession("demo", "oc2", "opencode", "", "")
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}
	if s2.AgentPort != 4097 {
		t.Fatalf("s2 port = %d, want 4097", s2.AgentPort)
	}
}

func TestOpenSession_RecreatesKilledSession(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)
	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	s, _, err := a.CreateSession("demo", "feature", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if out, err := exec.Command("tmux", "kill-session", "-t", s.TmuxSession).CombinedOutput(); err != nil {
		t.Fatalf("kill-session: %v (%s)", err, out)
	}
	if tmuxHasSession(s.TmuxSession) {
		t.Fatalf("session should be dead after external kill")
	}

	if _, err := a.OpenSession(s.ID); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if !tmuxHasSession(s.TmuxSession) {
		t.Fatalf("OpenSession did not recreate the tmux session")
	}
	if got := tmuxPaneCwd(t, s.TmuxSession); got != s.WorktreePath {
		t.Fatalf("recreated pane cwd = %q, want %q", got, s.WorktreePath)
	}
}

func TestOpenSession_RecreatesOnCwdMismatch(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)
	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	s, _, err := a.CreateSession("demo", "feature", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if out, err := exec.Command("tmux", "kill-session", "-t", s.TmuxSession).CombinedOutput(); err != nil {
		t.Fatalf("kill-session: %v (%s)", err, out)
	}
	wrongCwd := t.TempDir()
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", s.TmuxSession, "-c", wrongCwd).CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v (%s)", err, out)
	}
	if got := tmuxPaneCwd(t, s.TmuxSession); got != wrongCwd {
		t.Fatalf("setup: pane cwd = %q, want %q", got, wrongCwd)
	}

	if _, err := a.OpenSession(s.ID); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if got := tmuxPaneCwd(t, s.TmuxSession); got != s.WorktreePath {
		t.Fatalf("pane cwd after mismatch recovery = %q, want %q", got, s.WorktreePath)
	}
}

func TestKillTmux_PreservesStoreAndWorktree(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)
	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	s, _, err := a.CreateSession("demo", "feature", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := a.KillTmux(s.ID); err != nil {
		t.Fatalf("KillTmux: %v", err)
	}
	if tmuxHasSession(s.TmuxSession) {
		t.Fatalf("tmux session should be dead")
	}
	if _, ok := a.Store.Get(s.ID); !ok {
		t.Fatalf("store entry should survive KillTmux")
	}
	if _, err := os.Stat(s.WorktreePath); err != nil {
		t.Fatalf("worktree should survive KillTmux: %v", err)
	}

	if _, err := a.OpenSession(s.ID); err != nil {
		t.Fatalf("OpenSession after KillTmux: %v", err)
	}
	if !tmuxHasSession(s.TmuxSession) {
		t.Fatalf("OpenSession should have recreated the tmux session")
	}
}

func TestTmuxAliveAll(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)
	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	s1, _, err := a.CreateSession("demo", "alive", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession 1: %v", err)
	}
	s2, _, err := a.CreateSession("demo", "dead", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}
	if out, err := exec.Command("tmux", "kill-session", "-t", s2.TmuxSession).CombinedOutput(); err != nil {
		t.Fatalf("kill-session: %v (%s)", err, out)
	}

	alive := a.TmuxAliveAll()
	if !alive[s1.ID] {
		t.Fatalf("s1 should be alive")
	}
	if alive[s2.ID] {
		t.Fatalf("s2 should be dead")
	}
}

func TestSessionTagsAndArchive(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)
	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	s, _, err := a.CreateSession("demo", "feature", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := a.SetSessionTags(s.ID, "TICK-9", "https://example.com/pr/1"); err != nil {
		t.Fatalf("SetSessionTags: %v", err)
	}
	got, _ := a.Store.Get(s.ID)
	if got.Ticket != "TICK-9" || got.PR != "https://example.com/pr/1" {
		t.Fatalf("tags not persisted: %+v", got)
	}

	if _, err := a.SetSessionArchived(s.ID, true); err != nil {
		t.Fatalf("SetSessionArchived: %v", err)
	}
	got, _ = a.Store.Get(s.ID)
	if !got.Archived {
		t.Fatalf("expected archived")
	}

	reloaded := &session.Store{Path: a.Store.Path}
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := reloaded.Get(s.ID)
	if !ok || !got.Archived || got.Ticket != "TICK-9" {
		t.Fatalf("archive/tags not persisted across reload: %+v ok=%v", got, ok)
	}
}

func TestMoveSessionAndMoveProject(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)
	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject demo: %v", err)
	}
	if err := a.AddPlainProject("other", config.Project{Repo: filepath.Join(t.TempDir(), "other")}); err != nil {
		t.Fatalf("AddPlainProject other: %v", err)
	}

	sA, _, err := a.CreateSession("demo", "a", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession a: %v", err)
	}
	sB, _, err := a.CreateSession("demo", "b", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession b: %v", err)
	}

	if err := a.MoveSession(sB.ID, -1); err != nil {
		t.Fatalf("MoveSession: %v", err)
	}
	peers := a.Store.ByProject("demo")
	if len(peers) != 2 || peers[0].ID != sB.ID || peers[1].ID != sA.ID {
		t.Fatalf("unexpected order after move: %+v", peers)
	}
	// Moving the first entry further left is a no-op, not an error.
	if err := a.MoveSession(peers[0].ID, -1); err != nil {
		t.Fatalf("MoveSession out-of-bounds: %v", err)
	}
	if err := a.MoveSession("nonexistent", -1); err == nil {
		t.Fatalf("expected error for unknown session id")
	}

	order := a.Projects()
	if len(order) != 2 {
		t.Fatalf("expected 2 projects, got %+v", order)
	}
	first := order[0]
	if err := a.MoveProject(first, 1); err != nil {
		t.Fatalf("MoveProject: %v", err)
	}
	if a.Projects()[1] != first {
		t.Fatalf("project order unchanged after move: %+v", a.Projects())
	}
	if err := a.MoveProject("nonexistent", 1); err == nil {
		t.Fatalf("expected error for unknown project")
	}
}

func TestDeleteSession_GitProject_RemovesWorktreeAndBranch(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)
	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	s, _, err := a.CreateSession("demo", "feature", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := a.DeleteSession(s.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if tmuxHasSession(s.TmuxSession) {
		t.Fatalf("tmux session still running")
	}
	if _, err := os.Stat(s.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still on disk: %v", err)
	}
	if gitWorktreeListContains(t, repo, s.WorktreePath) {
		t.Fatalf("git still lists the removed worktree")
	}
	if gitBranchExists(t, repo, s.Branch) {
		t.Fatalf("branch %s should have been deleted (NewBranch=true)", s.Branch)
	}
	if _, ok := a.Store.Get(s.ID); ok {
		t.Fatalf("store entry should be gone")
	}
	if err := a.DeleteSession(s.ID); err == nil {
		t.Fatalf("expected error deleting an already-deleted session")
	}
}

func TestRemoveProject_ActiveSessionsBlocked(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)
	if err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	s, _, err := a.CreateSession("demo", "feature", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := a.RemoveProject("demo"); err == nil {
		t.Fatalf("expected RemoveProject to fail with an active session")
	}
	if err := a.DeleteSession(s.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if err := a.RemoveProject("demo"); err != nil {
		t.Fatalf("RemoveProject after cleanup: %v", err)
	}
	if err := a.RemoveProject("demo"); err == nil {
		t.Fatalf("expected error removing an already-removed project")
	}
}

func TestProjectLifecycle_AddInitPlainAndValidationErrors(t *testing.T) {
	a := newTestApp(t)

	existing := initRepo(t, "main")
	if err := a.AddProject("existing", config.Project{Repo: existing, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	freshDir := filepath.Join(t.TempDir(), "fresh")
	if err := a.InitProjectAndAdd("fresh", config.Project{Repo: freshDir, BaseBranch: "trunk"}); err != nil {
		t.Fatalf("InitProjectAndAdd: %v", err)
	}
	if err := gitwt.IsRepo(freshDir); err != nil {
		t.Fatalf("InitProjectAndAdd did not create a real repo: %v", err)
	}

	plainDir := filepath.Join(t.TempDir(), "plain")
	if err := a.AddPlainProject("plain", config.Project{Repo: plainDir}); err != nil {
		t.Fatalf("AddPlainProject: %v", err)
	}
	if _, err := os.Stat(plainDir); err != nil {
		t.Fatalf("AddPlainProject did not create the dir: %v", err)
	}

	cases := []struct {
		name string
		proj config.Project
	}{
		{"", config.Project{Repo: existing}},
		{"has space", config.Project{Repo: existing}},
		{"has/slash", config.Project{Repo: existing}},
		{"existing", config.Project{Repo: existing}}, // duplicate name
		{"norepo", config.Project{}},                  // missing repo
		{"notarepo", config.Project{Repo: t.TempDir()}},
	}
	for _, tc := range cases {
		if err := a.AddProject(tc.name, tc.proj); err == nil {
			t.Fatalf("AddProject(%q, %+v): expected error", tc.name, tc.proj)
		}
	}
}

// TestFullLifecycle_EndToEnd exercises a broader combined scenario across
// two projects with mixed agents, mirroring how a real user session would
// progress: create, tag, archive, reopen, and tear everything down.
func TestFullLifecycle_EndToEnd(t *testing.T) {
	repo := initRepo(t, "main")
	a := newTestApp(t)

	if err := a.AddProject("backend", config.Project{Repo: repo, BaseBranch: "main", BranchPrefix: "eg"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	plainDir := filepath.Join(t.TempDir(), "scripts")
	if err := a.AddPlainProject("scripts", config.Project{Repo: plainDir}); err != nil {
		t.Fatalf("AddPlainProject: %v", err)
	}

	claudeS, _, err := a.CreateSession("backend", "auth", "claude", "", "TICK-1")
	if err != nil {
		t.Fatalf("CreateSession claude: %v", err)
	}
	codexS, _, err := a.CreateSession("backend", "billing", "codex", "", "")
	if err != nil {
		t.Fatalf("CreateSession codex: %v", err)
	}
	plainS, _, err := a.CreateSession("scripts", "cleanup", "", "", "")
	if err != nil {
		t.Fatalf("CreateSession plain: %v", err)
	}

	for _, s := range []session.Session{claudeS, codexS, plainS} {
		if !tmuxHasSession(s.TmuxSession) {
			t.Fatalf("session %s not running", s.TmuxSession)
		}
	}
	if len(a.Sessions()) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(a.Sessions()))
	}

	if _, err := a.SetSessionTags(claudeS.ID, "TICK-1", "https://example.com/pr/7"); err != nil {
		t.Fatalf("SetSessionTags: %v", err)
	}
	if _, err := a.SetSessionArchived(codexS.ID, true); err != nil {
		t.Fatalf("SetSessionArchived: %v", err)
	}

	if err := a.KillTmux(plainS.ID); err != nil {
		t.Fatalf("KillTmux: %v", err)
	}
	if _, err := a.OpenSession(plainS.ID); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if !tmuxHasSession(plainS.TmuxSession) {
		t.Fatalf("plain session not reopened")
	}

	for _, s := range []session.Session{claudeS, codexS, plainS} {
		if err := a.DeleteSession(s.ID); err != nil {
			t.Fatalf("DeleteSession(%s): %v", s.ID, err)
		}
	}
	if len(a.Sessions()) != 0 {
		t.Fatalf("expected 0 sessions after cleanup, got %d", len(a.Sessions()))
	}
	if err := a.RemoveProject("backend"); err != nil {
		t.Fatalf("RemoveProject backend: %v", err)
	}
	if err := a.RemoveProject("scripts"); err != nil {
		t.Fatalf("RemoveProject scripts: %v", err)
	}
}
