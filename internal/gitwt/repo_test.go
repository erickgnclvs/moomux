package gitwt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failRunner fails calls whose joined args (without dir) appear in failOn.
type failRunner struct {
	fakeRunner
	failOn map[string]bool
}

func (f *failRunner) Run(dir string, args ...string) (string, error) {
	if f.failOn[strings.Join(args, " ")] {
		f.calls = append(f.calls, append([]string{"@" + dir}, args...))
		return "", errors.New("git failed")
	}
	return f.fakeRunner.Run(dir, args...)
}

func TestHasRemote(t *testing.T) {
	c := &Client{Runner: &fakeRunner{}}
	if !c.HasRemote("/repo", "origin") {
		t.Fatal("expected remote present")
	}
	c = &Client{Runner: &failRunner{failOn: map[string]bool{"remote get-url origin": true}}}
	if c.HasRemote("/repo", "origin") {
		t.Fatal("expected remote absent")
	}
}

func TestBranchExists(t *testing.T) {
	c := &Client{Runner: &fakeRunner{}}
	if !c.BranchExists("/repo", "feat") {
		t.Fatal("expected branch to exist")
	}
	c = &Client{Runner: &failRunner{failOn: map[string]bool{"rev-parse --verify --quiet refs/heads/feat": true}}}
	if c.BranchExists("/repo", "feat") {
		t.Fatal("expected branch to be absent")
	}
}

func TestAddWorktreeDeletesLeftoverBranch(t *testing.T) {
	// Branch exists (leftover from an orphaned worktree) and there's no
	// remote: it must be force-deleted before worktree add, and the start
	// point must be the local base branch.
	fr := &failRunner{failOn: map[string]bool{"remote get-url origin": true}}
	c := &Client{Runner: fr}
	if err := c.AddWorktree("/repo", "/wt/foo", "foo", "main"); err != nil {
		t.Fatal(err)
	}
	joined := make([]string, len(fr.calls))
	for i, call := range fr.calls {
		joined[i] = strings.Join(call, " ")
	}
	all := strings.Join(joined, "\n")
	if !strings.Contains(all, "@/repo branch -D foo") {
		t.Fatalf("leftover branch not deleted:\n%s", all)
	}
	if !strings.Contains(all, "@/repo worktree add /wt/foo -b foo main") {
		t.Fatalf("worktree add should start from local main:\n%s", all)
	}
}

func TestAddWorktreeBranchDeleteFails(t *testing.T) {
	fr := &failRunner{failOn: map[string]bool{"branch -D foo": true}}
	c := &Client{Runner: fr}
	if err := c.AddWorktree("/repo", "/wt/foo", "foo", "main"); err == nil {
		t.Fatal("expected error when leftover branch can't be deleted")
	}
}

func TestIsRepo(t *testing.T) {
	repo := t.TempDir()
	if err := Init(repo, ""); err != nil {
		t.Fatal(err)
	}
	if err := IsRepo(repo); err != nil {
		t.Fatalf("IsRepo(%s) = %v", repo, err)
	}
	if err := IsRepo(t.TempDir()); !errors.Is(err, ErrNotGitRepo) {
		t.Fatalf("plain dir: err = %v", err)
	}
	if err := IsRepo(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, ErrNotGitRepo) {
		t.Fatalf("missing dir: err = %v", err)
	}
}

func TestInitCreatesRepoWithInitialCommit(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "fresh")
	if err := Init(repo, "trunk"); err != nil {
		t.Fatal(err)
	}
	out, err := ExecRunner().Run(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "trunk" {
		t.Fatalf("branch = %q", got)
	}
	// the empty initial commit exists, so worktrees can branch off HEAD
	if _, err := ExecRunner().Run(repo, "rev-parse", "HEAD"); err != nil {
		t.Fatal(err)
	}
}

func TestExecRunnerError(t *testing.T) {
	out, err := ExecRunner().Run(t.TempDir(), "rev-parse", "HEAD")
	if err == nil {
		t.Fatalf("expected error, got %q", out)
	}
	if !strings.Contains(err.Error(), "rev-parse") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewUsesExecRunner(t *testing.T) {
	if New().Runner == nil {
		t.Fatal("nil runner")
	}
}

func TestRemoveWorktreeRealRepo(t *testing.T) {
	// End-to-end against real git: add a worktree, remove it, and verify
	// both git's bookkeeping and the directory are gone.
	repo := filepath.Join(t.TempDir(), "repo")
	if err := Init(repo, "main"); err != nil {
		t.Fatal(err)
	}
	c := New()
	wt := filepath.Join(t.TempDir(), "wt")
	if err := c.AddWorktree(repo, wt, "feat", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
	if !c.BranchExists(repo, "feat") {
		t.Fatal("branch not created")
	}
	if err := c.RemoveWorktree(repo, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still present: %v", err)
	}
	if err := c.DeleteBranch(repo, "feat"); err != nil {
		t.Fatal(err)
	}
	if c.BranchExists(repo, "feat") {
		t.Fatal("branch still present")
	}
}

func TestRemoveWorktreeCleansLeftoverDir(t *testing.T) {
	// git reports success (fake runner) but the directory is still on disk —
	// RemoveWorktree must delete it and prune.
	dir := filepath.Join(t.TempDir(), "leftover")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.RemoveWorktree("/repo", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("leftover dir still present: %v", err)
	}
	last := fr.calls[len(fr.calls)-1]
	if strings.Join(last, " ") != "@/repo worktree prune" {
		t.Fatalf("expected prune, calls = %v", fr.calls)
	}
}
