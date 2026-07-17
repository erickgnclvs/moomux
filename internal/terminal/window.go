package terminal

import "os/exec"

// argBuilder builds the full argument list given a title and tmux session name.
type argBuilder func(title, tmuxSession string) []string

type windowOpener struct {
	binary string
	args   argBuilder
	exec   func(binary string, args ...string) error
}

func (w *windowOpener) OpenSession(tmuxSession, title string) (string, error) {
	args := w.args(title, tmuxSession)
	if w.exec != nil {
		return "", w.exec(w.binary, args...)
	}
	return "", exec.Command(w.binary, args...).Start()
}

func kittyArgs(title, tmuxSession string) []string {
	args := []string{}
	if title != "" {
		args = append(args, "--title", title)
	}
	args = append(args, "--", "tmux", "attach", "-t", tmuxSession)
	return args
}

// ghosttyArgs invokes the ghostty binary directly. Ghostty has no CLI flag
// to open a tab in an existing window
// (https://github.com/ghostty-org/ghostty/issues/12136), and routing
// through `open -a`/`open -na` proved unreliable in practice (either
// silently dropped the launch args on an already-running instance, or forced
// a new window anyway) — this always opens a new window, which is the
// tradeoff until Ghostty ships a native new-tab CLI flag.
func ghosttyArgs(title, tmuxSession string) []string {
	args := []string{}
	if title != "" {
		args = append(args, "--title="+title)
	}
	args = append(args, "-e", "tmux", "attach", "-t", tmuxSession)
	return args
}

// weztermArgs opens the session as a new tab in the current wezterm window
// via the mux server (`wezterm cli spawn`). The tab title follows the tmux
// window name through set-titles, so no title flag is needed here.
func weztermArgs(title, tmuxSession string) []string {
	return []string{"cli", "spawn", "--", "tmux", "attach", "-t", tmuxSession}
}

// weztermStartArgs launches a fresh wezterm process (new window); used as
// the fallback when the mux server can't be reached.
func weztermStartArgs(title, tmuxSession string) []string {
	return []string{"start", "--", "tmux", "attach", "-t", tmuxSession}
}

// kittyTabArgs opens a new tab in the current kitty OS window via socket
// remote control (`kitten @ launch`). Requires allow_remote_control and
// listen_on in kitty.conf — Detect only picks this when KITTY_LISTEN_ON is
// set, and remoteOpener falls back to a new kitty instance on failure.
func kittyTabArgs(title, tmuxSession string) []string {
	args := []string{"@", "launch", "--type=tab"}
	if title != "" {
		args = append(args, "--tab-title="+title)
	}
	args = append(args, "tmux", "attach", "-t", tmuxSession)
	return args
}

// alacrittyMsgArgs opens a new window in the running Alacritty instance over
// its IPC socket. Alacritty has no tabs, but reusing the instance avoids
// booting a whole new process per session.
func alacrittyMsgArgs(title, tmuxSession string) []string {
	args := []string{"msg", "create-window"}
	if title != "" {
		args = append(args, "-T", title)
	}
	args = append(args, "-e", "tmux", "attach", "-t", tmuxSession)
	return args
}

func alacrittyArgs(title, tmuxSession string) []string {
	args := []string{}
	if title != "" {
		args = append(args, "--title", title)
	}
	args = append(args, "-e", "tmux", "attach", "-t", tmuxSession)
	return args
}

// terminalAppArgs opens Terminal.app via `open`. It cannot pass a startup
// command through this mechanism, so the new window will not auto-attach to
// the tmux session. Users will need to run the attach command manually.
func terminalAppArgs(title, tmuxSession string) []string {
	return []string{"-a", "Terminal"}
}

func windowsTerminalArgs(title, tmuxSession string) []string {
	args := []string{"new-tab"}
	if title != "" {
		args = append(args, "--title", title)
	}
	args = append(args, "--", "tmux", "attach", "-t", tmuxSession)
	return args
}

// gnomeTerminalArgs opens the session as a new tab in the last-opened
// GNOME Terminal window (--tab); if no window exists yet the server creates
// one.
func gnomeTerminalArgs(title, tmuxSession string) []string {
	args := []string{"--tab"}
	if title != "" {
		args = append(args, "--title", title)
	}
	args = append(args, "--", "tmux", "attach", "-t", tmuxSession)
	return args
}

// konsoleArgs opens the session as a new tab in an existing Konsole window
// (--new-tab); Konsole opens a new window when none exists.
func konsoleArgs(title, tmuxSession string) []string {
	args := []string{"--new-tab"}
	if title != "" {
		args = append(args, "--title", title)
	}
	args = append(args, "-e", "tmux", "attach", "-t", tmuxSession)
	return args
}

func xtermArgs(title, tmuxSession string) []string {
	args := []string{}
	if title != "" {
		args = append(args, "-title", title)
	}
	args = append(args, "-e", "tmux", "attach", "-t", tmuxSession)
	return args
}

func tilixArgs(title, tmuxSession string) []string {
	return []string{"-e", "tmux", "attach", "-t", tmuxSession}
}

// footArgs opens a new foot window; foot treats all trailing non-option
// arguments as the command to run.
func footArgs(title, tmuxSession string) []string {
	args := []string{}
	if title != "" {
		args = append(args, "--title="+title)
	}
	args = append(args, "tmux", "attach", "-t", tmuxSession)
	return args
}

// cmuxArgs opens a new cmux workspace and sends the tmux attach command to
// it, mirroring how the other multiplexer-aware terminals (kitty, wezterm)
// launch straight into the session.
func cmuxArgs(title, tmuxSession string) []string {
	args := []string{"new-workspace", "--focus", "true"}
	if title != "" {
		args = append(args, "--name", title)
	}
	args = append(args, "--command", "tmux attach -t "+tmuxSession)
	return args
}
