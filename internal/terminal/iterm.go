package terminal

import (
	"fmt"
	"os/exec"
)

type scriptRunner interface {
	Run(script string) (string, error)
}

type execScriptRunner struct{}

func (execScriptRunner) Run(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	return string(out), err
}

type itermClient struct {
	runner scriptRunner
}

func newITermClient() *itermClient {
	return &itermClient{runner: execScriptRunner{}}
}

func (c *itermClient) OpenSession(tmuxSession, title string) error {
	setName := ""
	// Emit an OSC 0 escape sequence before attaching so iTerm2 re-renders the
	// tab title immediately when the command runs, not just when the profile
	// title setting is next toggled.
	attachCmd := "tmux attach -t " + tmuxSession
	if title != "" {
		escaped := escapeAppleScript(title)
		setName = fmt.Sprintf("\n\t\t\tset name to \"%s\"", escaped)
		attachCmd = fmt.Sprintf("printf '\\033]0;%s\\007' && tmux attach -t %s", escaped, tmuxSession)
	}
	script := fmt.Sprintf(`
tell application "iTerm2"
	activate
	if (count of windows) = 0 then
		create window with default profile
	end if
	tell current window
		create tab with default profile
		tell current session of current tab%s
			write text "%s"
		end tell
	end tell
end tell`, setName, attachCmd)
	_, err := c.runner.Run(script)
	return err
}

func escapeAppleScript(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\\' || r == '"' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
