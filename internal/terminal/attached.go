package terminal

import (
	"log/slog"
	"os/exec"
	"strings"
)

// tmuxClient is one terminal pane attached to a tmux session: the tty tmux
// is talking to, and the pid of the tmux client process running in it.
// Terminals identify their own tabs by one or the other — iTerm2 and
// wezterm expose a tty per session/pane, kitty exposes the pids of a
// window's foreground processes — so both are collected in the one call.
type tmuxClient struct {
	TTY string
	PID string
}

// attachedClients returns the terminal panes currently attached to
// tmuxSession, in tmux's own client order. Empty when nothing is attached,
// or when tmux can't be reached — both mean "no tab to jump back to", which
// callers handle by opening a fresh one.
//
// This is the stateless half of "which tab is this session already open
// in": tmux knows who's attached, and a terminal that can map a tty or a
// process back to one of its tabs needs no remembered handle.
func attachedClients(tmuxSession string) []tmuxClient {
	// "=" pins -t to an exact session-name match, same as everywhere else
	// a session name reaches tmux.
	//
	// ponytail: attached more than once (rare), the first client tmux lists
	// wins. Sort on #{client_activity} if raising the least-recently-used
	// tab ever actually bites.
	out, err := exec.Command("tmux", "list-clients",
		"-t", "="+tmuxSession,
		"-F", clientFormat,
	).Output()
	if err != nil {
		slog.Debug("tmux list-clients failed", "tmux_session", tmuxSession, "err", err)
		return nil
	}
	return parseClients(string(out))
}

// clientFormat and parseClients are a pair: the tmux -F template and the
// reader for what it prints. They're separate from the exec above, and
// tested together, because getting either wrong fails *silently* — a typo
// yields no matches, every terminal opens a fresh tab, and that is
// indistinguishable from there being no tab to find. Nothing errors.
const clientFormat = "#{client_tty}\t#{client_pid}"

func parseClients(out string) []tmuxClient {
	var clients []tmuxClient
	for _, line := range strings.Split(out, "\n") {
		tty, pid, _ := strings.Cut(strings.TrimSpace(line), "\t")
		if tty == "" {
			continue
		}
		clients = append(clients, tmuxClient{TTY: tty, PID: strings.TrimSpace(pid)})
	}
	return clients
}
