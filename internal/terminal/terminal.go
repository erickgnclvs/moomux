// Package terminal detects the running terminal and opens tmux sessions in it.
package terminal

import "os"

// TerminalOpener opens a tmux session in the detected terminal.
type TerminalOpener interface {
	OpenSession(tmuxSession, title string) error
}

// Detect returns the best TerminalOpener for the current environment by
// inspecting well-known environment variables.
func Detect() TerminalOpener {
	switch {
	case os.Getenv("TERM_PROGRAM") == "iTerm.app":
		return newITermClient()
	case os.Getenv("KITTY_WINDOW_ID") != "":
		return &windowOpener{binary: "kitty", args: kittyArgs}
	case os.Getenv("WEZTERM_PANE") != "":
		return &windowOpener{binary: "wezterm", args: weztermArgs}
	case os.Getenv("TERM_PROGRAM") == "Apple_Terminal":
		return &windowOpener{binary: "open", args: terminalAppArgs}
	default:
		return &fallbackOpener{}
	}
}
