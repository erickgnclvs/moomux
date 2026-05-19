// Package tmux wraps the tmux CLI behind an injectable runner.
package tmux

import (
	"errors"
	"os/exec"
)

type Runner interface {
	Run(args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return string(out), err
}

func ExecRunner() Runner { return execRunner{} }

type Client struct {
	Runner Runner
}

func New() *Client { return &Client{Runner: ExecRunner()} }

// HasSession reports whether tmux session `name` exists.
func (c *Client) HasSession(name string) (bool, error) {
	_, err := c.Runner.Run("has-session", "-t", name)
	if err == nil {
		return true, nil
	}
	var exitErr interface{ ExitCode() int }
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, err
}

// NewSession creates a detached tmux session at cwd and sends `cmd` + Enter.
// If cmd is empty, no command is sent.
func (c *Client) NewSession(name, cwd, cmd string) error {
	if _, err := c.Runner.Run("new-session", "-d", "-s", name, "-c", cwd); err != nil {
		return err
	}
	if cmd != "" {
		if _, err := c.Runner.Run("send-keys", "-t", name, cmd, "Enter"); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) KillSession(name string) error {
	_, err := c.Runner.Run("kill-session", "-t", name)
	return err
}
