// Package gitwt wraps git worktree subcommands.
package gitwt

import (
	"fmt"
	"os/exec"
)

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
	_, err := c.Runner.Run(repoDir, "worktree", "add", worktreePath, "-b", branch, "origin/"+baseBranch)
	return err
}

func (c *Client) RemoveWorktree(repoDir, worktreePath string) error {
	_, err := c.Runner.Run(repoDir, "worktree", "remove", worktreePath, "--force")
	return err
}
