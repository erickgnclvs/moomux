package terminal

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

type scriptRunner interface {
	Run(script string) (string, error)
}

type execScriptRunner struct{}

func (execScriptRunner) Run(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	return string(out), err
}

type itermClient struct {
	runner scriptRunner
	// clients reports the terminal panes currently attached to a tmux
	// session; injectable so tests don't need a live tmux server.
	clients func(tmuxSession string) []tmuxClient
}

func newITermClient() *itermClient {
	return &itermClient{runner: execScriptRunner{}, clients: attachedClients}
}

func (c *itermClient) attached(tmuxSession string) []tmuxClient {
	if c.clients == nil {
		return attachedClients(tmuxSession)
	}
	return c.clients(tmuxSession)
}

// OpenSession brings the tab tmuxSession is already open in to the front,
// or opens a fresh one. Nothing is remembered between calls: the tab is
// found (see tabFor), which survives a restart of the front end or of
// iTerm2, and finds a tab another moomux front end opened.
func (c *itermClient) OpenSession(tmuxSession, title string) (string, error) {
	if id := c.tabFor(tmuxSession, true); id != "" {
		return "", nil
	}
	return "", c.createTab(tmuxSession, title)
}

// FindTab reports the id of the iTerm2 session in the tab attached to
// tmuxSession, without raising it — the answer CloseTab needs, and it has
// to be asked while tmux is still alive, since tmux is half the join. Empty
// when there's no such tab. See terminal.TabFinder.
func (c *itermClient) FindTab(tmuxSession string) (string, error) {
	return c.tabFor(tmuxSession, false), nil
}

// tabFor resolves tmuxSession to the id of the iTerm2 session holding it,
// raising that tab on the way when raise is set. The id matters only to
// CloseTab; OpenSession just needs to know whether a tab was found.
func (c *itermClient) tabFor(tmuxSession string, raise bool) string {
	for _, client := range c.attached(tmuxSession) {
		id, err := c.lookupByTTY(client.TTY, raise)
		if err != nil {
			// A broken osascript won't get better on the next tty.
			slog.Debug("iterm: tty lookup failed", "tty", client.TTY, "err", err)
			return ""
		}
		if id != "" {
			return id
		}
		slog.Debug("iterm: no iterm session on tty, trying the next", "tty", client.TTY)
	}
	return ""
}

// lookupByTTY returns the id of the iTerm2 session on tty, across all
// windows, bringing its tab to the front when raise is set. Returns an
// empty id (not an error) when no session is on that tty — the client
// attached to tmux may be some other terminal entirely, or a stale entry.
func (c *itermClient) lookupByTTY(tty string, raise bool) (string, error) {
	// Raising is three extra statements rather than a second script: the
	// walk is identical, and two copies of it would drift.
	sel := ""
	if raise {
		sel = "\n\t\t\t\t\tactivate\n\t\t\t\t\tselect t\n\t\t\t\t\tselect w"
	}
	script := fmt.Sprintf(`
tell application id "com.googlecode.iterm2"
	repeat with w in windows
		repeat with t in tabs of w
			repeat with sess in sessions of t
				if tty of sess is "%s" then%s
					return id of sess
				end if
			end repeat
		end repeat
	end repeat
	return "notfound"
end tell`, escapeAppleScript(tty), sel)
	out, err := c.runner.Run(script)
	slog.Debug("iterm: tty lookup result", "tty", tty, "raise", raise, "out", out, "err", err)
	id := strings.TrimSpace(out)
	if err != nil || id == "notfound" {
		return "", err
	}
	return id, nil
}

// createTab opens a new iTerm2 tab and attaches tmuxSession in it.
func (c *itermClient) createTab(tmuxSession, title string) error {
	setName := ""
	if title != "" {
		escaped := escapeAppleScript(title)
		setName = fmt.Sprintf("\n\t\t\tset name to \"%s\"", escaped)
	}
	// write text runs the line through the tab's interactive shell, so the
	// "=" exact-match target has to be single-quoted — zsh's EQUALS
	// expansion would read a bare "=name" as a command-path lookup.
	script := fmt.Sprintf(`
tell application id "com.googlecode.iterm2"
	activate
	if (count of windows) = 0 then
		create window with default profile
	end if
	tell current window
		set newTab to (create tab with default profile)
		tell current session of newTab%s
			write text "tmux attach -t '%s'"
		end tell
	end tell
end tell`, setName, escapeAppleScript("="+tmuxSession))
	slog.Debug("iterm: running applescript", "tmux_session", tmuxSession, "title", title, "set_name", setName != "", "script", script)
	out, err := c.runner.Run(script)
	slog.Debug("iterm: applescript result", "out", out, "err", err)
	return err
}

// CloseTab closes the tab holding session id tabID, across all windows. A
// no-op (not an error) if no such session exists — tabs close independently
// of the tmux session they were attached to, same as selectSession.
// Deliberately doesn't `activate` first, unlike selectSession/createTab: a
// close shouldn't steal focus the way bringing a tab to front should.
func (c *itermClient) CloseTab(tabID string) error {
	script := fmt.Sprintf(`
tell application id "com.googlecode.iterm2"
	repeat with w in windows
		repeat with t in tabs of w
			repeat with sess in sessions of t
				if id of sess is "%s" then
					close t
					return "closed"
				end if
			end repeat
		end repeat
	end repeat
	return "notfound"
end tell`, escapeAppleScript(tabID))
	out, err := c.runner.Run(script)
	slog.Debug("iterm: close tab result", "tab_id", tabID, "out", out, "err", err)
	return err
}

func escapeAppleScript(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\\' || r == '"' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
