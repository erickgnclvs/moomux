package terminal

import (
	"encoding/json"
	"log/slog"
	"os/exec"
	"strconv"
)

// kittyRunner runs a `kitten @ ...` remote-control call and returns its
// stdout, mirroring scriptRunner's shape in iterm.go.
type kittyRunner interface {
	Run(args ...string) (string, error)
}

type execKittyRunner struct{}

func (execKittyRunner) Run(args ...string) (string, error) {
	out, err := exec.Command("kitten", args...).CombinedOutput()
	return string(out), err
}

// kittyClient drives kitty over its remote-control socket. Detect() only returns this when KITTY_LISTEN_ON is set; without a
// socket, `kitten @` falls back to tty-based control and fallback is used
// instead.
type kittyClient struct {
	runner   kittyRunner
	fallback TerminalOpener
	// clients reports the terminal panes attached to a tmux session;
	// injectable so tests don't need a live tmux server.
	clients func(tmuxSession string) []tmuxClient
}

func newKittyClient(fallback TerminalOpener) *kittyClient {
	return &kittyClient{runner: execKittyRunner{}, fallback: fallback, clients: attachedClients}
}

func (c *kittyClient) attached(tmuxSession string) []tmuxClient {
	if c.clients == nil {
		return attachedClients(tmuxSession)
	}
	return c.clients(tmuxSession)
}

// OpenSession focuses the tab tmuxSession is already open in, or opens a
// fresh one. Nothing is remembered between calls: the tab is found (see
// findTab), which survives a restart of the front end or of kitty — kitty
// tab ids restart from 1 with the process, so a remembered one is actively
// dangerous — and finds a tab another moomux front end opened.
func (c *kittyClient) OpenSession(tmuxSession, title string) (string, error) {
	if id := c.findTab(tmuxSession); id != "" {
		if c.focusTab(id) {
			return "", nil
		}
		slog.Debug("kitty: found tab would not focus, opening a new one", "tab_id", id)
	}
	return c.createTab(tmuxSession, title)
}

// FindTab reports the id of the kitty tab attached to tmuxSession without
// focusing it — see terminal.TabFinder, and findTab for how the match is
// made.
func (c *kittyClient) FindTab(tmuxSession string) (string, error) {
	return c.findTab(tmuxSession), nil
}

// CloseTab closes tabID's tab. kitty exits non-zero when the tab is already
// gone, which is not a failure worth reporting — same as focusTab.
func (c *kittyClient) CloseTab(tabID string) error {
	if _, err := c.runner.Run("@", "close-tab", "--match", "id:"+tabID); err != nil {
		slog.Debug("kitty: close-tab failed (tab likely already gone)", "tab_id", tabID, "err", err)
	}
	return nil
}

// findTab returns the id of the kitty tab holding a tmux client attached to
// tmuxSession, or "" if there isn't one.
//
// The join is on process id, not tty: kitty's `@ ls` reports the foreground
// processes of every window (with pids), but no tty, and its `--match`
// vocabulary has no tty either. tmux's #{client_pid} is exactly one of
// those foreground processes — it's the `tmux attach` running in the tab.
func (c *kittyClient) findTab(tmuxSession string) string {
	clients := c.attached(tmuxSession)
	if len(clients) == 0 {
		return ""
	}
	out, err := c.runner.Run("@", "ls")
	if err != nil {
		slog.Debug("kitty: ls failed", "err", err)
		return ""
	}
	var osWindows []struct {
		Tabs []struct {
			ID      int `json:"id"`
			Windows []struct {
				ForegroundProcesses []struct {
					PID int `json:"pid"`
				} `json:"foreground_processes"`
			} `json:"windows"`
		} `json:"tabs"`
	}
	if err := json.Unmarshal([]byte(out), &osWindows); err != nil {
		slog.Debug("kitty: could not parse ls output", "err", err)
		return ""
	}
	for _, client := range clients {
		for _, osw := range osWindows {
			for _, tab := range osw.Tabs {
				for _, w := range tab.Windows {
					for _, fg := range w.ForegroundProcesses {
						if strconv.Itoa(fg.PID) == client.PID {
							return strconv.Itoa(tab.ID)
						}
					}
				}
			}
		}
	}
	return ""
}

// focusTab brings tabID's tab to the front. kitty exits non-zero both when
// the tab is gone and on a genuine remote-control failure; either way the
// caller's best move is the same — open a fresh tab — so both cases are
// treated as "not found" here rather than needing to be told apart.
func (c *kittyClient) focusTab(tabID string) bool {
	_, err := c.runner.Run("@", "focus-tab", "--match", "id:"+tabID)
	return err == nil
}

// createTab opens tmuxSession in a new kitty tab. It doesn't report which
// tab: nothing stores one, and finding it again is findTab's job — which
// is also one fewer `kitten @ ls` on every open.
func (c *kittyClient) createTab(tmuxSession, title string) (string, error) {
	if _, err := c.runner.Run(kittyTabArgs(title, "="+tmuxSession)...); err != nil {
		slog.Debug("kitty: launch failed, falling back", "err", err)
		return c.fallback.OpenSession(tmuxSession, title)
	}
	return "", nil
}
