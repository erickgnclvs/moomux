// Package terminal detects the running terminal and opens tmux sessions in it.
package terminal

import "os"

// TerminalOpener opens a tmux session in the detected terminal. The returned
// hint is a non-empty, user-facing instruction when the opener couldn't
// actually attach a terminal for the caller (e.g. no supported terminal was
// detected) but still succeeded in the sense that there's nothing more it
// can do — callers should surface it, not treat it as failure.
type TerminalOpener interface {
	OpenSession(tmuxSession, title string) (hint string, err error)
}

// Detect returns the best TerminalOpener for the current environment by
// inspecting well-known environment variables.
func Detect() TerminalOpener {
	switch {
	case os.Getenv("TERM_PROGRAM") == "iTerm.app":
		return newITermClient()
	case os.Getenv("CMUX_SURFACE_ID") != "":
		return &windowOpener{binary: "cmux", args: cmuxArgs}
	case os.Getenv("KITTY_WINDOW_ID") != "":
		return &windowOpener{binary: "kitty", args: kittyArgs}
	case os.Getenv("WEZTERM_PANE") != "":
		return &windowOpener{binary: "wezterm", args: weztermArgs}
	case os.Getenv("TERM") == "alacritty":
		return &windowOpener{binary: "alacritty", args: alacrittyArgs}
	case os.Getenv("TERM_PROGRAM") == "Apple_Terminal":
		return &windowOpener{binary: "open", args: terminalAppArgs}
	case os.Getenv("TILIX_ID") != "":
		return &windowOpener{binary: "tilix", args: tilixArgs}
	case os.Getenv("KONSOLE_VERSION") != "":
		return &windowOpener{binary: "konsole", args: konsoleArgs}
	case os.Getenv("XTERM_VERSION") != "":
		return &windowOpener{binary: "xterm", args: xtermArgs}
	case os.Getenv("VTE_VERSION") != "":
		return &windowOpener{binary: "gnome-terminal", args: gnomeTerminalArgs}
	default:
		return &fallbackOpener{}
	}
}
