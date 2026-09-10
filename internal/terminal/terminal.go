// Package terminal detects the running terminal and opens tmux sessions in it.
package terminal

import (
	"os"
	"os/exec"
)

// TerminalOpener opens a tmux session in the detected terminal. The returned
// hint is a non-empty, user-facing instruction when the opener couldn't
// actually attach a terminal for the caller (e.g. no supported terminal was
// detected) but still succeeded in the sense that there's nothing more it
// can do — callers should surface it, not treat it as failure.
type TerminalOpener interface {
	OpenSession(tmuxSession, title string) (hint string, err error)
}

// TabReopener is an optional capability: implement it on a TerminalOpener
// whose terminal has an addressable tab/window concept and exposes a way to
// bring a specific one back to the front (currently iTerm2, via AppleScript;
// kitty, via `kitten @ focus-tab --match id:N`; and wezterm, via
// `wezterm cli activate-pane --pane-id N` over its mux server). Callers
// (see app.go's openTerminal) type-assert for this interface rather than
// calling it directly, so terminals without the capability are unaffected.
//
// OpenTab brings tabID back to the front instead of always creating a new
// tab; if tabID is empty or no longer exists, it falls back to opening a
// fresh tab/window and returns its id so the caller can remember it for
// next time. tabID is an opaque per-implementation handle, not necessarily
// a literal "tab id" — iTerm2's AppleScript tab objects have no id
// property, so itermClient uses the id of the tab's session instead (see
// its OpenTab doc comment). Future implementers should pick whatever
// stable handle their terminal actually exposes, not assume "tab id" means
// a tab-scoped identifier.
type TabReopener interface {
	OpenTab(tabID, tmuxSession, title string) (newTabID, hint string, err error)
}

// TabCloser is an optional capability, mirroring TabReopener: implement it
// on a TerminalOpener whose terminal can close a specific addressable tab
// (currently just iTerm2, via AppleScript). Callers type-assert for this
// interface rather than calling it directly, so terminals without the
// capability are unaffected — closing a session's tab is a best-effort
// nicety, not something every terminal needs to support.
//
// CloseTab closes tabID's tab if it still exists; closing an already-gone
// tab is not an error, mirroring TabReopener.OpenTab's "tab gone" handling.
type TabCloser interface {
	CloseTab(tabID string) error
}

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
		return &windowOpener{binary: "ghostty", args: ghosttyArgs}
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
