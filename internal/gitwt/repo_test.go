package gitwt

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	// remote: it must be safe-deleted (-d, never -D — it could be the
	// user's own branch with unpushed commits) before worktree add, and
	// the start point must be the local base branch.
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
	if !strings.Contains(all, "@/repo branch -d foo") {
		t.Fatalf("leftover branch not deleted:\n%s", all)
	}
	if strings.Contains(all, "branch -D") {
		t.Fatalf("force-deleted a pre-existing branch:\n%s", all)
	}
	if !strings.Contains(all, "@/repo worktree add /wt/foo -b foo main") {
		t.Fatalf("worktree add should start from local main:\n%s", all)
	}
}

func TestAddWorktreeUnmergedBranchBlocks(t *testing.T) {
	// An unmerged (or checked-out) same-named branch refuses -d; creation
	// must fail with a pointer to the fix, without running worktree add.
	fr := &failRunner{failOn: map[string]bool{"branch -d foo": true}}
	c := &Client{Runner: fr}
	err := c.AddWorktree("/repo", "/wt/foo", "foo", "main")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
	for _, call := range fr.calls {
		if strings.Contains(strings.Join(call, " "), "worktree add") {
			t.Fatalf("worktree add must not run after refused delete: %v", fr.calls)
		}
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

func TestRemoveWorktreeOrphanedCheckout(t *testing.T) {
	// git worktree remove fails because the worktree's gitdir registration
	// under the main repo is already gone (e.g. someone ran `git worktree
	// prune` directly), but the checkout directory itself is still on disk.
	// RemoveWorktree must recognize the orphaned .git pointer and finish the
	// cleanup itself instead of leaving it stuck forever.
	repo := filepath.Join(t.TempDir(), "repo")
	if err := Init(repo, "main"); err != nil {
		t.Fatal(err)
	}
	c := New()
	wt := filepath.Join(t.TempDir(), "wt")
	if err := c.AddWorktree(repo, wt, "feat", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repo, ".git", "worktrees", "wt")); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveWorktree(repo, wt); err != nil {
		t.Fatalf("expected orphaned checkout to be cleaned up, got: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("orphaned worktree dir still present: %v", err)
	}
}

func TestRemoveWorktreeRefusesRealRepo(t *testing.T) {
	// git refuses to remove it (fake runner) and the directory is a real
	// repo, not an orphaned worktree checkout — RemoveWorktree must not
	// delete it, or a stale session entry could wipe a real repository.
	dir := filepath.Join(t.TempDir(), "realrepo")
	if err := Init(dir, "main"); err != nil {
		t.Fatal(err)
	}
	fr := &failRunner{failOn: map[string]bool{"worktree remove " + dir + " --force --force": true}}
	c := &Client{Runner: fr}
	if err := c.RemoveWorktree("/repo", dir); err == nil {
		t.Fatal("expected error, real repo should not be deleted")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("real repo directory was deleted: %v", err)
	}
}

// diffRepo is a linked worktree on branch feat, cut from main — linked,
// so its index lives under .git/worktrees/ the way a session's does —
// holding one of each thing Diff has to show: a committed rename, an
// uncommitted edit, a rename never staged, an untracked file with a
// non-ASCII name, and an ignored file and a nested repo it must not.
func diffRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repo, dir := filepath.Join(root, "repo"), filepath.Join(root, "wt")
	git := func(in string, args ...string) {
		t.Helper()
		args = append([]string{"-c", "user.name=moomux", "-c", "user.email=moomux@localhost"}, args...)
		if out, err := ExecRunner().Run(in, args...); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lines := strings.Repeat("a line long enough to be recognised as the same file\n", 20)
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(repo, "init", "-b", "main")
	for name, body := range map[string]string{"edited.txt": "one\n", "old.txt": lines, "moved.txt": "moved " + lines, ".gitignore": "*.log\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(repo, "add", ".")
	git(repo, "commit", "-m", "init")
	git(repo, "worktree", "add", "-b", "feat", dir)
	git(dir, "mv", "old.txt", "new.txt")
	git(dir, "commit", "-m", "rename")
	write("edited.txt", "two\n")
	if err := os.Rename(filepath.Join(dir, "moved.txt"), filepath.Join(dir, "moved-unstaged.txt")); err != nil {
		t.Fatal(err)
	}
	write("café.txt", "fresh\n")
	write("debug.log", "ignored\n")
	// An agent's clone with nothing checked out: add -N would record it as
	// a gitlink, and then the diff dies trying to hash it.
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(filepath.Join(dir, "nested"), "init")
	return dir
}

func TestDiffShowsRenamesUncommittedAndUntrackedWork(t *testing.T) {
	dir := diffRepo(t)
	index := func() []byte {
		t.Helper()
		out, err := ExecRunner().Run(dir, "rev-parse", "--path-format=absolute", "--git-path", "index")
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(strings.TrimSpace(out))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	before := index()
	p, err := Diff(dir, "main", 1<<20)
	if err != nil || p.Truncated || p.Base != "main" {
		t.Fatalf("Diff = %+v, err %v", p, err)
	}
	for _, want := range []string{
		"diff --git a/old.txt b/new.txt\nsimilarity index 100%\nrename from old.txt\nrename to new.txt\n",
		"diff --git a/edited.txt b/edited.txt\n",
		"-one\n+two\n",
		// Moved without git mv: a delete and an untracked file to git
		// status, but a rename to anyone reading the diff.
		"diff --git a/moved.txt b/moved-unstaged.txt\nsimilarity index 100%\n",
		// core.quotePath off: the name as itself, not "a/caf\303\251.txt".
		"diff --git a/café.txt b/café.txt\nnew file mode 100644\n",
		"--- /dev/null\n+++ b/café.txt\n@@ -0,0 +1 @@\n+fresh\n",
	} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("patch is missing %q:\n%s", want, p.Text)
		}
	}
	for _, not := range []string{"debug.log", "nested"} {
		if strings.Contains(p.Text, not) {
			t.Errorf("%s made it into the patch:\n%s", not, p.Text)
		}
	}
	// Untracked files are marked in a copy: the agent's index is its own,
	// byte for byte, and the copy doesn't outlive the call.
	if !bytes.Equal(index(), before) {
		t.Error("Diff wrote the worktree's index")
	}
	if out, _ := ExecRunner().Run(dir, "status", "--porcelain", "café.txt"); !strings.HasPrefix(out, "??") {
		t.Errorf("café.txt should still be untracked, status %q", out)
	}
	gitDir, _ := ExecRunner().Run(dir, "rev-parse", "--path-format=absolute", "--git-dir")
	if left, _ := filepath.Glob(filepath.Join(strings.TrimSpace(gitDir), "moomux-diff-index-*")); len(left) > 0 {
		t.Errorf("scratch index left behind: %v", left)
	}
}

// An edit landing in the same instant as the last index write — same size,
// same mtime as the cached entry — is one git catches only because the
// entry is no older than the index file ("racy git"). A scratch copy
// stamped with a fresh mtime would hide it; pinned here deterministically,
// with ctime out of the comparison and both mtimes set by hand.
func TestDiffSeesAnEditRacingTheIndexWrite(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		args = append([]string{"-c", "user.name=moomux", "-c", "user.email=moomux@localhost"}, args...)
		out, err := ExecRunner().Run(dir, args...)
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return out
	}
	f := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(f, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("init", "-b", "main")
	git("config", "core.trustctime", "false")
	git("config", "core.checkStat", "minimal")
	// An hour back, before add: an entry stamped in the same instant as
	// its index write is "smudged" and content-checked forever after,
	// which would pass this test with or without the fix.
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(f, at, at); err != nil {
		t.Fatal(err)
	}
	git("add", "f.txt")
	git("commit", "-m", "init")
	if err := os.WriteFile(f, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{f, filepath.Join(dir, ".git", "index")} {
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Diff(dir, "main", 1<<20)
	if err != nil || !strings.Contains(p.Text, "-one\n+two\n") {
		t.Fatalf("the racing edit is missing (err %v):\n%s", err, p.Text)
	}
}

// A client parses the patch, so the user's diff config must not reshape
// its headers.
func TestDiffHeadersIgnoreThePrefixConfig(t *testing.T) {
	dir := diffRepo(t)
	for _, kv := range [][]string{{"diff.noprefix", "true"}, {"diff.mnemonicPrefix", "true"}, {"color.diff", "always"}} {
		if out, err := ExecRunner().Run(dir, "config", kv[0], kv[1]); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	p, err := Diff(dir, "main", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Text, "diff --git a/edited.txt b/edited.txt\n") ||
		!strings.Contains(p.Text, "--- a/edited.txt\n+++ b/edited.txt\n") {
		t.Fatalf("want a/ and b/ prefixes whatever the config says:\n%s", p.Text)
	}
	if strings.Contains(p.Text, "\x1b[") {
		t.Fatalf("colour escapes in the patch:\n%s", p.Text)
	}
}

func TestDiffFallsBackToHEADWithoutTheBase(t *testing.T) {
	dir := diffRepo(t)
	p, err := Diff(dir, "no-such-branch", 1<<20)
	if err != nil || p.Base != "HEAD" {
		t.Fatalf("Diff = base %q, err %v", p.Base, err)
	}
	// Against HEAD the committed rename is already in, the rest isn't.
	if strings.Contains(p.Text, "b/new.txt") || !strings.Contains(p.Text, "+two\n") || !strings.Contains(p.Text, "+fresh\n") {
		t.Fatalf("want uncommitted and untracked work only:\n%s", p.Text)
	}
}

// A base that shares no history with the branch has no merge base, and
// --merge-base fails outright on it. That is a reason to fall back, not to
// fail the whole diff.
func TestDiffFallsBackToHEADWithoutAMergeBase(t *testing.T) {
	dir := diffRepo(t)
	if out, err := ExecRunner().Run(dir, "-c", "user.name=m", "-c", "user.email=m@m", "commit-tree", "-m", "orphan",
		"4b825dc642cb6eb9a060e54bf8d69288fbee4904"); err != nil {
		t.Fatalf("%v: %s", err, out)
	} else if out, err := ExecRunner().Run(dir, "branch", "orphan", strings.TrimSpace(out)); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	p, err := Diff(dir, "orphan", 1<<20)
	if err != nil || p.Base != "HEAD" || !strings.Contains(p.Text, "+two\n") {
		t.Fatalf("Diff = %+v, err %v", p, err)
	}
}

// The cap cuts between files, never inside one.
func TestDiffCutsAtAFileBoundary(t *testing.T) {
	dir := diffRepo(t)
	full, err := Diff(dir, "main", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	starts := []int{}
	for i := 0; i < len(full.Text); i++ {
		if strings.HasPrefix(full.Text[i:], "diff --git ") && (i == 0 || full.Text[i-1] == '\n') {
			starts = append(starts, i)
		}
	}
	if len(starts) != 4 {
		t.Fatalf("want 4 files in the full patch, got %d:\n%s", len(starts), full.Text)
	}
	// Just short of each file's end: that file is dropped, the ones
	// before it kept whole.
	ends := append(starts[1:], len(full.Text))
	for i, end := range ends {
		p, err := Diff(dir, "main", end-1)
		if err != nil || !p.Truncated {
			t.Fatalf("limit %d: %+v, err %v", end-1, p, err)
		}
		if p.Text != full.Text[:starts[i]] {
			t.Fatalf("limit %d: want the first %d files whole, got:\n%s", end-1, i, p.Text)
		}
	}
	if p, _ := Diff(dir, "main", len(full.Text)); p.Truncated || p.Text != full.Text {
		t.Fatalf("a patch of exactly the limit should come back whole, truncated=%v", p.Truncated)
	}
}

func TestDiffOnANonRepoIsErrNotGitRepo(t *testing.T) {
	if _, err := Diff(t.TempDir(), "main", 1<<20); !errors.Is(err, ErrNotGitRepo) {
		t.Fatalf("err = %v, want ErrNotGitRepo", err)
	}
}
