package main

import (
	"fmt"
	"log/slog"

	"github.com/erickgnclvs/moomux/internal/app"
	"github.com/erickgnclvs/moomux/internal/browser"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/terminal"
	"github.com/erickgnclvs/moomux/internal/tui"
)

// terminalBackend is where every terminal moomux opens or closes comes
// from. It decorates a backend — the core in-process, or the core over the
// socket, identically — so that anything involving a terminal happens here
// rather than in the core. Every front end goes through it: the local TUI,
// `moomux ui -socket`, and the `moomux park` worker.
//
// The core owns the tmux session: that it exists, what it's called, that
// its agent is alive. EnsureTmux is exactly that much. Which emulator to
// launch and which of its tabs a session is in are facts about the machine
// a human is sitting at, and the core is not reliably that machine's
// process — a `moomux serve` core started by launchd has no TERM_PROGRAM
// and no $TMUX at all, so terminal.Detect() over there answers for the
// wrong process.
//
// There is one implementation because there was briefly two: the core kept
// its own copy for the in-process TUI while this served the socket TUI, and
// the same stale-handle bug had to be fixed in both.
//
// Nothing here remembers which tab a session is in. Each terminal finds its
// own (terminal.TabFinder), by joining tmux's attached-client list to what
// it can report about its tabs — tty for iTerm2 and wezterm,
// foreground-process pid for kitty. A remembered handle would survive
// neither a restart of this process nor of the terminal, and kitty tab ids
// and wezterm pane ids restart from zero, so a stale one names a stranger's
// tab.
type terminalBackend struct {
	tui.Backend
	term terminal.TerminalOpener
}

func newTerminalBackend(b tui.Backend) *terminalBackend {
	return &terminalBackend{Backend: b, term: terminal.Detect()}
}

// CreateSession opens the new session's terminal here, after the core has
// built it. req.OpenTerminal stays set on the way through: the core uses it
// to stamp LastOpened and to skip the manual-attach hint, it just doesn't
// act on it.
func (t *terminalBackend) CreateSession(req session.CreateRequest) (session.Session, string, error) {
	s, hint, err := t.Backend.CreateSession(req)
	if err != nil || !req.OpenTerminal {
		return s, hint, err
	}
	return s, app.JoinHints(hint, t.open(s)), nil
}

func (t *terminalBackend) OpenSession(id string) (string, error) {
	hooksHint, err := t.Backend.EnsureTmux(id)
	if err != nil {
		return "", err
	}
	s, ok := findSession(t.Backend.Sessions(), id)
	if !ok {
		// EnsureTmux just revived it, so the session is running — we
		// simply can't see its tmux name, and without that there is
		// nothing to attach a terminal to. Say so: the core has no
		// terminal to fall back to, and silently opening nothing is worse
		// than an error.
		return hooksHint, fmt.Errorf("session %q is running but isn't in the session list — can't tell which tmux session to open", id)
	}
	return app.JoinHints(hooksHint, t.open(s)), nil
}

// open opens s in this machine's terminal and returns the hint to show.
// Errors are folded into the hint rather than returned: the tmux session is
// up either way, so the user needs a way in, not a failure.
func (t *terminalBackend) open(s session.Session) string {
	if browser.Remote() {
		// This client is itself somebody's SSH window: the emulator that
		// could open a tab is a third machine. Same reasoning as the core
		// used to apply to itself — but now it's the process that actually
		// knows.
		return fmt.Sprintf("tmux attach -t %s", s.TmuxSession)
	}
	hint, err := t.term.OpenSession(s.TmuxSession, s.Name)
	if err != nil {
		slog.Error("open terminal failed", "id", s.ID, "err", err)
		return fmt.Sprintf("couldn't open a terminal (%v) — attach yourself: tmux attach -t %s", err, s.TmuxSession)
	}
	return hint
}

// KillTmux parks the session and closes the tab it was open in. The core
// does neither half of the tab work: the tab is on this machine.
//
// Order matters twice. The tab is resolved before the core kills tmux,
// because terminal.TabFinder joins on tmux's attached-client list — ask
// after the kill and there's nothing left to join. And it's closed after,
// since closing first can take the tmux client (and anything else in that
// tab) down with it before the kill is issued.
func (t *terminalBackend) KillTmux(id string) error {
	tabID := t.resolveTab(id)
	err := t.Backend.KillTmux(id)
	t.closeTab(id, tabID)
	return err
}

// DeleteSession closes the tab too — a deleted session's tab is attached to
// a tmux session that no longer exists, same as a parked one's. Same
// resolve-before, close-after ordering as KillTmux, for the same reasons.
func (t *terminalBackend) DeleteSession(id string) (string, error) {
	tabID := t.resolveTab(id)
	hint, err := t.Backend.DeleteSession(id)
	t.closeTab(id, tabID)
	return hint, err
}

// closeTab closes tabID, if there is one and the terminal can address tabs.
// Best-effort: a session that parked or deleted fine shouldn't report
// failure because a tab the user already closed wouldn't close.
func (t *terminalBackend) closeTab(id, tabID string) {
	closer, ok := t.term.(terminal.TabCloser)
	if !ok || tabID == "" {
		return
	}
	if err := closer.CloseTab(tabID); err != nil {
		slog.Warn("close terminal tab failed", "id", id, "tab_id", tabID, "err", err)
	}
}

// resolveTab finds the tab to close for a session, or "" when the terminal
// can't address tabs or has none for it. Must be called while the session's
// tmux is still alive — see KillTmux.
func (t *terminalBackend) resolveTab(id string) string {
	finder, ok := t.term.(terminal.TabFinder)
	if !ok {
		return ""
	}
	s, ok := findSession(t.Backend.Sessions(), id)
	if !ok {
		return ""
	}
	found, err := finder.FindTab(s.TmuxSession)
	if err != nil {
		slog.Debug("find terminal tab failed", "id", id, "err", err)
	}
	return found
}

func findSession(all []session.Session, id string) (session.Session, bool) {
	for _, s := range all {
		if s.ID == id {
			return s, true
		}
	}
	return session.Session{}, false
}
