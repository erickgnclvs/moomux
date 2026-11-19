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

const codexStateOption = "@moomux_codex_state"

// SessionStates gets liveness and the last Codex terminal status together.
// Every pane contributes liveness. Only a pane with a running output pipe
// can contribute a status, and a session's other panes must not overwrite it.
func (c *Client) SessionStates() map[string]string {
	// A core started by launchd has no UTF-8 locale. Without -u, tmux
	// replaces the tab delimiters with underscores and every session is lost.
	out, err := c.Runner.Run("-u", "list-panes", "-a", "-F", "#{session_name}\t#{pane_pipe_pid}\t#{?pane_pipe,#{"+codexStateOption+"},}")
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

// ObserveCodex also covers a live session created before the core upgraded.
func (c *Client) ObserveCodex(session string) {
	out, err := c.runAgentPane(session, "display-message", "-p", "-t", agentTarget, "#{pane_id}")
	if err == nil {
		c.observeCodexPane(strings.TrimSpace(out))
	}
}

func (c *Client) observeCodexPane(pane string) {
	if c.CodexObserver == "" {
		return
	}
	if !validPaneID(pane) {
		return
	}
	// pipe-pane -o would toggle OFF an existing pipe. Test pane_pipe in
	// the server's command queue so reopening the session preserves it.
	// Let tmux quote the path when it expands the shell command. This
	// also works when the server PATH points at an older moomux.
	if _, err := c.Runner.Run("set-option", "-p", "-t", pane, "@moomux_codex_observer", c.CodexObserver); err != nil {
		slog.Warn("could not configure Codex status observer", "pane", pane, "err", err)
		return
	}
	cmd := "pipe-pane -O -t " + pane + " 'exec #{q:@moomux_codex_observer} hook codex terminal #{q:socket_path} #{pane_id}'"
	if _, err := c.Runner.Run("if-shell", "-F", "-t", pane, "#{pane_pipe}", "", cmd); err != nil {
		slog.Warn("could not observe Codex terminal status", "pane", pane, "err", err)
	}
}

func (c *Client) launchAgent(pane, cmd string) error {
	if cmd == "" {
		return nil
	}
	if agent, _, _ := strings.Cut(cmd, " "); agent == "codex" {
		c.observeCodexPane(pane)
	}
	_, err := c.Runner.Run("send-keys", "-t", pane, cmd, "Enter")
	return err
}

// ReportCodexTerminal publishes OSC 7501 from this pipe to its originating pane.
func ReportCodexTerminal(stream io.Reader, socket, pane string) error {
	if !validPaneID(pane) {
		return fmt.Errorf("invalid tmux pane %q", pane)
	}
	owner := strconv.Itoa(os.Getpid()) + ":"
	// A replacement reader may have published its status before our EOF.
	defer func() {
		_, _ = ExecRunner().Run("-S", socket, "if-shell", "-F", "-t", pane,
			"#{m:"+owner+"*,#{"+codexStateOption+"}}", "set-option -pu -t "+pane+" "+codexStateOption)
	}()
	return readCodexTerminal(stream, func(state watcher.State) error {
		_, err := ExecRunner().Run("-S", socket, "set-option", "-p", "-t", pane, codexStateOption, owner+state.String())
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

// readCodexTerminal reads OSC 7501, Codex's displayed lifecycle state. The
// terminal can split an escape sequence across arbitrary writes. Feed the
// parser incrementally and discard all ordinary text and tool output.
func readCodexTerminal(r io.Reader, publish func(watcher.State) error) error {
	parser := ansi.NewParser()
	parser.SetDataSize(128)
	var last watcher.State
	var publishErr error
	parser.SetHandler(ansi.Handler{HandleOsc: func(command int, data []byte) {
		if command != 7501 || publishErr != nil {
			return
		}
		var st watcher.State
		switch string(data) {
		case "7501;state=blocked:app=codex:kind=permission", "7501;state=blocked:app=codex:kind=question", "7501;state=blocked:app=codex:kind=auth":
			st = watcher.NeedsInput
		case "7501;state=working:app=codex":
			st = watcher.Working
		case "7501;state=done:app=codex", "7501;state=idle:app=codex", "7501;state=error:app=codex":
			st = watcher.Done
		case "7501;state=clear":
			if last == watcher.Unknown {
				return
			}
			st = watcher.Done
		default:
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
