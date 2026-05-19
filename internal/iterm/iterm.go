// Package iterm opens iTerm2 tabs that attach to a tmux session.
package iterm

import (
	"fmt"
	"os/exec"
)

type Runner interface {
	Run(script string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	return string(out), err
}

func ExecRunner() Runner { return execRunner{} }

type Client struct {
	Runner Runner
}

func New() *Client { return &Client{Runner: ExecRunner()} }

// OpenTab opens a new iTerm2 tab in the current window and attaches to tmuxSession.
func (c *Client) OpenTab(tmuxSession string) error {
	script := fmt.Sprintf(`
tell application "iTerm2"
	activate
	if (count of windows) = 0 then
		create window with default profile
	end if
	tell current window
		create tab with default profile
		tell current session of current tab
			write text "tmux attach -t %s"
		end tell
	end tell
end tell`, tmuxSession)
	_, err := c.Runner.Run(script)
	return err
}
