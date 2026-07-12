// Package gitwt wraps git worktree subcommands.
package gitwt

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

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
		{"commit", "--allow-empty", "-m", "initial commit"},
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
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
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

func (c *Client) AddWorktree(repoDir, worktreePath, branch, baseBranch string) error {
	start := baseBranch
	if c.HasRemote(repoDir, "origin") {
		start = "origin/" + baseBranch
	}
	if c.BranchExists(repoDir, branch) {
		// Leftover from an orphaned worktree (branch survives, checkout doesn't).
		// If it's actually checked out elsewhere, this delete fails with a clear
		// "cannot delete branch checked out at ..." error instead of the more
		// confusing "-b" already-exists error below.
		if _, err := c.Runner.Run(repoDir, "branch", "-D", branch); err != nil {
			return err
		}
	}
	_, err := c.Runner.Run(repoDir, "worktree", "add", worktreePath, "-b", branch, start)
	return err
}

// BranchExists reports whether a local branch with the given name exists.
func (c *Client) BranchExists(repoDir, branch string) bool {
	_, err := c.Runner.Run(repoDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// AddWorktreeExisting links worktreePath to an already-existing branch
// (local, or remote-tracking via git's single-remote DWIM) instead of
// creating a new branch.
func (c *Client) AddWorktreeExisting(repoDir, worktreePath, branch string) error {
	_, err := c.Runner.Run(repoDir, "worktree", "add", worktreePath, branch)
	return err
}

func (c *Client) RemoveWorktree(repoDir, worktreePath string) error {
	_, err := c.Runner.Run(repoDir, "worktree", "remove", worktreePath, "--force")
	if _, statErr := os.Stat(worktreePath); statErr == nil {
		// git reported the worktree gone (or failed) but left the directory on
		// disk — seen in practice even on a clean --force removal. Finish the
		// job ourselves rather than leaving an orphaned checkout behind.
		if rmErr := os.RemoveAll(worktreePath); rmErr != nil {
			if err == nil {
				err = rmErr
			}
			return err
		}
		_, _ = c.Runner.Run(repoDir, "worktree", "prune")
		return nil
	}
	return err
}

// diffTarget resolves the git revision to diff a worktree against, given an
// ordered list of candidate base refs (e.g. "origin/main", "main"). It uses
// the first candidate that resolves to a commit, returning the merge-base of
// that ref and HEAD — so the diff shows everything this branch added since it
// forked (committed, staged, and unstaged tracked changes), and not unrelated
// commits that landed on the base afterwards.
//
// Falling straight to HEAD when the base can't be found is deliberately
// avoided: `git diff HEAD` shows only uncommitted changes and would silently
// hide all of the branch's committed work. HEAD is used only as a last resort
// when no candidate resolves at all (e.g. plain projects with no base branch).
func (c *Client) diffTarget(worktree string, refs ...string) string {
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		if _, err := c.Runner.Run(worktree, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
			continue // ref doesn't resolve in this worktree; try the next
		}
		if out, err := c.Runner.Run(worktree, "merge-base", ref, "HEAD"); err == nil {
			if base := strings.TrimSpace(out); base != "" {
				return base
			}
		}
		return ref // resolved but no common ancestor with HEAD — diff vs the ref itself
	}
	return "HEAD"
}

// DiffAgainst returns the unified diff of the worktree — committed, staged,
// and unstaged tracked changes — since it diverged from the first resolvable
// base ref (see diffTarget). Untracked files are not included.
func (c *Client) DiffAgainst(worktree string, refs ...string) (string, error) {
	return c.Runner.Run(worktree, "diff", c.diffTarget(worktree, refs...))
}

// DiffStatAgainst returns a files/additions/deletions summary of the same
// range DiffAgainst reports, parsed from `git diff --numstat`.
func (c *Client) DiffStatAgainst(worktree string, refs ...string) (DiffStat, error) {
	out, err := c.Runner.Run(worktree, "diff", "--numstat", c.diffTarget(worktree, refs...))
	if err != nil {
		return DiffStat{}, err
	}
	return parseNumstat(out), nil
}

// DiffStat mirrors session.DiffStat but lives here to avoid gitwt importing
// the session package; app translates between the two.
type DiffStat struct {
	Files     int
	Additions int
	Deletions int
}

// parseNumstat sums `git diff --numstat` output. Each non-empty line is
// "<added>\t<deleted>\t<path>"; binary files report "-\t-\t<path>", which
// contributes to the file count but not the line totals.
func parseNumstat(out string) DiffStat {
	var d DiffStat
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		d.Files++
		if n, err := strconv.Atoi(fields[0]); err == nil {
			d.Additions += n
		}
		if n, err := strconv.Atoi(fields[1]); err == nil {
			d.Deletions += n
		}
	}
	return d
}

// DeleteBranch force-deletes a local branch, e.g. after its worktree has been
// removed. Callers should only do this for branches moomux created itself —
// deleting a branch the user checked out on purpose would be destructive.
func (c *Client) DeleteBranch(repoDir, branch string) error {
	_, err := c.Runner.Run(repoDir, "branch", "-D", branch)
	return err
}
