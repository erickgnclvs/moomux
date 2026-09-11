// Package terminal detects the running terminal and opens tmux sessions in it.
package terminal

import (
	"os"
	"os/exec"
	"runtime"
)

// TerminalOpener opens a tmux session in the detected terminal. The returned
// hint is a non-empty, user-facing instruction when the opener couldn't
// actually attach a terminal for the caller (e.g. no supported terminal was
// detected) but still succeeded in the sense that there's nothing more it
// can do — callers should surface it, not treat it as failure.
type TerminalOpener interface {
	OpenSession(tmuxSession, title string) (hint string, err error)
}

// TabCloser is an optional capability, mirroring TabFinder: implement it on
// a TerminalOpener whose terminal can close a specific addressable tab. Callers type-assert for this
// interface rather than calling it directly, so terminals without the
// capability are unaffected — closing a session's tab is a best-effort
// nicety, not something every terminal needs to support.
//
// CloseTab closes tabID's tab if it still exists; closing an already-gone
// tab is not an error: tabs close independently of the tmux session they
// were attached to.
type TabCloser interface {
	CloseTab(tabID string) error
}

// TabFinder is an optional capability for a terminal that can identify a
// session's tab from the outside rather than from a handle it was given
// (currently just iTerm2, which joins tmux's attached-client tty to its own
// per-session tty). It exists for the close path: a caller that wants to
// close a session's tab has to learn which tab that is *before* tmux dies,
// because tmux is half the join, and CloseTab can only be called after.
//
// FindTab returns an empty id, and no error, when there's no such tab.
type TabFinder interface {
	FindTab(tmuxSession string) (tabID string, err error)
}

// Compile-time capability roster: which terminal implements which optional
// interface. Cheap insurance — these are all satisfied by method name, so a
// renamed or re-signatured method would otherwise just silently stop being
// detected by the type assertions in app.go and main.go.
var (
	_ TabCloser = (*itermClient)(nil)
	_ TabFinder = (*itermClient)(nil)

	_ TabCloser = (*kittyClient)(nil)
	_ TabFinder = (*kittyClient)(nil)

	_ TabCloser = (*weztermClient)(nil)
	_ TabFinder = (*weztermClient)(nil)
)

// Detect returns the best TerminalOpener for the current environment by
// inspecting well-known environment variables.
func Detect() TerminalOpener {
	switch {
	case os.Getenv("TERM_PROGRAM") == "iTerm.app":
		return newITermClient()
	// cmux is Ghostty-based and sets the same GHOSTTY_* vars as vanilla
	// Ghostty, so the bundle ID is the only thing that tells them apart.
	case os.Getenv("__CFBundleIdentifier") == "com.cmuxterm.app":
		return &windowOpener{binary: "cmux", args: cmuxArgs}
	case os.Getenv("KITTY_WINDOW_ID") != "":
		newWindow := &windowOpener{binary: "kitty", args: kittyArgs}
		// With socket remote control we can open a tab in the current OS
		// window instead of spawning a whole new kitty instance. Gated on
		// KITTY_LISTEN_ON: without a socket `kitten @` falls back to
		// tty-based control, which writes escape sequences into the same
		// terminal the TUI is drawing on.
		if os.Getenv("KITTY_LISTEN_ON") != "" {
			return newKittyClient(newWindow)
		}
		return newWindow
	case os.Getenv("TERM_PROGRAM") == "ghostty" || os.Getenv("GHOSTTY_RESOURCES_DIR") != "":
		return ghosttyOpener()
	case os.Getenv("WEZTERM_PANE") != "":
		// WEZTERM_PANE means a wezterm mux server is running, so `cli spawn`
		// can open a tab in the current window; fall back to a fresh
		// process if the server can't be reached (e.g. stale env in tmux).
		return newWeztermClient(&windowOpener{binary: "wezterm", args: weztermStartArgs})
	case os.Getenv("TERM") == "alacritty" || os.Getenv("ALACRITTY_WINDOW_ID") != "":
		newWindow := &windowOpener{binary: "alacritty", args: alacrittyArgs}
		// `alacritty msg create-window` opens a window in the running
		// instance over its IPC socket — much faster than booting a new
		// process. ALACRITTY_SOCKET is exported when the socket exists.
		if os.Getenv("ALACRITTY_SOCKET") != "" {
			return &remoteOpener{binary: "alacritty", args: alacrittyMsgArgs, fallback: newWindow}
		}
		return newWindow
	case os.Getenv("TERM_PROGRAM") == "Apple_Terminal":
		return &windowOpener{binary: "open", args: terminalAppArgs, manualAttach: true}
	case os.Getenv("TILIX_ID") != "":
		return &windowOpener{binary: "tilix", args: tilixArgs}
	case os.Getenv("KONSOLE_VERSION") != "":
		return &windowOpener{binary: "konsole", args: konsoleArgs}
	case os.Getenv("TERM") == "foot":
		return &windowOpener{binary: "foot", args: footArgs}
	case os.Getenv("XTERM_VERSION") != "":
		return &windowOpener{binary: "xterm", args: xtermArgs}
	case os.Getenv("GNOME_TERMINAL_SCREEN") != "" || os.Getenv("GNOME_TERMINAL_SERVICE") != "":
		return &windowOpener{binary: "gnome-terminal", args: gnomeTerminalArgs}
	case os.Getenv("VTE_VERSION") != "":
		// Some other VTE-based terminal (GNOME Console, Ptyxis, Xfce
		// Terminal, ...). They all set VTE_VERSION but don't necessarily
		// ship gnome-terminal, so only pick it when it's actually
		// installed; otherwise surface the attach hint instead of
		// exec-ing a missing binary.
		if _, err := exec.LookPath("gnome-terminal"); err == nil {
			return &windowOpener{binary: "gnome-terminal", args: gnomeTerminalArgs}
		}
		return fallback()
	case os.Getenv("WT_SESSION") != "":
		return &windowOpener{binary: "wt.exe", args: windowsTerminalArgs}
	default:
		return fallback()
	}
}

// fallback is used when no terminal emulator was identified by env vars. If
// moomux itself is running inside a tmux client (detected via $TMUX), the
// env vars Detect relies on are often gone by now — tmux only snapshots the
// environment once, at server start — so fall back to walking the process
// tree to find the terminal emulator actually hosting this pane.
func fallback() TerminalOpener {
	if os.Getenv("TMUX") != "" {
		if opener := detectFromProcessTree(); opener != nil {
			return opener
		}
	}
	return platformFallback()
}

// ghosttyOpener prefers Ghostty's AppleScript dictionary on macOS, which
// can open a tab in the current window; the `ghostty` binary can only open
// a whole new window, so it stays the fallback (and the only option on
// Linux, where there's no scripting bridge).
func ghosttyOpener() TerminalOpener {
	newWindow := &windowOpener{binary: "ghostty", args: ghosttyArgs}
	if runtime.GOOS == "darwin" {
		return newGhosttyClient(newWindow)
	}
	return newWindow
}
