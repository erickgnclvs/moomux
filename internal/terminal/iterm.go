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
	if title != "" {
		setName = fmt.Sprintf("\n\t\t\tset name to \"%s\"", escapeAppleScript(title))
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
			write text "tmux attach -t %s"
		end tell
	end tell
end tell`, setName, tmuxSession)
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

// argBuilder builds the full argument list given a title and tmux session name.
// Defined here so the package compiles while window.go is pending (Task 3).
type argBuilder func(title, tmuxSession string) []string

// windowOpener and fallbackOpener are stubbed here so the package compiles.
// Task 3 replaces windowOpener with the real implementation.
// Task 4 replaces fallbackOpener with the real implementation.
type windowOpener struct {
	binary string
	args   argBuilder
	exec   func(binary string, args ...string) error
}

func (w *windowOpener) OpenSession(tmuxSession, title string) error { return nil }

type fallbackOpener struct{}

func (f *fallbackOpener) OpenSession(tmuxSession, title string) error { return nil }

func kittyArgs(title, tmuxSession string) []string       { return nil }
func weztermArgs(title, tmuxSession string) []string     { return nil }
func terminalAppArgs(title, tmuxSession string) []string { return nil }
