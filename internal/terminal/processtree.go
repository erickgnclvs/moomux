package terminal

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// attachedClientPIDs returns the pids of every tmux client attached to the
// session moomux itself is running in. Those client processes — the actual
// `tmux attach`/`tmux new-session` invocations — are real children of
// whatever terminal window is currently displaying them. moomux's own pane
// is not: its parent is the tmux *server*, which survives detaches and
// reattaches and is typically reparented to init, so walking ancestry from
// moomux's own pid never reaches a terminal emulator.
//
// All of them, not just the first: a session is commonly attached from a
// local emulator *and* from something with no window of its own (a mosh or
// ssh client), or from two different emulators at once. They come back
// most-recently-active first, which is the closest thing tmux can tell us
// to "the window the human is actually looking at" — tmux's own list order
// says nothing, and picking from it opened tabs in the emulator the user
// had walked away from.
var attachedClientPIDs = func() ([]int, error) {
	sessionOut, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return nil, err
	}
	session := strings.TrimSpace(string(sessionOut))

	clientsOut, err := exec.Command("tmux", "list-clients", "-t", session, "-F", "#{client_activity} #{client_pid}").Output()
	if err != nil {
		return nil, err
	}
	return clientPIDsByActivity(string(clientsOut)), nil
}

// clientPIDsByActivity parses "<activity> <pid>" lines into pids, most
// recently active first.
func clientPIDsByActivity(out string) []int {
	type client struct{ activity, pid int }
	var clients []client
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		activity, pid, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		a, aerr := strconv.Atoi(activity)
		p, perr := strconv.Atoi(pid)
		if aerr != nil || perr != nil {
			continue
		}
		clients = append(clients, client{a, p})
	}
	sort.SliceStable(clients, func(i, j int) bool { return clients[i].activity > clients[j].activity })
	pids := make([]int, len(clients))
	for i, c := range clients {
		pids[i] = c.pid
	}
	return pids
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
		ppid, name, ok := parsePS(string(out))
		if !ok {
			break
		}
		names = append(names, name)
		pid = ppid
	}
	return names
}

// matchesProcess reports whether an ancestor process name is the terminal
// called want, allowing the "<name>-<version>" spelling some apps use for
// their helper processes (iTermServer-3.7.1).
func matchesProcess(name, want string) bool {
	return strings.EqualFold(name, want) ||
		strings.HasPrefix(strings.ToLower(name), strings.ToLower(want)+"-")
}

// parsePS pulls the parent pid and base command name out of one line of
// `ps -o ppid=,comm=`. It splits once, not on every space: comm is a path
// that can contain them — iTerm2's pane host lives under "Application
// Support", and splitting on all whitespace yielded "Application", so
// attaching from iTerm2 was never recognized.
func parsePS(out string) (int, string, bool) {
	ppidStr, comm, ok := strings.Cut(strings.TrimSpace(out), " ")
	if !ok {
		return 0, "", false
	}
	ppid, err := strconv.Atoi(ppidStr)
	if err != nil {
		return 0, "", false
	}
	return ppid, filepath.Base(strings.TrimSpace(comm)), true
}

type terminalMatch struct {
	names []string
	build func() TerminalOpener
}

var terminalMatches = []terminalMatch{
	// iTerm2 3.7 doesn't host panes under "iTerm2": each one is a child of
	// a shared "iTermServer-<version>" process, so the bare app name never
	// appears in the ancestry (which is how attaching from iTerm2 used to
	// fall through to the .command fallback and open in whatever app macOS
	// has registered for it).
	{[]string{"iTerm2", "iTermServer"}, func() TerminalOpener { return newITermClient() }},
	{[]string{"Terminal"}, func() TerminalOpener { return &windowOpener{binary: "open", args: terminalAppArgs, manualAttach: true} }},
	{[]string{"kitty"}, func() TerminalOpener { return &windowOpener{binary: "kitty", args: kittyArgs} }},
	{[]string{"ghostty"}, ghosttyOpener},
	{[]string{"wezterm-gui", "wezterm"}, func() TerminalOpener { return &windowOpener{binary: "wezterm", args: weztermStartArgs} }},
	{[]string{"alacritty"}, func() TerminalOpener { return &windowOpener{binary: "alacritty", args: alacrittyArgs} }},
	{[]string{"konsole"}, func() TerminalOpener { return &windowOpener{binary: "konsole", args: konsoleArgs} }},
	{[]string{"gnome-terminal-server"}, func() TerminalOpener { return &windowOpener{binary: "gnome-terminal", args: gnomeTerminalArgs} }},
	{[]string{"xterm"}, func() TerminalOpener { return &windowOpener{binary: "xterm", args: xtermArgs} }},
}

// detectFromProcessTree finds the terminal emulator hosting one of the tmux
// clients currently attached to moomux's session, returning nil if none is
// recognized (or the client/ancestry lookup itself fails, e.g. over ssh).
func detectFromProcessTree() TerminalOpener {
	pids, err := attachedClientPIDs()
	if err != nil {
		return nil
	}
	for _, pid := range pids {
		for _, name := range processAncestors(pid) {
			for _, m := range terminalMatches {
				for _, n := range m.names {
					if matchesProcess(name, n) {
						return m.build()
					}
				}
			}
		}
	}
	return nil
}
