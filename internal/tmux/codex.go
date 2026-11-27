package tmux

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// Keep the original option name so existing Codex readers survive an upgrade.
const terminalStateOption = "@moomux_codex_state"

// SessionStates gets liveness and the latest terminal status together.
// Every pane contributes liveness. Only a pane with a running output pipe
// can contribute a status, and a session's other panes must not overwrite it.
func (c *Client) SessionStates() map[string]string {
	out, err := c.Runner.Run("list-panes", "-a", "-F", "#{session_name}\t#{pane_pipe_pid}\t#{?pane_pipe,#{"+terminalStateOption+"},}")
	if err != nil {
		if noServer(out) {
			return map[string]string{}
		}
		return nil
	}
	live := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		name, pipe, ok := strings.Cut(line, "\t")
		if !ok || live[name] != "" {
			continue
		}
		live[name] = ""
		pid, report, _ := strings.Cut(pipe, "\t")
		owner, state, ok := strings.Cut(report, ":")
		// Older tmux versions omit pane_pipe_pid. The reader also removes
		// its own report on EOF so replacing a pipe retires its status.
		if ok && (pid == "" || pid == owner) {
			live[name] = state
		}
	}
	return live
}

// ReportsTerminalState identifies agents that publish OSC 7501.
func ReportsTerminalState(agent string) bool { return agent == "codex" || agent == "opencode" }

// ObserveTerminal also covers live sessions created before the core upgraded.
func (c *Client) ObserveTerminal(session, agent string) {
	if !ReportsTerminalState(agent) || c.TerminalObserver == "" {
		return
	}
	out, err := c.runAgentPane(session, "display-message", "-p", "-t", agentTarget, "#{pane_id}")
	if err == nil {
		c.observeTerminalPane(strings.TrimSpace(out), agent)
	}
}

func (c *Client) observeTerminalPane(pane, agent string) {
	if c.TerminalObserver == "" {
		return
	}
	if !validPaneID(pane) {
		return
	}
	// pipe-pane -o would toggle OFF an existing pipe. Test pane_pipe in
	// the server's command queue so reopening the session preserves it.
	// Let tmux quote the path when it expands the shell command. This
	// also works when the server PATH points at an older moomux.
	if _, err := c.Runner.Run("set-option", "-p", "-t", pane, "@moomux_terminal_observer", c.TerminalObserver); err != nil {
		slog.Warn("could not configure terminal status observer", "pane", pane, "err", err)
		return
	}
	cmd := "pipe-pane -O -t " + pane + " 'exec #{q:@moomux_terminal_observer} hook " + agent + " terminal #{q:socket_path} #{pane_id}'"
	if _, err := c.Runner.Run("if-shell", "-F", "-t", pane, "#{pane_pipe}", "", cmd); err != nil {
		slog.Warn("could not observe terminal status", "pane", pane, "err", err)
	}
}

func (c *Client) launchAgent(pane, agent, cmd string) error {
	if cmd == "" {
		return nil
	}
	if ReportsTerminalState(agent) {
		c.observeTerminalPane(pane, agent)
	}
	_, err := c.Runner.Run("send-keys", "-t", pane, cmd, "Enter")
	return err
}

// ReportTerminalState publishes OSC 7501 from this pipe to its originating pane.
func ReportTerminalState(stream io.Reader, agent, socket, pane string) error {
	if !validPaneID(pane) {
		return fmt.Errorf("invalid tmux pane %q", pane)
	}
	owner := strconv.Itoa(os.Getpid()) + ":"
	// A replacement reader may have published its status before our EOF.
	defer func() {
		_, _ = ExecRunner().Run("-S", socket, "if-shell", "-F", "-t", pane,
			"#{m:"+owner+"*,#{"+terminalStateOption+"}}", "set-option -pu -t "+pane+" "+terminalStateOption)
	}()
	return readTerminalState(stream, agent, func(state watcher.State) error {
		_, err := ExecRunner().Run("-S", socket, "set-option", "-p", "-t", pane, terminalStateOption, owner+state.String())
		return err
	})
}

func validPaneID(pane string) bool {
	id, ok := strings.CutPrefix(pane, "%")
	if !ok {
		return false
	}
	_, err := strconv.ParseUint(id, 10, 64)
	return err == nil
}

// readTerminalState reads OSC 7501, the displayed lifecycle state. The
// terminal can split an escape sequence across arbitrary writes. Feed the
// parser incrementally and discard all ordinary text and tool output.
func readTerminalState(r io.Reader, agent string, publish func(watcher.State) error) error {
	parser := ansi.NewParser()
	parser.SetDataSize(4096)
	var last watcher.State
	var publishErr error
	parser.SetHandler(ansi.Handler{HandleOsc: func(command int, data []byte) {
		if command != 7501 || publishErr != nil {
			return
		}
		st := rootTerminalState(string(data), agent)
		if st == watcher.Unknown || (string(data) == "7501;state=clear" && last == watcher.Unknown) {
			return
		}
		if st != last {
			publishErr = publish(st)
			last = st
		}
	}})
	var buf [8192]byte
	for {
		n, err := r.Read(buf[:])
		for _, b := range buf[:n] {
			parser.Advance(b)
		}
		if publishErr != nil {
			return publishErr
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// OpenCode emits a root summary plus a record with an id for each tab.
// Only the summary describes the pane. Optional titles and messages are
// base64-encoded fields and need no decoding.
func rootTerminalState(record, agent string) watcher.State {
	if record == "7501;state=clear" {
		return watcher.Done
	}
	fields, ok := strings.CutPrefix(record, "7501;")
	if !ok {
		return watcher.Unknown
	}
	var app, state, kind string
	for field := range strings.SplitSeq(fields, ":") {
		key, value, _ := strings.Cut(field, "=")
		switch key {
		case "id":
			return watcher.Unknown
		case "kind":
			kind = value
		case "app":
			app = value
		case "state":
			state = value
		}
	}
	if app != agent {
		return watcher.Unknown
	}
	switch state {
	case "blocked":
		if agent == "codex" && kind != "permission" && kind != "question" && kind != "auth" {
			return watcher.Unknown
		}
		return watcher.NeedsInput
	case "working":
		return watcher.Working
	case "done", "error", "idle":
		return watcher.Done
	default:
		return watcher.Unknown
	}
}
