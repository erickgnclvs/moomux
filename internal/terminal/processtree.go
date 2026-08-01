package terminal

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// attachedClientPID returns the pid of the tmux client attached to the
// session moomux itself is running in. That client process — the actual
// `tmux attach`/`tmux new-session` invocation — is a real child of whatever
// terminal window is currently displaying it. moomux's own pane is not: its
// parent is the tmux *server*, which survives detaches and reattaches and
// is typically reparented to init, so walking ancestry from moomux's own
// pid never reaches a terminal emulator.
var attachedClientPID = func() (int, error) {
	sessionOut, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return 0, err
	}
	session := strings.TrimSpace(string(sessionOut))

	clientsOut, err := exec.Command("tmux", "list-clients", "-t", session, "-F", "#{client_pid}").Output()
	if err != nil {
		return 0, err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(clientsOut)), "\n")
	return strconv.Atoi(line)
}

// processAncestors returns the base command name of pid's parent, then its
// parent, and so on up to (but not including) pid 1.
var processAncestors = func(pid int) []string {
	var names []string
	for i := 0; i < 32 && pid > 1; i++ {
		out, err := exec.Command("ps", "-o", "ppid=,comm=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			break
		}
		fields := strings.Fields(strings.TrimSpace(string(out)))
		if len(fields) < 2 {
			break
		}
		ppid, err := strconv.Atoi(fields[0])
		if err != nil {
			break
		}
		names = append(names, filepath.Base(fields[1]))
		pid = ppid
	}
	return names
}

type terminalMatch struct {
	names []string
	build func() TerminalOpener
}

var terminalMatches = []terminalMatch{
	{[]string{"iTerm2"}, func() TerminalOpener { return newITermClient() }},
	{[]string{"Terminal"}, func() TerminalOpener { return newTerminalAppClient() }},
	{[]string{"kitty"}, func() TerminalOpener { return &windowOpener{binary: "kitty", args: kittyArgs} }},
	{[]string{"ghostty"}, func() TerminalOpener { return &windowOpener{binary: "ghostty", args: ghosttyArgs} }},
	{[]string{"wezterm-gui", "wezterm"}, func() TerminalOpener { return &windowOpener{binary: "wezterm", args: weztermStartArgs} }},
	{[]string{"alacritty"}, func() TerminalOpener { return &windowOpener{binary: "alacritty", args: alacrittyArgs} }},
	{[]string{"konsole"}, func() TerminalOpener { return &windowOpener{binary: "konsole", args: konsoleArgs} }},
	{[]string{"gnome-terminal-server"}, func() TerminalOpener { return &windowOpener{binary: "gnome-terminal", args: gnomeTerminalArgs} }},
	{[]string{"xterm"}, func() TerminalOpener { return &windowOpener{binary: "xterm", args: xtermArgs} }},
}

// detectFromProcessTree finds the terminal emulator hosting the tmux client
// currently attached to moomux's session, returning nil if none is
// recognized (or the client/ancestry lookup itself fails, e.g. over ssh).
func detectFromProcessTree() TerminalOpener {
	pid, err := attachedClientPID()
	if err != nil {
		return nil
	}
	for _, name := range processAncestors(pid) {
		for _, m := range terminalMatches {
			for _, n := range m.names {
				if strings.EqualFold(name, n) {
					return m.build()
				}
			}
		}
	}
	return nil
}
