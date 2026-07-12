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

func weztermArgs(title, tmuxSession string) []string {
	args := []string{"start"}
	if title != "" {
		args = append(args, "--class", title)
	}
	args = append(args, "--", "tmux", "attach", "-t", tmuxSession)
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

func gnomeTerminalArgs(title, tmuxSession string) []string {
	args := []string{}
	if title != "" {
		args = append(args, "--title", title)
	}
	args = append(args, "--", "tmux", "attach", "-t", tmuxSession)
	return args
}

func konsoleArgs(title, tmuxSession string) []string {
	args := []string{}
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
