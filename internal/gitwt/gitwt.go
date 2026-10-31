// Package gitwt wraps git worktree subcommands.
package gitwt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/erickgnclvs/moomux/internal/filecap"
)

// runTimeout bounds every git subprocess execRunner spawns. Client methods
// don't carry a caller context (they're called synchronously from app-layer
// code with no ctx of its own), so an unresponsive git (e.g. a fetch against
// a dead remote) is bounded by a fixed timeout instead of hanging forever.
var runTimeout = 30 * time.Second

// noPrompt is in every git subprocess's environment. An HTTPS remote with no
// credential helper makes git ask "Username for ..." on /dev/tty — the
// terminal moomux's TUI is drawn on — which stole keystrokes from the UI and
// held a session's create for the whole runTimeout. With it, git fails fast
// and the best-effort fetches just skip.
// ponytail: covers git's own prompt only; an ssh passphrase/host-key prompt
// still reaches /dev/tty — Setsid on the cmd if that ever bites.
const noPrompt = "GIT_TERMINAL_PROMPT=0"

// ErrNotGitRepo is returned when a path is not inside a git working tree.
var ErrNotGitRepo = errors.New("not a git repository")

// IsRepo returns nil if path is inside a git working tree.
// If path is missing or not a git repo, returns an error wrapping ErrNotGitRepo.
func IsRepo(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%w: %s", ErrNotGitRepo, path)
	}
	cmd := exec.Command("git", "-C", path, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (%s): %s", ErrNotGitRepo, path, strings.TrimSpace(string(out)))
	}
	return nil
}

// Init creates path (if missing), runs git init with the given default branch,
// and makes an empty initial commit so worktrees can branch off it.
func Init(path, defaultBranch string) error {
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", path, err)
	}
	steps := [][]string{
		{"init", "-b", defaultBranch},
		// -c user.* scopes the identity to just this commit instead of
		// requiring the caller to have a global git identity configured
		// (a fresh machine/container/CI runner commonly doesn't).
		{"-c", "user.name=moomux", "-c", "user.email=moomux@localhost", "commit", "--allow-empty", "-m", "initial commit"},
	}
	for _, args := range steps {
		cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w (%s)", args, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// HasRemote returns true if the given remote (e.g. "origin") is configured.
func (c *Client) HasRemote(repoDir, name string) bool {
	_, err := c.Runner.Run(repoDir, "remote", "get-url", name)
	return err == nil
}

type Runner interface {
	Run(dir string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), noPrompt)
	// Without WaitDelay, CombinedOutput can still block past ctx's
	// deadline: if git forked a child that inherited the output pipe,
	// killing git alone doesn't close it — Read() waits for every process
	// holding the write end to exit, not just the one we canceled.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %v in %s: %w (%s)", args, dir, err, string(out))
	}
	return string(out), nil
}

func ExecRunner() Runner { return execRunner{} }

type Client struct {
	Runner Runner
}

func New() *Client { return &Client{Runner: ExecRunner()} }

func (c *Client) Fetch(repoDir, baseBranch string) error {
	_, err := c.Runner.Run(repoDir, "fetch", "origin", baseBranch)
	return err
}

// ErrBaseBranchNotFound is AddWorktree's error for a base ref that resolves
// to nothing. The caller says what to fix: only it knows whether the name
// came from the project's settings or from the new-session form.
var ErrBaseBranchNotFound = errors.New("not found")

func (c *Client) AddWorktree(repoDir, worktreePath, branch, baseBranch string) error {
	start := baseBranch
	if c.HasRemote(repoDir, "origin") {
		start = "origin/" + baseBranch
	}
	// Usually the project's base branch was renamed or deleted upstream (a
	// finished release branch, say). Say which ref is missing and where it
	// came from — git alone reports "fatal: invalid reference: origin/x",
	// which reads as if the new branch name were the broken one.
	if _, err := c.Runner.Run(repoDir, "rev-parse", "--verify", "--quiet", start+"^{commit}"); err != nil {
		return fmt.Errorf("base branch %q %w (looked for %s)", baseBranch, ErrBaseBranchNotFound, start)
	}
	if c.BranchExists(repoDir, branch) {
		// Usually a leftover from an orphaned worktree (branch survives,
		// checkout doesn't) — but it could equally be the user's own branch
		// with unpushed commits, so only delete what git considers merged
		// (-d, not -D). Anything unmerged or checked out elsewhere fails
		// here with a clear message instead of losing commits.
		if _, err := c.Runner.Run(repoDir, "branch", "-d", branch); err != nil {
			return fmt.Errorf("branch %q already exists and isn't fully merged — create the session from the existing branch instead, or delete the branch manually: %w", branch, err)
		}
	}
	if _, err := c.Runner.Run(repoDir, "worktree", "add", worktreePath, "-b", branch, start); err != nil {
		return err
	}
	return c.lockNewWorktree(repoDir, worktreePath)
}

// lockNewWorktree locks a just-added worktree against accidental removal by
// other tools. If locking fails, the checkout it just created would
// otherwise be left behind as an untracked orphan (add succeeded, caller
// sees an error and never registers it) — so we remove it here to fail
// clean instead.
func (c *Client) lockNewWorktree(repoDir, worktreePath string) error {
	if _, err := c.Runner.Run(repoDir, "worktree", "lock", worktreePath, "--reason", "moomux"); err != nil {
		_ = c.RemoveWorktree(repoDir, worktreePath)
		return err
	}
	return nil
}

// WorktreeForBranch returns the path of the worktree that currently has
// branch checked out, or "" if none does.
func (c *Client) WorktreeForBranch(repoDir, branch string) (string, error) {
	out, err := c.Runner.Run(repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	var path string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case line == "branch refs/heads/"+branch:
			return path, nil
		}
	}
	return "", nil
}

// IsWorktreeClean reports whether worktreePath has no uncommitted changes.
func (c *Client) IsWorktreeClean(worktreePath string) (bool, error) {
	out, err := c.Runner.Run(worktreePath, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// HasUnpushedCommits reports whether worktreePath's checked-out branch has
// commits its upstream doesn't. A branch that never had an upstream counts
// as unpushed, since nothing has been pushed at all. A branch whose upstream
// was configured but has since disappeared — e.g. the remote branch was
// deleted after the PR merged — has nothing left to compare against, so it
// counts as not unpushed rather than reading as stuck forever.
func (c *Client) HasUnpushedCommits(worktreePath string) (bool, error) {
	if _, err := c.Runner.Run(worktreePath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err != nil {
		branch, err := c.Runner.Run(worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return true, nil
		}
		// branch.<name>.merge survives the remote ref being pruned, so its
		// presence tells "upstream gone" apart from "never configured".
		if _, err := c.Runner.Run(worktreePath, "config", "--get", "branch."+strings.TrimSpace(branch)+".merge"); err == nil {
			return false, nil
		}
		return true, nil
	}
	out, err := c.Runner.Run(worktreePath, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "0", nil
}

// FilesChangedCount reports how many files worktreePath has uncommitted
// changes in (staged, unstaged, or untracked) — one count per line of `git
// status --porcelain`. Separate from IsWorktreeClean (rather than having it
// call this and check >0) so that function's git-call sequence, and the
// tests pinned to it, don't shift.
func (c *Client) FilesChangedCount(worktreePath string) (int, error) {
	out, err := c.Runner.Run(worktreePath, "status", "--porcelain")
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(out) == "" {
		return 0, nil
	}
	return len(strings.Split(strings.TrimRight(out, "\n"), "\n")), nil
}

// UnpushedCommitCount reports how many commits worktreePath's checked-out
// branch has that its upstream doesn't. A branch that never had an upstream
// counts every commit on it as unpushed, since nothing has been pushed at
// all. A branch whose upstream was configured but has since disappeared
// (e.g. the remote branch was deleted after the PR merged) reports 0 —
// there's nothing left to compare against. Mirrors HasUnpushedCommits'
// classification but returns a count instead of a bool; kept as a separate
// method (rather than deriving HasUnpushedCommits from it) so that
// function's git-call sequence, and the tests pinned to it, don't shift.
func (c *Client) UnpushedCommitCount(worktreePath string) (int, error) {
	if _, err := c.Runner.Run(worktreePath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err != nil {
		branch, err := c.Runner.Run(worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return 0, nil
		}
		if _, err := c.Runner.Run(worktreePath, "config", "--get", "branch."+strings.TrimSpace(branch)+".merge"); err == nil {
			return 0, nil
		}
		out, err := c.Runner.Run(worktreePath, "rev-list", "--count", "HEAD")
		if err != nil {
			return 0, nil
		}
		n, _ := strconv.Atoi(strings.TrimSpace(out))
		return n, nil
	}
	out, err := c.Runner.Run(worktreePath, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return 0, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n, nil
}

// BranchExists reports whether a local branch with the given name exists.
func (c *Client) BranchExists(repoDir, branch string) bool {
	_, err := c.Runner.Run(repoDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// RemoteBranchExists reports whether origin has the branch (as a
// remote-tracking ref), which is what git's worktree-add DWIM checks out.
func (c *Client) RemoteBranchExists(repoDir, branch string) bool {
	_, err := c.Runner.Run(repoDir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	return err == nil
}

// AddWorktreeExisting links worktreePath to an already-existing branch
// (local, or remote-tracking via git's single-remote DWIM) instead of
// creating a new branch.
func (c *Client) AddWorktreeExisting(repoDir, worktreePath, branch string) error {
	if _, err := c.Runner.Run(repoDir, "worktree", "add", worktreePath, branch); err != nil {
		return err
	}
	return c.lockNewWorktree(repoDir, worktreePath)
}

func (c *Client) RemoveWorktree(repoDir, worktreePath string) error {
	// A single --force overrides a dirty worktree but not a locked one (by
	// moomux itself, see AddWorktree, or by another tool) — git demands
	// --force twice, or an unlock first, to remove a locked worktree.
	_, err := c.Runner.Run(repoDir, "worktree", "remove", worktreePath, "--force", "--force")
	if _, statErr := os.Stat(worktreePath); statErr != nil {
		if os.IsNotExist(statErr) {
			// The checkout is already gone (e.g. another session removed
			// it). Nothing left to remove — drop any stale registration and
			// succeed instead of failing on a worktree that no longer
			// exists.
			_, _ = c.Runner.Run(repoDir, "worktree", "prune")
			return nil
		}
		// Some other stat failure (e.g. permission denied) — we don't
		// actually know whether the worktree is gone, so don't assume
		// success. Surface whichever error is more informative.
		if err != nil {
			return err
		}
		return statErr
	}
	if err != nil {
		// git refused to remove it and the directory is still there. If
		// it's an orphaned worktree checkout — its .git file still points
		// at a worktrees/ gitdir that git itself has already forgotten
		// (e.g. someone ran `git worktree prune` in the main repo without
		// going through moomux) — finish the cleanup ourselves. Otherwise
		// it's the repo's main working tree (a no-worktree session's
		// WorktreePath IS the repo) or a checkout belonging to another
		// repo; deleting it ourselves is how a stale entry wipes a real
		// repository, so surface the error instead.
		if !isOrphanedWorktreeCheckout(worktreePath) {
			return err
		}
		if rmErr := os.RemoveAll(worktreePath); rmErr != nil {
			return rmErr
		}
		_, _ = c.Runner.Run(repoDir, "worktree", "prune")
		return nil
	}
	// git reported success but left the directory on disk — seen in
	// practice even on a clean --force removal. Finish the job ourselves
	// rather than leaving an orphaned checkout behind.
	if rmErr := os.RemoveAll(worktreePath); rmErr != nil {
		return rmErr
	}
	_, _ = c.Runner.Run(repoDir, "worktree", "prune")
	return nil
}

// isOrphanedWorktreeCheckout reports whether path is a worktree checkout
// whose git-side registration is already gone — either its .git file points
// at a worktrees/<name> gitdir that no longer exists, or .git is missing
// entirely (e.g. worktree creation died before `git worktree add` ran). Real
// repos (a full .git directory) return false, so callers only treat the
// checkout as safe to delete themselves when git has nothing left to lose
// track of.
func isOrphanedWorktreeCheckout(path string) bool {
	info, err := os.Stat(path + "/.git")
	if err != nil {
		return os.IsNotExist(err)
	}
	if info.IsDir() {
		return false
	}
	data, err := os.ReadFile(path + "/.git")
	if err != nil {
		return false
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir:"))
	if gitdir == "" {
		return false
	}
	_, statErr := os.Stat(gitdir)
	return os.IsNotExist(statErr)
}

// DeleteBranch force-deletes a local branch, e.g. after its worktree has been
// removed. Callers should only do this for branches moomux created itself —
// deleting a branch the user checked out on purpose would be destructive.
func (c *Client) DeleteBranch(repoDir, branch string) error {
	_, err := c.Runner.Run(repoDir, "branch", "-D", branch)
	return err
}

// Patch is Diff's answer.
type Patch struct {
	// Text is raw `git diff` output, headers and all.
	Text string
	// Base is the ref Text was taken against: origin/<base> or <base>
	// (from their merge base), or HEAD when neither exists or shares
	// history with it — in which case Text is uncommitted work only.
	Base string
	// Commit is the commit Text was taken against: that merge base, or
	// HEAD's commit. ReadAt with it reads the patch's "before" side, and
	// keeps reading the same one if the base branch moves on.
	Commit string
	// Truncated says Text was cut at a file boundary to stay under the
	// limit.
	Truncated bool
	// Uncommitted is the paths, as Text's b/ side names them, that differ
	// from HEAD: staged, unstaged or untracked. Every other file in Text
	// is committed on the branch. Empty for a worktree whose only changes
	// are its commits — an open PR's, say — which is not local work.
	Uncommitted []string
}

// Diff is everything in dir not yet on base, as one raw patch: `git diff
// --merge-base` (commits since the merge base and uncommitted work alike,
// with Uncommitted naming the latter)
// with every untracked, non-ignored file in it as a new file. The base is
// resolved by BaseCommit, and the diff is taken against the commit it
// answers.
//
// Untracked files are marked intent-to-add (`add -N`) in a throwaway copy
// of the index, so one diff process covers them and a file moved without
// `git mv` still reads as a rename. The worktree's own index belongs to the
// agent working in it and is never written. Nested repos are left out: add
// would record them as gitlinks, and an empty one makes the diff fatal.
//
// Output is pinned against the user's config — a/ and b/ prefixes whatever
// diff.noprefix or diff.mnemonicPrefix say, unquoted UTF-8 paths, no
// colour, no external diff — because a client parses it. The patch is cut
// at a file boundary before it passes limit bytes, and Truncated says so;
// a first file bigger than limit on its own leaves Text empty. A dir that
// isn't a git repo wraps ErrNotGitRepo.
//
// Not through Runner: that returns stderr folded into stdout, and a warning
// git prints mid-diff would land inside the patch.
func Diff(dir, base string, limit int) (Patch, error) {
	ref, commit, err := BaseCommit(dir, base)
	if err != nil {
		return Patch{}, err
	}
	p := Patch{Base: ref, Commit: commit}
	index, err := scratchIndex(dir)
	if err != nil {
		return Patch{}, err
	}
	defer os.Remove(index)
	defer os.Remove(index + ".lock")
	env := []string{"GIT_INDEX_FILE=" + index}
	others, _, err := gitStdout(dir, env, 0, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return Patch{}, err
	}
	if others != "" {
		// `:/` rather than the listed names: a file the agent deletes
		// before add runs would fail an explicit pathspec, and the whole
		// diff with it. ls-files ends a nested repo's entry with a slash.
		add := []string{"add", "-N", "--", ":/"}
		for _, name := range strings.Split(others, "\x00") {
			if strings.HasSuffix(name, "/") {
				add = append(add, ":(top,exclude,literal)"+strings.TrimSuffix(name, "/"))
			}
		}
		if _, _, err := gitStdout(dir, env, 0, add...); err != nil {
			return Patch{}, err
		}
	}
	// Against HEAD, with the same scratch index, so untracked files count
	// and a plain mv names the file it became, as Text does.
	names, _, err := gitStdout(dir, env, 0, "diff", "--name-only", "-z", "--no-ext-diff", "-M", "HEAD")
	if err != nil {
		return Patch{}, err
	}
	if names = strings.TrimSuffix(names, "\x00"); names != "" {
		p.Uncommitted = strings.Split(names, "\x00")
	}
	// The commit itself, not --merge-base <ref>: the ref can move between
	// resolving it and diffing, and Commit must be what Text came from.
	// It is git's own hex output, so it can't parse as a flag.
	args := []string{"-c", "core.quotePath=false", "diff", "--no-color", "--no-ext-diff", "-M",
		"--src-prefix=a/", "--dst-prefix=b/", commit}
	// limit+1 so a patch of exactly limit bytes isn't mistaken for a cut one.
	out, over, err := gitStdout(dir, env, limit+1, args...)
	if err != nil {
		return Patch{}, err
	}
	p.Text, p.Truncated = out, over
	if over {
		p.Text = cutAtFile(out, limit)
	}
	return p, nil
}

// BaseCommit resolves base for Diff, ReadAt's callers and the review
// window — origin/<base>, then <base>, then HEAD — and answers the ref it settled on
// and the commit a diff against it starts from: the ref's merge base with
// HEAD, or HEAD's own commit. A ref sharing no history with HEAD has no
// merge base and is skipped, rather than failing the diff. A dir that isn't
// a git repo wraps ErrNotGitRepo.
func BaseCommit(dir, base string) (ref, commit string, err error) {
	if err := IsRepo(dir); err != nil {
		return "", "", err
	}
	for _, ref := range []string{"origin/" + base, base} {
		if out, _, err := gitStdout(dir, nil, 0, "merge-base", ref, "HEAD"); err == nil {
			return ref, strings.TrimSpace(out), nil
		}
	}
	out, _, err := gitStdout(dir, nil, 0, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", "", fmt.Errorf("no commit to diff against: %s", reason(err))
	}
	return "HEAD", strings.TrimSpace(out), nil
}

// TrackedBase is the branch dir's checkout tracks, when that is some other
// branch: `worktree add -b x origin/base` sets that up, and it lasts until a
// `push -u` points the branch at its own remote copy. It answers empty for
// no upstream, or for the branch's own copy, which would diff away exactly
// the work being reviewed. The remote is dropped, so BaseCommit resolves
// the answer like any other base.
func TrackedBase(dir string) string {
	out, _, err := gitStdout(dir, nil, 0, "rev-parse", "--symbolic-full-name", "HEAD", "HEAD@{upstream}")
	if err != nil {
		return ""
	}
	head, up, _ := strings.Cut(strings.TrimSpace(out), "\n")
	base, local := strings.CutPrefix(up, "refs/heads/")
	if !local {
		// refs/remotes/<remote>/<branch>; a remote name with a slash in it
		// would be cut wrong, and git allows one, but nobody names one so.
		_, base, _ = strings.Cut(strings.TrimPrefix(up, "refs/remotes/"), "/")
	}
	if base == "" || "refs/heads/"+base == head {
		return ""
	}
	return base
}

// commitID is a full commit id, SHA-1 or SHA-256. ReadAt takes nothing
// looser: a ref name or an expression could start with "-" and read as an
// option, and the only commit a client has to pass is one Diff gave it.
var commitID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// ReadAt is path's content in commit: with a Patch's Commit, the "before"
// side of that patch. path is repo-relative, as the patch's a/ side names
// it. found is false, with no error, when commit has nothing at path — a
// file the patch adds. A symlink reads as its target, as the patch shows
// it; a directory, a submodule or a path inside one is an error. A blob
// over limit bytes is an error, judged by its size before it is read, and
// worded by filecap.TooBig. Only objects are read: the worktree and its
// index are never touched. Errors are fit to show a user: no git argv, no
// paths beyond the one asked for. A dir that isn't a git repo wraps
// ErrNotGitRepo.
func ReadAt(dir, commit, path string, limit int64) (data []byte, found bool, err error) {
	if err := IsRepo(dir); err != nil {
		return nil, false, err
	}
	if !commitID.MatchString(commit) {
		return nil, false, fmt.Errorf("%q is not a commit id", commit)
	}
	if path == "" || path == "." || strings.HasPrefix(path, "/") || pathpkg.Clean(path) != path ||
		path == ".." || strings.HasPrefix(path, "../") {
		return nil, false, fmt.Errorf("%q is not a path inside the repository", path)
	}
	entries, err := lsTree(dir, commit, path)
	if err != nil {
		// Asked only on failure: the likely cause, said plainly.
		if _, _, e := gitStdout(dir, nil, 0, "cat-file", "-e", commit+"^{commit}"); e != nil {
			return nil, false, fmt.Errorf("commit %.7s is not in this repository", commit)
		}
		return nil, false, fmt.Errorf("couldn't read %s at %.7s: %s", path, commit, reason(err))
	}
	e, ok := entries[path]
	if !ok {
		// Nothing at path, or path is inside a submodule, which lists
		// nothing for anything under it. Only a parent's own entry tells
		// the two apart, so a miss asks about the parents too.
		var parents []string
		for p := pathpkg.Dir(path); p != "."; p = pathpkg.Dir(p) {
			parents = append(parents, p)
		}
		if len(parents) == 0 {
			return nil, false, nil
		}
		entries, err := lsTree(dir, commit, parents...)
		if err != nil {
			return nil, false, fmt.Errorf("couldn't read %s at %.7s: %s", path, commit, reason(err))
		}
		for name, e := range entries {
			if e.typ == "commit" && strings.HasPrefix(path, name+"/") {
				return nil, false, fmt.Errorf("%s is inside the submodule %s", path, name)
			}
		}
		return nil, false, nil
	}
	switch e.typ {
	case "blob":
	case "tree":
		return nil, false, fmt.Errorf("%s is a directory", path)
	default:
		return nil, false, fmt.Errorf("%s is a submodule", path)
	}
	size, err := strconv.ParseInt(e.size, 10, 64)
	if err != nil {
		return nil, false, fmt.Errorf("couldn't read %s at %.7s: size %q", path, commit, e.size)
	}
	if size > limit {
		return nil, false, filecap.TooBig(size, limit)
	}
	// size+1: read into one allocation of the right size; a blob is
	// immutable, so it is never over.
	blob, _, err := gitOut(dir, nil, int(size)+1, "cat-file", "blob", e.object)
	if err != nil {
		return nil, false, fmt.Errorf("couldn't read %s at %.7s: %s", path, commit, reason(err))
	}
	return blob, true, nil
}

// treeEntry is one line of `git ls-tree -l`.
type treeEntry struct{ typ, object, size string }

// lsTree is commit's entries for paths, keyed by name: each path's own
// entry, and whatever else ls-tree lists alongside them. --literal-pathspecs
// so a name like ":(glob)*" is only itself.
func lsTree(dir, commit string, paths ...string) (map[string]treeEntry, error) {
	args := append([]string{"--literal-pathspecs", "ls-tree", "-z", "-l", "--full-tree", commit, "--"}, paths...)
	out, _, err := gitStdout(dir, nil, 0, args...)
	if err != nil {
		return nil, err
	}
	entries := map[string]treeEntry{}
	// "<mode> <type> <object> <size>\t<name>\x00" per entry.
	for _, line := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		meta, name, _ := strings.Cut(line, "\t")
		if f := strings.Fields(meta); len(f) == 4 {
			entries[name] = treeEntry{typ: f[1], object: f[2], size: f[3]}
		}
	}
	return entries, nil
}

// scratchIndex copies dir's index to a new file beside it and returns the
// copy's path. Beside it rather than in the temp dir because a split index
// finds its shared half relative to the index file. A repo with no index
// yet gets a path with nothing at it, which git reads as an empty index.
func scratchIndex(dir string) (string, error) {
	real, _, err := gitStdout(dir, nil, 0, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", err
	}
	real = strings.TrimSpace(real)
	f, err := os.CreateTemp(filepath.Dir(real), "moomux-diff-index-*")
	if err != nil {
		return "", err
	}
	defer f.Close()
	src, err := os.Open(real)
	if errors.Is(err, os.ErrNotExist) {
		return f.Name(), os.Remove(f.Name())
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	defer src.Close()
	info, err := src.Stat()
	if err == nil {
		_, err = io.Copy(f, src)
	}
	if err == nil {
		err = f.Close()
	}
	// The copy keeps the original's mtime. Git trusts an entry's cached
	// stat only if it is older than the index file itself; a copy stamped
	// "now" makes a file edited in the same instant as the last index
	// write — same size, same mtime — read as unchanged, and its edit
	// silently drops out of the diff.
	if err == nil {
		err = os.Chtimes(f.Name(), info.ModTime(), info.ModTime())
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// cutAtFile is patch's longest prefix of whole files within limit bytes. A
// "diff --git " at the start of a line is always a header: hunk lines start
// with a space, a + or a -.
func cutAtFile(patch string, limit int) string {
	if len(patch) <= limit {
		return patch
	}
	if i := strings.LastIndex(patch[:limit], "\ndiff --git "); i >= 0 {
		return patch[:i+1]
	}
	return ""
}

// gitStdout runs git in dir, with env added to its environment, and
// returns its stdout alone. With max > 0 it
// reads at most max bytes and reports over — and stops git, rather than
// buffering a generated file's worth of patch — once there would be more.
// A failed run is a *gitError.
func gitStdout(dir string, env []string, max int, args ...string) (out string, over bool, err error) {
	data, over, err := gitOut(dir, env, max, args...)
	return string(data), over, err
}

// gitOut is gitStdout as bytes. With max > 0 its buffer is sized for max
// bytes up front, so a caller that knows the output's size (ReadAt, from
// ls-tree) passes size+1 and the output is read into one allocation.
func gitOut(dir string, env []string, max int, args ...string) (out []byte, over bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), env...), noPrompt)
	cmd.WaitDelay = 2 * time.Second // see execRunner
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	var r io.Reader = stdout
	var buf bytes.Buffer
	if max > 0 {
		r = io.LimitReader(stdout, int64(max))
		// MinRead more, or ReadFrom grows the buffer for its last read.
		buf.Grow(max + bytes.MinRead)
	}
	_, readErr := buf.ReadFrom(r)
	data := buf.Bytes()
	if over = max > 0 && len(data) >= max; over {
		cancel() // killed on purpose; its exit status means nothing now
		_ = cmd.Wait()
		return data, true, nil
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		reason := strings.TrimPrefix(msg[strings.LastIndex(msg, "\n")+1:], "fatal: ")
		switch {
		case ctx.Err() != nil:
			reason = "git timed out"
		case reason == "":
			reason = "git " + err.Error()
		}
		return data, false, &gitError{
			msg:    fmt.Sprintf("git %v in %s: %v (%s)", args, dir, err, msg),
			reason: reason,
			err:    err,
		}
	}
	return data, false, readErr
}

// gitError is a failed git run. Error is the whole story — argv, dir,
// stderr — for a log. reason is git's own last line of complaint, or that
// it timed out, without the paths and flags: fit to show someone who never
// typed the command.
type gitError struct {
	msg, reason string
	err         error
}

func (e *gitError) Error() string { return e.msg }
func (e *gitError) Unwrap() error { return e.err }

// reason is err's gitError reason, or err itself when it isn't one.
func reason(err error) string {
	var ge *gitError
	if errors.As(err, &ge) {
		return ge.reason
	}
	return err.Error()
}
