// Package sessionview turns the raw agent-activity stream into the state a
// front end actually renders.
//
// Everything here used to live in internal/tui, which meant every client —
// the TUI, the Mac app, anything else that speaks internal/ipc — had to
// re-derive it: join the watcher's path-keyed states against tmux liveness
// to get "parked", run its own git/PR status cache with its own staleness
// policy, scan the agent's own logs for a session's first prompt, and keep
// the tmux window titles in step with agent state. Two front ends attached
// at once did all of that twice, from two different code bases, and drifted
// (the served quip was picked from the raw state while the TUI's was picked
// from the effective one, so a parked session's cow said it was working).
//
// So the core does it once and hands out finished Views. Same shape whether
// it's read in-process (the local TUI) or over the socket — internal/ipc
// serializes Snapshot as-is.
package sessionview

import (
	"context"
	"hash/fnv"
	"math"
	"sync"
	"time"

	"github.com/erickgnclvs/moomux/internal/prompt"
	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// View is everything about one session that isn't stored on the session
// itself: what it's doing, and what that costs a subprocess to find out.
//
// State is the *effective* state — tmux liveness is already folded in, so a
// session whose tmux window is gone reads Parked here regardless of what
// its agent last wrote to its own log. Clients render this; they don't
// recompute it.
type View struct {
	ID    string        `json:"id"`
	State watcher.State `json:"state"`
	Label string        `json:"label"`
	Quip  string        `json:"quip"`
	// Prompt is the session's first prompt: the one captured at creation
	// time, or — for a session moomux didn't start, or one created before
	// that was captured — the one recovered from the agent's own logs.
	Prompt string `json:"prompt,omitempty"`
	// GitOK is false when the worktree's status couldn't be determined (not
	// a git repo, or simply not checked yet), in which case Dirty and
	// Unpushed are meaningless.
	GitOK    bool `json:"git_ok,omitempty"`
	Dirty    bool `json:"dirty,omitempty"`
	Unpushed bool `json:"unpushed,omitempty"`
	// PR is the attached pull request's merge/CI status, nil when the
	// session has no PR or it couldn't be looked up.
	PR *prstatus.Info `json:"pr,omitempty"`
}

// Snapshot is the session list and the full set of views, as of PollTime.
// Absolute state, never a delta — a client that misses one loses nothing.
//
// Sessions is in display order, so a client renders the slice as it comes
// and looks up Views by id. Order is served rather than derived because it
// was only half the core's to begin with: Sessions() applied the manual
// order or the recent-first sort, but the "sessions with a live tmux window
// float to the top" tiebreak was applied per client, off each client's own
// liveness map — so two front ends could list the same project in different
// orders. Clients filter (by project, by archived) and render; they don't
// sort. Streaming it also takes the session list off the render path: it
// used to be a store re-read, or a whole socket round trip, on every single
// keystroke — once per Update and again per View.
//
// Err is the underlying watcher's scan error, as a string: it crosses the
// socket, and an error value doesn't survive JSON.
type Snapshot struct {
	Sessions []session.Session `json:"sessions"`
	Views    map[string]View   `json:"views"`
	PollTime time.Time         `json:"poll_time"`
	Err      string            `json:"err,omitempty"`
}

// Source is a stream of Snapshots. Implemented by Watcher (the real thing,
// in-process) and by ipc.Client (the same stream, over a socket), so the TUI
// can't tell which end of the wire it's on.
type Source interface {
	Run(ctx context.Context, out chan<- Snapshot)
	// Nudge asks for a snapshot now rather than at the next tick, for the
	// moments where waiting a whole interval is visible: a session just
	// created, or just parked. Cheap and idempotent — a Nudge with nothing
	// to report just re-sends what the last tick would have.
	Nudge()
}

// Core is the slice of the orchestration core a Watcher needs. *app.App
// satisfies it.
type Core interface {
	Sessions() []session.Session
	TmuxAliveAll() map[string]bool
	WorktreeStatus(id string) (dirty, unpushed, ok bool)
	PRStatus(id string) (prstatus.Info, bool)
	SetSessionStatusTitle(id string, st watcher.State) error
}

// DefaultInterval is how often a Watcher re-checks tmux liveness and
// re-emits. The raw watcher is event-driven, but tmux windows die without
// producing an event, so this is the floor on noticing a parked session.
const DefaultInterval = 2 * time.Second

// Watcher is the real Source: it runs a raw watcher.Watcher, joins its
// output with tmux liveness, and maintains the caches for everything that
// costs a subprocess (git status, `gh pr view`, agent-log prompt scans).
//
// One goroutine owns all of that state — the run loop. Background fetches
// report back through a channel rather than writing the caches themselves,
// so there are no locks here and no chance of a client observing a half
// -applied refresh.
type Watcher struct {
	Core Core
	// Raw is the agent-activity watcher whose path-keyed states get joined
	// with tmux liveness here.
	Raw watcher.Watcher
	// Home is the user's home directory, where the agents keep the logs a
	// first prompt is recovered from. Empty disables prompt recovery.
	Home string
	// Interval defaults to DefaultInterval.
	Interval time.Duration

	nudgeOnce sync.Once
	nudgeCh   chan struct{}
	// titleCh serializes tmux window renames onto one goroutine. A goroutine
	// per emit could deliver two renames out of order (Working then
	// NeedsInput arriving reversed), and w.titles already records the newer
	// state — so the window would keep the older title for good.
	titleCh chan map[string]watcher.State

	// Everything below is owned by the run loop.
	states  map[string]watcher.State // by worktree path, cumulative; see Run
	alive   map[string]bool          // by session id
	git     map[string]gitEntry      // by session id
	pr      map[string]prEntry       // by session id
	prompts map[string]promptEntry   // by session id
	titles  map[string]watcher.State // last state pushed as each session's tmux window title
	pending map[fetchKey]bool        // fetches in flight, so a slow one isn't re-issued every tick
	lastErr string
	// sessions is the list the last build() read, reused by schedule() so
	// one pass costs one Sessions() call rather than two.
	sessions []session.Session
}

type gitEntry struct {
	dirty, unpushed, ok bool
	checkedAt           time.Time
}

type prEntry struct {
	info      prstatus.Info
	ok        bool
	checkedAt time.Time
}

type promptEntry struct {
	text string
	// agent the text was recovered from. A first prompt is read out of that
	// agent's own logs, so switching a session's agent invalidates it —
	// without this, a session moved from claude to codex kept showing the
	// claude-log prompt for the life of the process.
	agent     string
	checkedAt time.Time
}

type fetchKind int

const (
	fetchGit fetchKind = iota
	fetchPR
	fetchPrompt
)

type fetchKey struct {
	kind fetchKind
	id   string
}

// result is one completed background fetch, applied by the run loop.
type result struct {
	key    fetchKey
	git    gitEntry
	pr     prEntry
	prompt promptEntry
}

func (w *Watcher) nudger() chan struct{} {
	w.nudgeOnce.Do(func() { w.nudgeCh = make(chan struct{}, 1) })
	return w.nudgeCh
}

// Nudge implements Source.
func (w *Watcher) Nudge() {
	select {
	case w.nudger() <- struct{}{}:
	default: // one pending nudge is as good as ten
	}
}

// Run streams snapshots until ctx is cancelled.
//
// Raw snapshots are merged into a cumulative map rather than replacing it:
// watcher.MultiWatcher emits one Snapshot per sub-watcher, each covering
// only its own agent's paths, so replacing wholesale would wipe every other
// agent's entries on every tick.
func (w *Watcher) Run(ctx context.Context, out chan<- Snapshot) {
	w.states = map[string]watcher.State{}
	w.alive = map[string]bool{}
	w.git = map[string]gitEntry{}
	w.pr = map[string]prEntry{}
	w.prompts = map[string]promptEntry{}
	w.titles = map[string]watcher.State{}
	w.pending = map[fetchKey]bool{}

	interval := w.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}

	raw := make(chan watcher.Snapshot, 8)
	if w.Raw != nil {
		go w.Raw.Run(ctx, raw)
	}
	results := make(chan result, 32)
	w.titleCh = make(chan map[string]watcher.State, 8)
	go w.pushTitles(ctx)

	tick := time.NewTicker(interval)
	defer tick.Stop()
	nudge := w.nudger()

	// A raw snapshot can arrive far more often than interval (the darwin
	// watchers are fsevents-driven), and re-listing tmux sessions on each
	// one would be a subprocess per file write. Liveness is refreshed on
	// the ticker and on an explicit nudge instead.
	w.refreshAlive()
	w.emit(ctx, out)
	// Start the first status sweep now rather than at the first tick: every
	// session is "never checked" at this point, and waiting a whole interval
	// to begin means a front end renders its first frames with no git or PR
	// status at all.
	w.schedule(ctx, results)

	for {
		select {
		case <-ctx.Done():
			return
		case snap := <-raw:
			for path, st := range snap.States {
				w.states[path] = st
			}
			w.lastErr = ""
			if snap.Err != nil {
				w.lastErr = snap.Err.Error()
			}
			w.emit(ctx, out)
		case <-tick.C:
			w.refreshAlive()
			w.emit(ctx, out)
		case <-nudge:
			w.refreshAlive()
			w.emit(ctx, out)
		case r := <-results:
			w.apply(r)
			// Drain whatever else landed in the same instant, so a burst of
			// completed fetches costs one snapshot rather than one each.
		drain:
			for {
				select {
				case r := <-results:
					w.apply(r)
				default:
					break drain
				}
			}
			w.emit(ctx, out)
		}
		w.schedule(ctx, results)
	}
}

// pushTitles renames tmux windows in the order emit produced them, one at a
// time. Renaming shells out, so it can't run on the run loop.
func (w *Watcher) pushTitles(ctx context.Context) {
	for {
		select {
		case changed := <-w.titleCh:
			for id, st := range changed {
				_ = w.Core.SetSessionStatusTitle(id, st)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (w *Watcher) apply(r result) {
	delete(w.pending, r.key)
	switch r.key.kind {
	case fetchGit:
		// Two fetches for the same session can resolve out of order (the
		// delete dialog's on-demand check racing a routine refresh) — never
		// let an older answer clobber a newer one.
		if cur, ok := w.git[r.key.id]; ok && !r.git.checkedAt.After(cur.checkedAt) {
			return
		}
		w.git[r.key.id] = r.git
	case fetchPR:
		if cur, ok := w.pr[r.key.id]; ok && !r.pr.checkedAt.After(cur.checkedAt) {
			return
		}
		w.pr[r.key.id] = r.pr
	case fetchPrompt:
		w.prompts[r.key.id] = r.prompt
	}
}

func (w *Watcher) refreshAlive() {
	if w.Core == nil {
		return
	}
	w.alive = w.Core.TmuxAliveAll()
}

// stateOf is the join this package exists for: with tmux gone, whatever the
// agent last wrote to its own session log is stale, so the session is
// parked no matter what that log says.
func (w *Watcher) stateOf(s session.Session) watcher.State {
	if !w.alive[s.ID] {
		return watcher.Parked
	}
	return w.states[s.WorktreePath]
}

// emit sends a snapshot and pushes any tmux window titles that went stale
// with it. Renaming a window is a tmux subprocess, so it happens off this
// goroutine — the run loop must not block on it.
func (w *Watcher) emit(ctx context.Context, out chan<- Snapshot) {
	snap, changedTitles := w.build()
	if len(changedTitles) > 0 && w.titleCh != nil {
		select {
		case w.titleCh <- changedTitles:
		case <-ctx.Done():
		}
	}
	select {
	case out <- snap:
	case <-ctx.Done():
	}
}

// build assembles the current snapshot, prunes caches for sessions that no
// longer exist, and reports which sessions' tmux window titles no longer
// match their state.
func (w *Watcher) build() (Snapshot, map[string]watcher.State) {
	sessions := w.Core.Sessions()
	w.sessions = sessions
	views := make(map[string]View, len(sessions))
	live := make(map[string]bool, len(sessions))
	changedTitles := map[string]watcher.State{}

	// Live sessions first, each group keeping the order Sessions() gave it.
	// Two passes rather than a sort: the input is already in base order, so
	// this is just a stable partition.
	ordered := make([]session.Session, 0, len(sessions))
	for _, s := range sessions {
		if w.stateOf(s) != watcher.Parked {
			ordered = append(ordered, s)
		}
	}
	for _, s := range sessions {
		if w.stateOf(s) == watcher.Parked {
			ordered = append(ordered, s)
		}
	}

	for _, s := range sessions {
		live[s.ID] = true
		st := w.stateOf(s)
		v := View{ID: s.ID, State: st, Label: Label(st), Quip: Quip(s.ID, st)}
		// A prompt recovered from the agent's own log wins over the one
		// captured at creation: for a session resumed on an existing branch
		// the stored one may be empty, and where both exist they're the
		// same text anyway.
		if p := w.prompts[s.ID]; p.text != "" && p.agent == s.AgentName() {
			v.Prompt = p.text
		} else {
			v.Prompt = s.Prompt
		}
		if g, ok := w.git[s.ID]; ok {
			v.GitOK, v.Dirty, v.Unpushed = g.ok, g.dirty, g.unpushed
		}
		if p, ok := w.pr[s.ID]; ok && p.ok {
			info := p.info
			v.PR = &info
		}
		views[s.ID] = v

		if prev, ok := w.titles[s.ID]; !ok || prev != st {
			w.titles[s.ID] = st
			changedTitles[s.ID] = st
		}
	}

	w.prune(sessions, live)
	return Snapshot{Sessions: ordered, Views: views, PollTime: time.Now(), Err: w.lastErr}, changedTitles
}

// prune drops per-session bookkeeping for sessions that no longer exist.
// Without it every map here grows for the life of the process, holding on
// to whole prompt strings for sessions deleted hours ago.
func (w *Watcher) prune(sessions []session.Session, live map[string]bool) {
	livePaths := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		livePaths[s.WorktreePath] = true
	}
	for path := range w.states {
		if !livePaths[path] {
			delete(w.states, path)
		}
	}
	for id := range w.git {
		if !live[id] {
			delete(w.git, id)
		}
	}
	for id := range w.pr {
		if !live[id] {
			delete(w.pr, id)
		}
	}
	for id := range w.prompts {
		if !live[id] {
			delete(w.prompts, id)
		}
	}
	for id := range w.titles {
		if !live[id] {
			delete(w.titles, id)
		}
	}
	for k := range w.pending {
		if !live[k.id] {
			delete(w.pending, k)
		}
	}
}

// maxConcurrentFetches caps how many git/gh/log-scan fetches run at once, so
// a sweep over a lot of sessions doesn't burst that many subprocesses at the
// same instant.
const maxConcurrentFetches = 8

var fetchSem = make(chan struct{}, maxConcurrentFetches)

// schedule starts a background fetch for everything that's due. Each one is
// marked pending first, so a `git status` that runs long isn't re-issued on
// every tick until it finally returns.
func (w *Watcher) schedule(ctx context.Context, results chan<- result) {
	for _, s := range w.sessions {
		if w.gitDue(s) {
			w.start(ctx, results, fetchKey{fetchGit, s.ID}, func() result {
				dirty, unpushed, ok := w.Core.WorktreeStatus(s.ID)
				return result{
					key: fetchKey{fetchGit, s.ID},
					git: gitEntry{dirty: dirty, unpushed: unpushed, ok: ok, checkedAt: time.Now()},
				}
			})
		}
		if w.prDue(s) {
			w.start(ctx, results, fetchKey{fetchPR, s.ID}, func() result {
				info, ok := w.Core.PRStatus(s.ID)
				return result{
					key: fetchKey{fetchPR, s.ID},
					pr:  prEntry{info: info, ok: ok, checkedAt: time.Now()},
				}
			})
		}
		if w.promptDue(s) {
			agent, path := s.AgentName(), s.WorktreePath
			w.start(ctx, results, fetchKey{fetchPrompt, s.ID}, func() result {
				return result{
					key:    fetchKey{fetchPrompt, s.ID},
					prompt: promptEntry{text: prompt.ForAgent(w.Home, agent, path), agent: agent, checkedAt: time.Now()},
				}
			})
		}
	}
}

func (w *Watcher) start(ctx context.Context, results chan<- result, key fetchKey, fetch func() result) {
	w.pending[key] = true
	go func() {
		select {
		case fetchSem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-fetchSem }()
		r := fetch()
		select {
		case results <- r:
		case <-ctx.Done():
		}
	}()
}

// gitDue reports whether s's worktree status is worth re-checking.
//
// A parked session is checked once, like any session with nothing cached,
// but is then left alone however stale it gets: no agent is running in that
// worktree, so dirty/unpushed can't change on their own. It comes due again
// the moment it stops being parked — checkedAt didn't advance while it was.
func (w *Watcher) gitDue(s session.Session) bool {
	if w.pending[fetchKey{fetchGit, s.ID}] {
		return false
	}
	cur, ok := w.git[s.ID]
	if !ok {
		return true
	}
	if w.stateOf(s) == watcher.Parked {
		return false
	}
	return time.Since(cur.checkedAt) > staleAfter(s.ID, gitStaleAfter)
}

// prDue, unlike gitDue, keeps polling a parked session. A parked worktree's
// dirty/unpushed state can't change on its own, but its PR is exactly what
// does: the work is finished and pushed, and the merge happens on GitHub,
// with nothing local to notice it. A parked session that stopped being
// re-checked would sit on "open" forever.
//
// A session with no PR attached is polled too, on a slower cadence: that
// call is what discovers a PR opened for its branch (see App.PRStatus) and
// attaches it, so a fetch that comes back empty is a cost paid by every
// session that never gets one.
func (w *Watcher) prDue(s session.Session) bool {
	if w.pending[fetchKey{fetchPR, s.ID}] {
		return false
	}
	stale := prStaleAfter
	if s.PR == "" {
		stale = prDiscoverAfter
	}
	cur, ok := w.pr[s.ID]
	return !ok || time.Since(cur.checkedAt) > staleAfter(s.ID, stale)
}

// promptDue stops re-scanning once a prompt is found, and backs off when one
// isn't. Without the back-off every session with no discoverable prompt
// re-ran the scan on every tick forever — a line-by-line pass over every
// .jsonl under ~/.claude/projects/<cwd>/, or a sqlite3 subprocess per
// codex/opencode database — because an empty result is the very condition
// that selects a session for scanning. Prompts do appear late (the agent
// writes its log a moment after the session exists), hence a back-off
// rather than giving up.
func (w *Watcher) promptDue(s session.Session) bool {
	if w.Home == "" || w.pending[fetchKey{fetchPrompt, s.ID}] {
		return false
	}
	cur, ok := w.prompts[s.ID]
	if !ok || cur.agent != s.AgentName() {
		return true
	}
	return cur.text == "" && time.Since(cur.checkedAt) > promptRetryAfter
}

const (
	// gitStaleAfter bounds how long a cached git status is trusted. Long
	// enough that no session is re-checked every tick (those calls can run
	// well past one), short enough that a session sitting untouched still
	// eventually reflects changes made outside moomux.
	gitStaleAfter = time.Minute
	// prStaleAfter is longer: a PR's merge/CI status changes less often
	// than a worktree's dirty state, and `gh` hits the network (slower,
	// rate-limited) rather than a local git call.
	prStaleAfter = 2 * time.Minute
	// prDiscoverAfter paces the branch lookup for a session with no PR yet.
	// Longer than prStaleAfter because most of those calls find nothing:
	// plenty of sessions never open a PR at all, and `moomux tag -pr`
	// attaches one immediately when an agent does.
	prDiscoverAfter = 5 * time.Minute
	// promptRetryAfter bounds how often a session with no discoverable
	// first prompt is re-scanned.
	promptRetryAfter = 30 * time.Second
	// staleJitter varies each threshold by up to this fraction, per
	// session. Without it every session first fetched in the same moment
	// (notably: all of them, at startup) would keep coming due in the same
	// tick forever after — a thundering herd every minute, on the minute,
	// instead of spread out.
	staleJitter = 0.2
)

// staleAfter returns id's jittered threshold: deterministic, so it never
// flaps between "stale" and "fresh" from call to call, without storing
// anything extra per session.
func staleAfter(id string, base time.Duration) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	frac := float64(h.Sum32()) / float64(math.MaxUint32) // in [0,1)
	return time.Duration(float64(base) * (1 + staleJitter*(2*frac-1)))
}

// Once computes a single snapshot synchronously, fetching everything inline
// instead of in the background. For tests and cmd/uishot, which want a
// settled snapshot without running a loop; the real path is Run.
func Once(core Core, home string, states map[string]watcher.State) Snapshot {
	w := &Watcher{Core: core, Home: home}
	w.states = states
	if w.states == nil {
		w.states = map[string]watcher.State{}
	}
	w.git = map[string]gitEntry{}
	w.pr = map[string]prEntry{}
	w.prompts = map[string]promptEntry{}
	w.titles = map[string]watcher.State{}
	w.pending = map[fetchKey]bool{}
	w.refreshAlive()

	now := time.Now()
	for _, s := range core.Sessions() {
		dirty, unpushed, ok := core.WorktreeStatus(s.ID)
		w.git[s.ID] = gitEntry{dirty: dirty, unpushed: unpushed, ok: ok, checkedAt: now}
		if s.PR != "" {
			info, ok := core.PRStatus(s.ID)
			w.pr[s.ID] = prEntry{info: info, ok: ok, checkedAt: now}
		}
		if home != "" {
			w.prompts[s.ID] = promptEntry{
				text: prompt.ForAgent(home, s.AgentName(), s.WorktreePath), agent: s.AgentName(), checkedAt: now,
			}
		}
	}

	snap, _ := w.build()
	return snap
}
