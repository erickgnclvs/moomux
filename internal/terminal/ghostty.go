package terminal

import (
	"fmt"
	"log/slog"
	"strings"
)

// ghosttyAppID is Ghostty's bundle identifier, used to address it by id so
// the scripts don't depend on the app's display name.
const ghosttyAppID = "com.mitchellh.ghostty"

// ghosttyClient implements TabReopener/TabCloser for Ghostty on macOS via
// its AppleScript dictionary (Ghostty 1.3+, `macos-applescript = true`,
// which is the default). Ghostty still has no CLI flag for opening a tab in
// an existing window (see ghosttyArgs), but the scripting suite exposes
// `new tab in front window` plus stable tab ids that can be selected and
// closed later — everything TabReopener/TabCloser need.
//
// Every path falls back to fallback (a fresh ghostty window) when the
// script fails, because AppleScript can be unavailable for reasons moomux
// can't see from here: `macos-applescript = false`, macOS automation
// permission not granted for moomux's host app, or an older Ghostty with no
// scripting dictionary. The no-window case rides the same fallback rather
// than an AppleScript `new window` branch — spawning the binary is what
// that used to do anyway.
//
// Tab titles are left alone: Ghostty's surface configuration has no title
// property, and the tab title follows the terminal title, which the core
// already keeps in step with agent state via tmux window names.
type ghosttyClient struct {
	runner   scriptRunner
	fallback TerminalOpener
}

func newGhosttyClient(fallback TerminalOpener) *ghosttyClient {
	return &ghosttyClient{runner: execScriptRunner{}, fallback: fallback}
}

func (c *ghosttyClient) OpenSession(tmuxSession, title string) (string, error) {
	_, hint, err := c.OpenTab("", tmuxSession, title)
	return hint, err
}

// OpenTab selects tabID's tab if Ghostty still has it, otherwise opens a
// fresh tab attaching tmuxSession and returns the id Ghostty assigned it.
func (c *ghosttyClient) OpenTab(tabID, tmuxSession, title string) (string, string, error) {
	if tabID != "" {
		if c.selectTab(tabID) {
			return tabID, "", nil
		}
		slog.Debug("ghostty: tab gone, opening a new one", "tab_id", tabID)
	}
	return c.createTab(tmuxSession, title)
}

// selectTab brings tabID's tab to the front, across all windows. Tabs are
// only addressable as elements of a window, hence the nested loop. A script
// error is treated the same as a missing tab — the caller's next move is to
// open a fresh tab either way.
func (c *ghosttyClient) selectTab(tabID string) bool {
	out, err := c.runner.Run(fmt.Sprintf(`
tell application id "%s"
	activate
	repeat with w in windows
		repeat with t in tabs of w
			if id of t is "%s" then
				select tab t
				activate window w
				return "found"
			end if
		end repeat
	end repeat
	return "notfound"
end tell`, ghosttyAppID, escapeAppleScript(tabID)))
	slog.Debug("ghostty: select tab result", "tab_id", tabID, "out", out, "err", err)
	return err == nil && strings.TrimSpace(out) == "found"
}

// createTab opens tmuxSession in a new tab of Ghostty's front window and
// returns that tab's id, falling back to a fresh ghostty window if the
// script fails (including when there is no front window to add a tab to).
func (c *ghosttyClient) createTab(tmuxSession, title string) (string, string, error) {
	// The command string is run through a shell, so the "=" exact-match
	// target has to be quoted — zsh's EQUALS expansion would otherwise
	// read a bare "=name" as a command-path lookup.
	out, err := c.runner.Run(fmt.Sprintf(`
tell application id "%s"
	activate
	return id of (new tab in front window with configuration {command:"tmux attach -t %s"})
end tell`, ghosttyAppID, escapeAppleScript(shellQuote("="+tmuxSession))))
	slog.Debug("ghostty: new tab result", "tmux_session", tmuxSession, "out", out, "err", err)
	if err != nil {
		hint, ferr := c.fallback.OpenSession(tmuxSession, title)
		return "", hint, ferr
	}
	return strings.TrimSpace(out), "", nil
}

// CloseTab closes tabID's tab if it still exists; a missing tab is a no-op,
// not an error. Deliberately doesn't `activate` first — closing a tab
// shouldn't steal focus the way selectTab should.
func (c *ghosttyClient) CloseTab(tabID string) error {
	out, err := c.runner.Run(fmt.Sprintf(`
tell application id "%s"
	repeat with w in windows
		repeat with t in tabs of w
			if id of t is "%s" then
				close tab t
				return "closed"
			end if
		end repeat
	end repeat
	return "notfound"
end tell`, ghosttyAppID, escapeAppleScript(tabID)))
	slog.Debug("ghostty: close tab result", "tab_id", tabID, "out", out, "err", err)
	return err
}
