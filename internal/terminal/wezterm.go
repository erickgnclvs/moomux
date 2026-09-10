package terminal

import (
	"encoding/json"
	"log/slog"
	"os/exec"
	"strconv"
)

// weztermClient drives the wezterm mux server's
// `wezterm cli` (see Detect's WEZTERM_PANE case, which only picks this path
// when the mux server is reachable). It's a self-contained type rather than
// an addition to remoteOpener so that other remoteOpener users (kitty,
// alacritty) don't falsely satisfy TabFinder/TabCloser — alacritty in
// particular has no addressable-tab concept at all.
type weztermClient struct {
	run      func(args ...string) (string, error)
	fallback TerminalOpener
	// clients reports the terminal panes attached to a tmux session;
	// injectable so tests don't need a live tmux server.
	clients func(tmuxSession string) []tmuxClient
}

func newWeztermClient(fallback TerminalOpener) *weztermClient {
	return &weztermClient{run: weztermRun, fallback: fallback, clients: attachedClients}
}

func (c *weztermClient) attached(tmuxSession string) []tmuxClient {
	if c.clients == nil {
		return attachedClients(tmuxSession)
	}
	return c.clients(tmuxSession)
}

func weztermRun(args ...string) (string, error) {
	out, err := exec.Command("wezterm", args...).CombinedOutput()
	return string(out), err
}

// OpenSession brings the pane tmuxSession is already open in to the front,
// or spawns a fresh one. Nothing is remembered between calls: the pane is
// found (see findPane), which survives a restart of the front end or of
// the mux server, whose pane ids restart from 0.
func (c *weztermClient) OpenSession(tmuxSession, title string) (string, error) {
	if id := c.findPane(tmuxSession); id != "" {
		if _, err := c.run("cli", "activate-pane", "--pane-id", id); err == nil {
			return "", nil
		}
		slog.Debug("wezterm: found pane would not activate, opening a new one", "tab_id", id)
	}
	// "=" pins tmux's -t to an exact session-name match; a bare name falls
	// back to prefix matching and can attach to the wrong session.
	if _, err := c.run("cli", "spawn", "--", "tmux", "attach", "-t", "="+tmuxSession); err != nil {
		slog.Debug("wezterm: spawn failed, falling back", "err", err)
		return c.fallback.OpenSession(tmuxSession, title)
	}
	return "", nil
}

// FindTab reports the id of the wezterm pane attached to tmuxSession
// without activating it — see terminal.TabFinder.
func (c *weztermClient) FindTab(tmuxSession string) (string, error) {
	return c.findPane(tmuxSession), nil
}

// CloseTab kills tabID's pane. An already-gone pane makes wezterm exit
// non-zero, which is not a failure worth reporting.
func (c *weztermClient) CloseTab(tabID string) error {
	if _, err := c.run("cli", "kill-pane", "--pane-id", tabID); err != nil {
		slog.Debug("wezterm: kill-pane failed (pane likely already gone)", "tab_id", tabID, "err", err)
	}
	return nil
}

// findPane returns the id of the wezterm pane holding a tmux client
// attached to tmuxSession, or "" if there isn't one. The join is on tty:
// `wezterm cli list` reports a tty_name per pane, which is what tmux's
// #{client_tty} names. A wezterm too old to report tty_name decodes as
// empty and matches nothing, which degrades to opening a fresh pane —
// exactly the behaviour before any of this.
func (c *weztermClient) findPane(tmuxSession string) string {
	clients := c.attached(tmuxSession)
	if len(clients) == 0 {
		return ""
	}
	out, err := c.run("cli", "list", "--format", "json")
	if err != nil {
		slog.Debug("wezterm: cli list failed", "err", err)
		return ""
	}
	var panes []struct {
		PaneID  int    `json:"pane_id"`
		TTYName string `json:"tty_name"`
	}
	if err := json.Unmarshal([]byte(out), &panes); err != nil {
		slog.Debug("wezterm: could not parse cli list output", "err", err)
		return ""
	}
	for _, client := range clients {
		for _, p := range panes {
			if p.TTYName != "" && p.TTYName == client.TTY {
				return strconv.Itoa(p.PaneID)
			}
		}
	}
	return ""
}
