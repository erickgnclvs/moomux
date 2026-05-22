// Package tmux wraps the tmux CLI behind an injectable runner.
package tmux

import (
	"errors"
	"os/exec"
	"strings"
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

// LiveSessions returns the set of currently running tmux session names via a
// single list-sessions call — much cheaper than N HasSession calls.
func (c *Client) LiveSessions() map[string]bool {
	out, err := c.Runner.Run("list-sessions", "-F", "#{session_name}")
	result := map[string]bool{}
	if err != nil {
		// tmux exits non-zero when no sessions exist; that's fine
		return result
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			result[line] = true
		}
	}
	return result
}

// NewSession creates a detached tmux session at cwd and sends `cmd` + Enter.
// If windowName is non-empty it is set as the initial window name via -n so
// terminals that read the tmux title (iTerm2, kitty, etc.) display it immediately.
// automatic-rename is disabled so the name is not overwritten by the shell.
// If cmd is empty, no command is sent.
func (c *Client) NewSession(name, cwd, cmd, windowName string) error {
	args := []string{"new-session", "-d", "-s", name, "-c", cwd}
	if windowName != "" {
		args = append(args, "-n", windowName)
	}
	if _, err := c.Runner.Run(args...); err != nil {
		return err
	}
	if windowName != "" {
		// Keep the window name stable; without this tmux replaces it with the
		// running process name (e.g. "bash") as soon as the shell starts.
		_, _ = c.Runner.Run("set-window-option", "-t", name, "automatic-rename", "off")
		// Make tmux continuously push the window name as the terminal title so
		// the shell's own PROMPT_COMMAND/precmd title updates don't win the race.
		_, _ = c.Runner.Run("set-option", "-t", name, "set-titles", "on")
		_, _ = c.Runner.Run("set-option", "-t", name, "set-titles-string", "#{window_name}")
	}
	if cmd != "" {
		if _, err := c.Runner.Run("send-keys", "-t", name, cmd, "Enter"); err != nil {
			return err
		}
	}
	return nil
}

// ConfigureTitleTracking ensures the tmux session keeps its window name stable
// and continuously emits it as the terminal title. Safe to call on existing
// sessions — idempotent tmux set-option calls never break anything.
func (c *Client) ConfigureTitleTracking(session, windowName string) {
	_, _ = c.Runner.Run("rename-window", "-t", session, windowName)
	_, _ = c.Runner.Run("set-window-option", "-t", session, "automatic-rename", "off")
	_, _ = c.Runner.Run("set-option", "-t", session, "set-titles", "on")
	_, _ = c.Runner.Run("set-option", "-t", session, "set-titles-string", "#{window_name}")
}

func (c *Client) KillSession(name string) error {
	_, err := c.Runner.Run("kill-session", "-t", name)
	return err
}
