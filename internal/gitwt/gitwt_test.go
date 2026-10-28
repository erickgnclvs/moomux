package gitwt

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls [][]string
	out   string
	// errAt, keyed by call index (0-based), makes that call fail instead of
	// returning out — used to simulate e.g. "no upstream configured".
	errAt map[int]error
	// failPrefix, when set, makes any call whose args start with it fail
	// instead of returning out — used when the failing call's index isn't
	// worth pinning down (e.g. "any 'worktree lock' call").
	failPrefix []string
}

// failCommand makes any call whose args start with prefix fail.
func (f *fakeRunner) failCommand(prefix ...string) {
	f.failPrefix = prefix
}

func (f *fakeRunner) Run(dir string, args ...string) (string, error) {
	c := append([]string{"@" + dir}, args...)
	idx := len(f.calls)
	f.calls = append(f.calls, c)
	if err, ok := f.errAt[idx]; ok {
		return "", err
	}
	if len(f.failPrefix) > 0 && len(args) >= len(f.failPrefix) && reflect.DeepEqual(args[:len(f.failPrefix)], f.failPrefix) {
		return "", fmt.Errorf("git %v: fake failure", args)
	}
	return f.out, nil
}

func TestFetch(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.Fetch("/repo", "main"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "fetch", "origin", "main"}
	if !reflect.DeepEqual(fr.calls[0], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestAddWorktree(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.AddWorktree("/repo", "/wt/foo", "user/foo", "main"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "worktree", "add", "/wt/foo", "-b", "user/foo", "origin/main"}
	if !reflect.DeepEqual(fr.calls[len(fr.calls)-2], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
	wantLock := []string{"@/repo", "worktree", "lock", "/wt/foo", "--reason", "moomux"}
	if !reflect.DeepEqual(fr.calls[len(fr.calls)-1], wantLock) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

// A base branch that no longer exists upstream (a finished release branch,
// say) fails before anything is created, naming the base branch rather than
// git's "invalid reference: origin/<base>".
func TestAddWorktreeMissingBaseBranch(t *testing.T) {
	fr := &fakeRunner{}
	fr.failCommand("rev-parse", "--verify", "--quiet", "origin/release/202632^{commit}")
	c := &Client{Runner: fr}
	err := c.AddWorktree("/repo", "/wt/foo", "user/foo", "release/202632")
	if err == nil {
		t.Fatal("want an error for a missing base branch")
	}
	if !strings.Contains(err.Error(), `base branch "release/202632" not found`) {
		t.Fatalf("err = %v", err)
	}
	for _, call := range fr.calls {
		if len(call) > 2 && call[1] == "worktree" && call[2] == "add" {
			t.Fatalf("worktree created anyway: %v", fr.calls)
		}
	}
}

func TestAddWorktreeExisting(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.AddWorktreeExisting("/repo", "/wt/foo", "user/foo"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "worktree", "add", "/wt/foo", "user/foo"}
	if !reflect.DeepEqual(fr.calls[len(fr.calls)-2], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
	wantLock := []string{"@/repo", "worktree", "lock", "/wt/foo", "--reason", "moomux"}
	if !reflect.DeepEqual(fr.calls[len(fr.calls)-1], wantLock) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

// TestAddWorktreeLockFailureCleansUpOrphan covers `worktree lock` failing
// right after `worktree add` succeeds — without cleanup, that leaves an
// untracked checkout+branch on disk that no session references, since the
// caller (CreateSession) bails out on the error before ever registering it.
func TestAddWorktreeLockFailureCleansUpOrphan(t *testing.T) {
	fr := &fakeRunner{}
	fr.failCommand("worktree", "lock")
	c := &Client{Runner: fr}
	if err := c.AddWorktree("/repo", "/wt/foo", "user/foo", "main"); err == nil {
		t.Fatal("expected the lock error to surface")
	}
	wantRemove := "@/repo worktree remove /wt/foo --force --force"
	found := false
	for _, call := range fr.calls {
		if strings.Join(call, " ") == wantRemove {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected cleanup removal after failed lock, calls = %v", fr.calls)
	}
}

func TestRemoveWorktree(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.RemoveWorktree("/repo", "/wt/foo"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "worktree", "remove", "/wt/foo", "--force", "--force"}
	if !reflect.DeepEqual(fr.calls[0], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

// TestRemoveWorktreeLocked reproduces the real bug against real git: a
// single --force removes a dirty worktree but git still refuses a locked
// one ("use 'remove -f -f' to override or unlock first"). Locking worktrees
// on creation (AddWorktree/AddWorktreeExisting) means every removal must
// clear a lock moomux itself may have set, not just one from another tool.
func TestRemoveWorktreeLocked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repoDir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	wtPath := filepath.Join(t.TempDir(), "wt")
	run("worktree", "add", "-q", wtPath, "-b", "feat")
	run("worktree", "lock", wtPath, "--reason", "test lock")

	c := &Client{Runner: ExecRunner()}
	if err := c.RemoveWorktree(repoDir, wtPath); err != nil {
		t.Fatalf("RemoveWorktree on a locked worktree: %v", err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("expected worktree directory to be gone, stat err = %v", err)
	}
}

// TestRemoveWorktreeStatPermissionErrorSurfaces simulates os.Stat failing
// for a reason other than "gone" (e.g. permission denied on a parent dir).
// RemoveWorktree must not treat that as "already removed" and report
// success — it doesn't actually know whether the worktree is there.
func TestRemoveWorktreeStatPermissionErrorSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(locked, "worktree")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	c := &Client{Runner: &erringRunner{}}
	if err := c.RemoveWorktree("/repo", target); err == nil {
		t.Fatal("expected an error, not silent success, for an unresolvable stat")
	}
}

// erringRunner fails every git call, simulating e.g. "worktree remove"
// refusing to touch a main working tree.
type erringRunner struct{ calls int }

func (f *erringRunner) Run(dir string, args ...string) (string, error) {
	f.calls++
	return "", errors.New("git refused")
}

func TestRemoveWorktreeGitFailureKeepsDirectory(t *testing.T) {
	dir := t.TempDir() // stands in for a real repo the user still needs
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Client{Runner: &erringRunner{}}
	if err := c.RemoveWorktree("/repo", dir); err == nil {
		t.Fatal("expected the git error to surface")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("directory was deleted despite git failure: %v", err)
	}
}

// TestRemoveWorktreeMissingGitSelfCleans covers a worktree directory that
// was never linked to git at all (e.g. worktree creation died before `git
// worktree add` ran, or something manually deleted the .git file). git
// itself has no registration to lose track of, so RemoveWorktree must clean
// it up rather than surface git's "not a working tree" error forever.
func TestRemoveWorktreeMissingGitSelfCleans(t *testing.T) {
	dir := t.TempDir()
	c := &Client{Runner: &erringRunner{}}
	if err := c.RemoveWorktree("/repo", dir); err != nil {
		t.Fatalf("expected self-cleanup, got error: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected directory to be removed, stat err = %v", err)
	}
}

// TestExecRunnerRespectsRunTimeout replaces "git" on PATH with a fake that
// sleeps far longer than runTimeout. Without exec.CommandContext bounding
// the subprocess, execRunner.Run would block for the full sleep instead of
// giving up once the timeout elapses (e.g. a fetch against a dead remote
// hanging the whole app).
func TestExecRunnerRespectsRunTimeout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	fakeDir := t.TempDir()
	fake := filepath.Join(fakeDir, "git")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	old := runTimeout
	runTimeout = 50 * time.Millisecond
	t.Cleanup(func() { runTimeout = old })

	start := time.Now()
	if _, err := (execRunner{}).Run(t.TempDir(), "status"); err == nil {
		t.Fatal("expected error once runTimeout elapsed")
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("Run took %v, want to return shortly after runTimeout", elapsed)
	}
}

// git must never prompt on the terminal moomux's TUI is drawn on — see
// noPrompt.
func TestGitRunsWithTerminalPromptDisabled(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	fakeDir := t.TempDir()
	fake := filepath.Join(fakeDir, "git")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf %s \"$GIT_TERMINAL_PROMPT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_TERMINAL_PROMPT", "1")

	if out, err := (execRunner{}).Run(t.TempDir(), "fetch"); err != nil || out != "0" {
		t.Fatalf("execRunner: GIT_TERMINAL_PROMPT=%q (err %v), want 0", out, err)
	}
	if out, _, err := gitStdout(t.TempDir(), nil, 0, "fetch"); err != nil || out != "0" {
		t.Fatalf("gitStdout: GIT_TERMINAL_PROMPT=%q (err %v), want 0", out, err)
	}
}

func TestWorktreeForBranch(t *testing.T) {
	fr := &fakeRunner{
		out: "worktree /wt/foo\nbranch refs/heads/user/foo\n\nworktree /wt/bar\nbranch refs/heads/user/bar\n",
	}
	c := &Client{Runner: fr}
	got, err := c.WorktreeForBranch("/repo", "user/bar")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/wt/bar" {
		t.Fatalf("got %q, want /wt/bar", got)
	}
	got, err = c.WorktreeForBranch("/repo", "user/missing")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestIsWorktreeClean(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	clean, err := c.IsWorktreeClean("/wt/foo")
	if err != nil || !clean {
		t.Fatalf("clean=%v err=%v, want clean", clean, err)
	}

	fr.out = " M some/file.go\n"
	clean, err = c.IsWorktreeClean("/wt/foo")
	if err != nil || clean {
		t.Fatalf("clean=%v err=%v, want dirty", clean, err)
	}
}

func TestHasUnpushedCommits(t *testing.T) {
	fr := &fakeRunner{out: "0\n"}
	c := &Client{Runner: fr}
	unpushed, err := c.HasUnpushedCommits("/wt/foo")
	if err != nil || unpushed {
		t.Fatalf("unpushed=%v err=%v, want false (up to date with upstream)", unpushed, err)
	}

	fr = &fakeRunner{out: "2\n"}
	c = &Client{Runner: fr}
	unpushed, err = c.HasUnpushedCommits("/wt/foo")
	if err != nil || !unpushed {
		t.Fatalf("unpushed=%v err=%v, want true (ahead of upstream)", unpushed, err)
	}

	// call 0: @{u} fails (no upstream). call 1: rev-parse HEAD succeeds.
	// call 2: config lookup also fails, meaning upstream was never configured.
	fr = &fakeRunner{errAt: map[int]error{
		0: errors.New("no upstream configured"),
		2: errors.New("key not found"),
	}}
	c = &Client{Runner: fr}
	unpushed, err = c.HasUnpushedCommits("/wt/foo")
	if err != nil || !unpushed {
		t.Fatalf("unpushed=%v err=%v, want true (no upstream)", unpushed, err)
	}

	// call 0: @{u} fails, but call 2's config lookup succeeds — the upstream
	// was configured and then pruned (e.g. remote branch deleted post-merge),
	// which must not read the same as "never pushed".
	fr = &fakeRunner{errAt: map[int]error{0: errors.New("upstream gone")}}
	c = &Client{Runner: fr}
	unpushed, err = c.HasUnpushedCommits("/wt/foo")
	if err != nil || unpushed {
		t.Fatalf("unpushed=%v err=%v, want false (upstream gone, nothing left to compare)", unpushed, err)
	}
}

func TestFilesChangedCount(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	n, err := c.FilesChangedCount("/wt/foo")
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v, want 0 (clean)", n, err)
	}

	fr.out = " M some/file.go\n?? other/file.go\n"
	n, err = c.FilesChangedCount("/wt/foo")
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v, want 2", n, err)
	}
}

func TestUnpushedCommitCount(t *testing.T) {
	fr := &fakeRunner{out: "3\n"}
	c := &Client{Runner: fr}
	n, err := c.UnpushedCommitCount("/wt/foo")
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v, want 3 (ahead of upstream)", n, err)
	}

	// call 0: @{u} fails (no upstream). call 1: rev-parse HEAD succeeds.
	// call 2: config lookup also fails (never configured), so call 3 counts
	// every commit on the branch instead.
	fr = &fakeRunner{out: "5\n", errAt: map[int]error{
		0: errors.New("no upstream configured"),
		2: errors.New("key not found"),
	}}
	c = &Client{Runner: fr}
	n, err = c.UnpushedCommitCount("/wt/foo")
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v, want 5 (total commits, never pushed)", n, err)
	}

	// upstream configured then pruned — nothing left to compare against.
	fr = &fakeRunner{errAt: map[int]error{0: errors.New("upstream gone")}}
	c = &Client{Runner: fr}
	n, err = c.UnpushedCommitCount("/wt/foo")
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v, want 0 (upstream gone)", n, err)
	}
}

func TestDeleteBranch(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.DeleteBranch("/repo", "user/foo"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "branch", "-D", "user/foo"}
	if !reflect.DeepEqual(fr.calls[0], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}
