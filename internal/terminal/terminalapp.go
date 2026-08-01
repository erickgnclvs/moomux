package terminal

import "fmt"

// terminalAppClient opens tabs in Terminal.app via AppleScript's `do script`,
// which — unlike the `open -a Terminal` mechanism windowOpener uses for
// every other opener — can pass a startup command, so the new window
// actually attaches to the tmux session instead of leaving the user to run
// the attach command by hand.
type terminalAppClient struct {
	runner scriptRunner
}

func newTerminalAppClient() *terminalAppClient {
	return &terminalAppClient{runner: execScriptRunner{}}
}

func (c *terminalAppClient) OpenSession(tmuxSession, title string) (string, error) {
	setTitle := ""
	if title != "" {
		setTitle = fmt.Sprintf("\n\tset custom title of win to \"%s\"", escapeAppleScript(title))
	}
	// do script runs the line through the window's interactive shell, same
	// as iTerm's write text, so the "=" exact-match target needs the same
	// single-quoting against zsh's EQUALS expansion (see iterm.go).
	script := fmt.Sprintf(`
tell application "Terminal"
	activate
	set win to do script "tmux attach -t '%s'"%s
end tell`, escapeAppleScript("="+tmuxSession), setTitle)
	_, err := c.runner.Run(script)
	return "", err
}
