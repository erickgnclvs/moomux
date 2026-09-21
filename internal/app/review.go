package app

import "fmt"

// Capture returns the visible text of each session's active pane, keyed by
// session id — the session grid a front end draws, without it needing a tmux
// binary or moomux's tmux session names.
//
// One tmux invocation for the whole set (see tmux.CapturePanes); ids the
// core doesn't know, and sessions whose tmux is gone, are simply absent from
// the result rather than an error, because a caller polling this every few
// seconds must not read a failure as "the pane is empty".
func (a *App) Capture(ids []string) map[string]string {
	// Parking keeps the session's tmux name, so a name is not liveness —
	// and one dead name aborts the rest of the chained tmux sequence,
	// dropping every session after it onto the per-session fallback. One
	// fork to ask is what makes batching the other N worth it.
	live := a.Tmux.LiveSessions()
	names := make([]string, 0, len(ids))
	byName := make(map[string]string, len(ids))
	for _, id := range ids {
		s, ok := a.Store.Get(id)
		if !ok || s.TmuxSession == "" || !live[s.TmuxSession] {
			continue
		}
		if _, dup := byName[s.TmuxSession]; dup {
			continue
		}
		byName[s.TmuxSession] = id
		names = append(names, s.TmuxSession)
	}
	out := make(map[string]string, len(names))
	for name, screen := range a.Tmux.CapturePanes(names) {
		out[byName[name]] = screen
	}
	return out
}

// Review opens (or reuses) a window named "review" in the session's tmux
// session, running its diff against the base branch.
//
// A tmux window rather than a patch rendered by the front end: the output
// goes to a real tty, so git colours and pages it with the user's own pager
// — a configured delta is honoured, which is most of the argument for
// reviewing here at all — and the window is somewhere to run `git add -p`
// from afterwards. It also works from a client that isn't attached; the
// returned hint is that client's only signal.
func (a *App) Review(id string) (string, error) {
	s, found := a.Store.Get(id)
	if !found {
		return "", fmt.Errorf("unknown session %q", id)
	}
	proj, ok := a.project(s.Project)
	if !ok {
		return "", fmt.Errorf("unknown project %q", s.Project)
	}
	if proj.IsPlain() {
		return "", fmt.Errorf("%s is not a git project", s.Project)
	}
	// The session's own base is what its branch was actually cut from; the
	// project's is the fallback for a resumed branch that never recorded one.
	base := s.BaseBranch
	if base == "" {
		base = proj.BaseBranch
	}
	if base == "" {
		base = "main"
	}
	// A review window needs a live tmux session to be added to, and both
	// respawn-window and new-window fail on a parked one with nothing but an
	// exit status. Deliberately *not* EnsureTmux, unlike Attach: reviving a
	// session relaunches its agent, which is a surprising and expensive side
	// effect of asking to look at a diff. Say what's wrong instead and let
	// the caller open it.
	if s.TmuxSession == "" {
		return "", fmt.Errorf("session %q has no tmux session", id)
	}
	if live, err := a.Tmux.HasSession(s.TmuxSession); err != nil {
		return "", err
	} else if !live {
		return "", fmt.Errorf("%s is parked; open it before reviewing", s.Name)
	}
	if err := a.Tmux.ReviewWindow(s.TmuxSession, s.WorktreePath, ReviewScript(base)); err != nil {
		return "", fmt.Errorf("review window: %w", err)
	}
	return "Opened a review window in " + s.TmuxSession + ".", nil
}

// ReviewScript is the shell line a review window runs.
//
// `git diff --merge-base` is everything not yet on the base branch — commits
// since the merge base *and* uncommitted work — in one command. `origin/`
// first because a local base branch goes stale in a worktree checkout, then
// the local one, then a plain `HEAD` diff for a project with neither. The
// `git status` line is not decoration: untracked files are invisible to
// every diff, an agent's new files are usually untracked, and `--branch`
// guarantees at least one line of output so a clean worktree reads as
// "nothing to review" rather than as a window that failed to run anything.
//
// Which ref exists is asked with rev-parse, not inferred from a diff's exit
// status, because a paged diff exits 141 when the user quits the pager
// early: a `||` chain re-ran the same diff against the local base and then
// fell through to `git diff HEAD`, quietly showing uncommitted work only and
// calling it the review — on exactly the large diffs the feature is for.
//
// No --color and no `| less`: output goes straight to a tty, so git colours
// and pages it with the user's own pager. It ends in a shell so the window
// survives the pager.
func ReviewScript(base string) string {
	return "b=$(git rev-parse --verify --quiet " + shellQuote("origin/"+base) +
		" || git rev-parse --verify --quiet " + shellQuote(base) + ");" +
		` if [ -n "$b" ]; then git diff --merge-base "$b"; else git diff HEAD; fi;` +
		" git status --short --branch;" +
		` exec "${SHELL:-/bin/sh}"`
}
