package terminal

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
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

func (c *itermClient) OpenSession(tmuxSession, title string) (string, error) {
	setName := ""
	if title != "" {
		escaped := escapeAppleScript(title)
		setName = fmt.Sprintf("\n\t\t\tset name to \"%s\"", escaped)
	}
	// write text runs the line through the tab's interactive shell, so the
	// "=" exact-match target has to be single-quoted — zsh's EQUALS
	// expansion would read a bare "=name" as a command-path lookup.
	script := fmt.Sprintf(`
tell application "iTerm2"
	activate
	if (count of windows) = 0 then
		create window with default profile
	end if
	tell current window
		create tab with default profile
		tell current session of current tab%s
			write text "tmux attach -t '%s'"
		end tell
	end tell
end tell`, setName, escapeAppleScript(escapeShellSingleQuotes("="+tmuxSession)))
	slog.Debug("iterm: running applescript", "tmux_session", tmuxSession, "title", title, "set_name", setName != "", "script", script)
	out, err := c.runner.Run(script)
	slog.Debug("iterm: applescript result", "out", out, "err", err)
	return "", err
}

// escapeShellSingleQuotes applies the standard close-quote/escaped-quote/
// reopen-quote trick so a value can be safely interpolated inside a POSIX
// single-quoted shell string. Must run before escapeAppleScript, since the
// backslash it introduces then needs doubling for the AppleScript string
// container — tmuxSession is currently always moomux-<name>-<hash> (never
// attacker input, per sanitizeName), but write text/do script hand this
// straight to a shell, so an embedded "'" would otherwise break out of the
// quoted attach target.
func escapeShellSingleQuotes(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
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
