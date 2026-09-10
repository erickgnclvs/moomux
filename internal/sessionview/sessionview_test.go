package sessionview

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

type fakeCore struct {
	folders map[string]map[string]config.FolderMeta

	mu       sync.Mutex
	sessions []session.Session
	alive    map[string]bool
	git      map[string][3]bool // dirty, unpushed, ok
	pr       map[string]prstatus.Info

	gitCalls   []string
	prCalls    []string
	aliveCalls int
	titles     map[string]watcher.State

	gitDelay time.Duration
}

func (f *fakeCore) ProjectFolders() map[string]map[string]config.FolderMeta {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.folders
}

func (f *fakeCore) Sessions() []session.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]session.Session(nil), f.sessions...)
}

func (f *fakeCore) TmuxAliveAll() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aliveCalls++
	out := map[string]bool{}
	for k, v := range f.alive {
		out[k] = v
	}
	return out
}

func (f *fakeCore) WorktreeStatus(id string) (bool, bool, bool) {
	if f.gitDelay > 0 {
		time.Sleep(f.gitDelay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gitCalls = append(f.gitCalls, id)
	v := f.git[id]
	return v[0], v[1], v[2]
}

func (f *fakeCore) PRStatus(id string) (prstatus.Info, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prCalls = append(f.prCalls, id)
	info, ok := f.pr[id]
	return info, ok
}

func (f *fakeCore) SetSessionStatusTitle(id string, st watcher.State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.titles == nil {
		f.titles = map[string]watcher.State{}
	}
	f.titles[id] = st
	return nil
}

func (f *fakeCore) calls(which *[]string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), (*which)...)
}

func sess(id, path string) session.Session {
	return session.Session{ID: id, Project: "demo", Name: id, WorktreePath: path}
}

// newWatcher builds a Watcher with its run-loop state initialized, for tests
// that drive build/schedule directly instead of running the loop.
func newWatcher(core Core) *Watcher {
	w := &Watcher{Core: core}
	w.states = map[string]watcher.State{}
	w.alive = map[string]bool{}
	w.git = map[string]gitEntry{}
	w.pr = map[string]prEntry{}
	w.prompts = map[string]promptEntry{}
	w.titles = map[string]watcher.State{}
	w.pending = map[fetchKey]bool{}
	return w
}

// TestParkedWhenTmuxIsGone is the join this package exists for, and the bug
// that made it necessary: the state lived in two places (the watcher's
// path-keyed map and a separate id-keyed tmux-liveness map) and every client
// had to combine them itself. With tmux gone the agent's own log is stale,
// so the session is parked no matter how recently it claimed to be working.
func TestParkedWhenTmuxIsGone(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{sess("demo:a", "/wt/a"), sess("demo:b", "/wt/b")},
		alive:    map[string]bool{"demo:a": true},
	}
	snap := Once(core, "", map[string]watcher.State{"/wt/a": watcher.Working, "/wt/b": watcher.Working})

	if got := snap.Views["demo:a"].State; got != watcher.Working {
		t.Errorf("live session state = %v, want working", got)
	}
	if got := snap.Views["demo:b"].State; got != watcher.Parked {
		t.Errorf("session with no tmux = %v, want parked despite its log saying working", got)
	}
}

// TestLabelAndQuipMatchEffectiveState is the drift that shipped before this
// package existed: the served quip was picked from the raw state while the
// TUI picked its own from the effective one, so a parked session's cow told
// the Mac app it was busy. Both now come off the same View.
func TestLabelAndQuipMatchEffectiveState(t *testing.T) {
	core := &fakeCore{sessions: []session.Session{sess("demo:a", "/wt/a")}} // no tmux
	snap := Once(core, "", map[string]watcher.State{"/wt/a": watcher.Working})

	v := snap.Views["demo:a"]
	if v.Label != Label(watcher.Parked) {
		t.Errorf("Label = %q, want %q", v.Label, Label(watcher.Parked))
	}
	if v.Quip != Quip("demo:a", watcher.Parked) {
		t.Errorf("Quip = %q, want the parked pool's", v.Quip)
	}
}

// TestPromptFallsBackToStored covers the recovery order: a prompt found in
// the agent's own log wins, and the one captured at creation time is the
// fallback. A client reads View.Prompt and never has to know there are two.
func TestPromptFallsBackToStored(t *testing.T) {
	s := sess("demo:a", "/wt/a")
	s.Prompt = "stored prompt"
	core := &fakeCore{sessions: []session.Session{s}, alive: map[string]bool{"demo:a": true}}

	// home "" disables log recovery, so the stored one must show through.
	if got := Once(core, "", nil).Views["demo:a"].Prompt; got != "stored prompt" {
		t.Errorf("Prompt = %q, want the stored one", got)
	}

	w := newWatcher(core)
	w.prompts["demo:a"] = promptEntry{text: "recovered from the log", agent: "claude", checkedAt: time.Now()}
	snap, _ := w.build()
	if got := snap.Views["demo:a"].Prompt; got != "recovered from the log" {
		t.Errorf("Prompt = %q, want the recovered one to win", got)
	}
}

// TestGitAndPRStatusRideTheSnapshot: status a client used to fetch per
// session, over its own round trips, on its own schedule, now arrives with
// the state it belongs to.
func TestGitAndPRStatusRideTheSnapshot(t *testing.T) {
	s := sess("demo:a", "/wt/a")
	s.PR = "https://github.com/x/y/pull/1"
	core := &fakeCore{
		sessions: []session.Session{s, sess("demo:b", "/wt/b")},
		alive:    map[string]bool{"demo:a": true, "demo:b": true},
		git:      map[string][3]bool{"demo:a": {true, true, true}},
		pr:       map[string]prstatus.Info{"demo:a": {State: "OPEN", CI: "PASSING"}},
	}
	snap := Once(core, "", nil)

	a := snap.Views["demo:a"]
	if !a.GitOK || !a.Dirty || !a.Unpushed {
		t.Errorf("git fields = %+v, want dirty and unpushed", a)
	}
	if a.PR == nil || a.PR.CI != "PASSING" {
		t.Errorf("PR = %+v, want the resolved status", a.PR)
	}
	if b := snap.Views["demo:b"]; b.PR != nil {
		t.Errorf("session with no PR attached got PR = %+v, want nil", b.PR)
	}
	if n := len(core.calls(&core.prCalls)); n != 1 {
		t.Errorf("PRStatus called %d times, want 1 — only the session with a PR", n)
	}
}

// TestParkedGitStatusSkipsRoutineRefetch: a parked worktree has no agent
// running in it, so dirty/unpushed can't change on their own. It's checked
// once and then left alone, and comes due again the moment tmux comes back —
// checkedAt didn't advance while it was parked.
func TestParkedGitStatusSkipsRoutineRefetch(t *testing.T) {
	core := &fakeCore{sessions: []session.Session{sess("demo:a", "/wt/a")}}
	w := newWatcher(core)

	s := core.sessions[0]
	if !w.gitDue(s) {
		t.Fatal("a session with nothing cached must be checked once, parked or not")
	}
	w.git["demo:a"] = gitEntry{ok: true, checkedAt: time.Now().Add(-2 * gitStaleAfter)}
	if w.gitDue(s) {
		t.Error("a parked session with a cached status must not be re-checked, however stale")
	}

	w.alive["demo:a"] = true
	if !w.gitDue(s) {
		t.Error("must come due again as soon as the session stops being parked")
	}
}

// TestUntaggedSessionPRDiscovery: a session with no PR attached is still
// polled, on the slower discovery cadence — that fetch is what finds a PR
// opened for its branch and attaches it (App.PRStatus).
func TestUntaggedSessionPRDiscovery(t *testing.T) {
	core := &fakeCore{sessions: []session.Session{sess("demo:a", "/wt/a")}}
	w := newWatcher(core)

	s := core.sessions[0]
	if s.PR != "" {
		t.Fatal("fixture must have no PR attached")
	}
	if !w.prDue(s) {
		t.Fatal("an untagged session must be checked once for a PR")
	}
	w.pr["demo:a"] = prEntry{checkedAt: time.Now().Add(-2 * prStaleAfter)}
	if w.prDue(s) {
		t.Error("discovery must back off past prStaleAfter, not re-run on the PR cadence")
	}
	w.pr["demo:a"] = prEntry{checkedAt: time.Now().Add(-2 * prDiscoverAfter)}
	if !w.prDue(s) {
		t.Error("discovery must come due again once prDiscoverAfter has passed")
	}
}

// TestParkedPRStatusKeepsRefetching is the counterpart to the git rule
// above: a parked session is the one whose PR status matters most, since it
// merges on GitHub with nothing local to notice.
func TestParkedPRStatusKeepsRefetching(t *testing.T) {
	core := &fakeCore{sessions: []session.Session{sess("demo:a", "/wt/a")}}
	core.sessions[0].PR = "https://github.com/o/r/pull/1"
	w := newWatcher(core)

	s := core.sessions[0]
	if w.stateOf(s) != watcher.Parked {
		t.Fatalf("fixture must be parked, got %v", w.stateOf(s))
	}
	if !w.prDue(s) {
		t.Fatal("a PR with nothing cached must be fetched")
	}
	w.pr["demo:a"] = prEntry{ok: true, checkedAt: time.Now()}
	if w.prDue(s) {
		t.Error("a freshly checked PR must not be re-fetched")
	}
	w.pr["demo:a"] = prEntry{ok: true, checkedAt: time.Now().Add(-2 * prStaleAfter)}
	if !w.prDue(s) {
		t.Error("a parked session's stale PR status must still come due")
	}
}

// TestGitStatusTrackedRegardlessOfState is the other half: "never checked"
// is maximally stale whatever the agent is doing.
func TestGitStatusTrackedRegardlessOfState(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{sess("demo:a", "/wt/a")},
		alive:    map[string]bool{"demo:a": true},
	}
	w := newWatcher(core)
	w.states["/wt/a"] = watcher.Working
	w.refreshAlive()

	if !w.gitDue(core.sessions[0]) {
		t.Error("a working session with no cached status must be fetched")
	}
	w.git["demo:a"] = gitEntry{ok: true, checkedAt: time.Now()}
	if w.gitDue(core.sessions[0]) {
		t.Error("a freshly checked session must not be re-fetched")
	}
}

// TestPendingSkipsDuplicateFetch: a `git status` that runs long must not be
// re-issued on every tick until it finally returns.
func TestPendingSkipsDuplicateFetch(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{sess("demo:a", "/wt/a")},
		alive:    map[string]bool{"demo:a": true},
	}
	w := newWatcher(core)
	w.refreshAlive()
	w.sessions = core.Sessions()

	results := make(chan result, 4)
	w.schedule(context.Background(), results)
	w.schedule(context.Background(), results)

	// One tick now schedules two fetches per session — git status and the
	// PR/branch lookup — so drain both before counting.
	<-results
	<-results
	if n := len(core.calls(&core.gitCalls)); n != 1 {
		t.Errorf("WorktreeStatus called %d times across two ticks, want 1", n)
	}
}

// TestApplyKeepsFresherResult: two fetches for the same session (the delete
// dialog's on-demand check racing a routine refresh) can resolve out of
// order — an older answer must never clobber a newer one.
func TestApplyKeepsFresherResult(t *testing.T) {
	w := newWatcher(&fakeCore{})
	newer, older := time.Now(), time.Now().Add(-time.Minute)

	w.apply(result{key: fetchKey{fetchGit, "demo:a"}, git: gitEntry{dirty: true, ok: true, checkedAt: newer}})
	w.apply(result{key: fetchKey{fetchGit, "demo:a"}, git: gitEntry{dirty: false, ok: true, checkedAt: older}})

	if got := w.git["demo:a"]; !got.dirty || !got.checkedAt.Equal(newer) {
		t.Errorf("git[demo:a] = %+v, want the newer result to survive", got)
	}
}

// TestStaleThresholdIsJittered: without jitter every session first fetched
// in the same moment (notably all of them, at startup) keeps coming due in
// the same tick forever after — a thundering herd every minute, on the
// minute.
func TestStaleThresholdIsJittered(t *testing.T) {
	seen := map[time.Duration]bool{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		d := staleAfter(id, gitStaleAfter)
		if d < time.Duration(float64(gitStaleAfter)*(1-staleJitter)) || d > time.Duration(float64(gitStaleAfter)*(1+staleJitter)) {
			t.Fatalf("staleAfter(%q) = %v, outside the jitter band", id, d)
		}
		if got := staleAfter(id, gitStaleAfter); got != d {
			t.Fatalf("staleAfter(%q) is not stable: %v then %v", id, d, got)
		}
		seen[d] = true
	}
	if len(seen) < 4 {
		t.Errorf("8 ids produced only %d distinct thresholds; that's not spread out", len(seen))
	}
}

// TestPromptScanBacksOff: a scan that comes back empty leaves the cache
// entry empty, which is the very condition that selects a session for
// scanning — so without a back-off it re-ran on every tick forever, each run
// a line-by-line pass over every .jsonl under ~/.claude/projects/<cwd>/ or a
// sqlite3 subprocess per database.
func TestPromptScanBacksOff(t *testing.T) {
	core := &fakeCore{sessions: []session.Session{sess("demo:a", "/wt/a")}}
	w := newWatcher(core)
	w.Home = "/home/nobody"
	s := core.sessions[0]

	if !w.promptDue(s) {
		t.Fatal("a never-scanned session must be scanned")
	}
	w.prompts["demo:a"] = promptEntry{text: "", agent: "claude", checkedAt: time.Now()}
	if w.promptDue(s) {
		t.Error("an empty result must not be re-scanned within promptRetryAfter")
	}
	w.prompts["demo:a"] = promptEntry{text: "", agent: "claude", checkedAt: time.Now().Add(-promptRetryAfter - time.Second)}
	if !w.promptDue(s) {
		t.Error("past the back-off it must try again; prompts do appear late")
	}
	w.prompts["demo:a"] = promptEntry{text: "found it", agent: "claude", checkedAt: time.Now().Add(-time.Hour)}
	if w.promptDue(s) {
		t.Error("a session whose prompt was found must never be re-scanned")
	}
}

// TestTitlesPushedOnStateChange: keeping each tmux window's name in step
// with its agent state is core upkeep, not a rendering concern. It used to
// be driven by whichever front end happened to be attached — so titles froze
// with the TUI closed, and two clients both issued the renames.
func TestTitlesPushedOnStateChange(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{sess("demo:a", "/wt/a")},
		alive:    map[string]bool{"demo:a": true},
	}
	w := newWatcher(core)
	w.refreshAlive()
	w.states["/wt/a"] = watcher.Working

	_, changed := w.build()
	if changed["demo:a"] != watcher.Working {
		t.Fatalf("first build reported titles %v, want demo:a working", changed)
	}
	if _, again := w.build(); len(again) != 0 {
		t.Errorf("unchanged state reported %v, want no rename", again)
	}

	w.states["/wt/a"] = watcher.NeedsInput
	if _, changed := w.build(); changed["demo:a"] != watcher.NeedsInput {
		t.Errorf("state change reported %v, want a rename", changed)
	}
}

// TestPruneDropsDeletedSessions is the memory fix: per-session bookkeeping
// used to grow for the life of the process, holding entries — whole prompt
// strings included — for every session ever deleted.
func TestPruneDropsDeletedSessions(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{sess("demo:a", "/wt/a"), sess("demo:gone", "/wt/gone")},
		alive:    map[string]bool{"demo:a": true, "demo:gone": true},
	}
	w := newWatcher(core)
	w.refreshAlive()
	for _, id := range []string{"demo:a", "demo:gone"} {
		w.git[id] = gitEntry{ok: true, checkedAt: time.Now()}
		w.pr[id] = prEntry{ok: true, checkedAt: time.Now()}
		w.prompts[id] = promptEntry{text: "a prompt"}
		w.pending[fetchKey{fetchGit, id}] = true
	}
	w.states["/wt/a"] = watcher.Working
	w.states["/wt/gone"] = watcher.Working
	w.build()

	core.sessions = core.sessions[:1]
	w.build()

	if _, ok := w.states["/wt/gone"]; ok {
		t.Error("states kept a deleted session's path")
	}
	for name, present := range map[string]bool{
		"git":     mapHas(w.git, "demo:gone"),
		"pr":      mapHas(w.pr, "demo:gone"),
		"prompts": mapHas(w.prompts, "demo:gone"),
		"titles":  mapHas(w.titles, "demo:gone"),
		"pending": w.pending[fetchKey{fetchGit, "demo:gone"}],
	} {
		if present {
			t.Errorf("%s kept a deleted session's entry", name)
		}
	}
	if !mapHas(w.prompts, "demo:a") || !mapHas(w.titles, "demo:a") {
		t.Error("pruning dropped a live session's entry")
	}
}

func mapHas[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

// TestFetchesRunConcurrently guards against checking each session one at a
// time: `git status` and `gh pr view` are subprocess- and network-bound, so
// a sequential sweep makes latency scale with session count instead of with
// the slowest single call.
func TestFetchesRunConcurrently(t *testing.T) {
	var sessions []session.Session
	alive := map[string]bool{}
	for _, id := range []string{"a", "b", "c", "d"} {
		sessions = append(sessions, sess(id, "/wt/"+id))
		alive[id] = true
	}
	core := &fakeCore{sessions: sessions, alive: alive, gitDelay: 50 * time.Millisecond}
	w := newWatcher(core)
	w.refreshAlive()
	w.sessions = sessions

	results := make(chan result, 8)
	start := time.Now()
	w.schedule(context.Background(), results)
	for i := 0; i < len(sessions); i++ {
		<-results
	}
	// Sequential would be ~4*50ms=200ms; concurrent lands near one delay.
	// 150ms leaves headroom for a loaded CI box while still failing hard on
	// a sequential implementation.
	if elapsed := time.Since(start); elapsed >= 150*time.Millisecond {
		t.Fatalf("scheduling 4 fetches at 50ms each took %v — looks sequential", elapsed)
	}
}

// TestRawSnapshotsDoNotRepollTmux is the CPU fix carried over from the TUI:
// the agent watchers are event-driven on darwin, so re-listing tmux sessions
// on every raw snapshot would be a subprocess per file write. Liveness gets
// the ticker (and an explicit nudge) instead.
func TestRawSnapshotsDoNotRepollTmux(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{sess("demo:a", "/wt/a")},
		alive:    map[string]bool{"demo:a": true},
	}
	raw := make(chan watcher.Snapshot, 8)
	w := &Watcher{Core: core, Raw: chanWatcher{raw}, Interval: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan Snapshot, 32)
	go w.Run(ctx, out)

	<-out // the startup emit, which does poll liveness once
	core.mu.Lock()
	core.aliveCalls = 0
	core.mu.Unlock()

	for i := 0; i < 5; i++ {
		raw <- watcher.Snapshot{States: map[string]watcher.State{"/wt/a": watcher.Working}}
		<-out
	}
	core.mu.Lock()
	n := core.aliveCalls
	core.mu.Unlock()
	if n != 0 {
		t.Errorf("5 raw snapshots spawned %d tmux-alive polls, want 0", n)
	}
}

// TestNudgeEmitsNow covers the escape hatch: after parking a session, or
// creating one, waiting out a whole tick is visible in the UI.
func TestNudgeEmitsNow(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{sess("demo:a", "/wt/a")},
		alive:    map[string]bool{"demo:a": true},
	}
	w := &Watcher{Core: core, Interval: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan Snapshot, 4)
	go w.Run(ctx, out)
	<-out // startup emit

	core.mu.Lock()
	core.alive = map[string]bool{} // tmux just died
	core.mu.Unlock()
	w.Nudge()

	// Snapshots the startup status sweep already queued can still be in
	// flight, so look for the one the nudge caused rather than assuming the
	// very next one is it. The interval is an hour: without the nudge, none
	// of these would ever report parked.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case snap := <-out:
			if snap.Views["demo:a"].State == watcher.Parked {
				return
			}
		case <-deadline:
			t.Fatal("nudge produced no fresh snapshot; the UI would wait out the whole interval")
		}
	}
}

type chanWatcher struct{ ch chan watcher.Snapshot }

func (c chanWatcher) Run(ctx context.Context, out chan<- watcher.Snapshot) {
	for {
		select {
		case snap := <-c.ch:
			select {
			case out <- snap:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// TestStartupSweepBeginsImmediately: every session is "never checked" when
// Run starts, so waiting out a whole interval before the first sweep leaves
// a front end rendering its opening frames with no git or PR status.
func TestStartupSweepBeginsImmediately(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{sess("demo:a", "/wt/a")},
		alive:    map[string]bool{"demo:a": true},
		git:      map[string][3]bool{"demo:a": {true, false, true}},
	}
	// An interval far longer than the test: if the sweep waited for a tick,
	// nothing would ever be fetched.
	w := &Watcher{Core: core, Interval: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan Snapshot, 8)
	go w.Run(ctx, out)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case snap := <-out:
			if snap.Views["demo:a"].GitOK {
				return
			}
		case <-deadline:
			t.Fatal("no git status within 5s; the first sweep waited for a tick")
		}
	}
}

// TestSessionsServedInDisplayOrder: the base order (manual, or recent-first)
// already came from the core, but the "live tmux window floats to the top"
// tiebreak used to be applied per client off each client's own liveness map
// — so two front ends could list the same project in different orders. It's
// folded into the served list now.
func TestSessionsServedInDisplayOrder(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{sess("a", "/wt/a"), sess("b", "/wt/b"), sess("c", "/wt/c")},
		alive:    map[string]bool{"b": true},
	}
	snap := Once(core, "", nil)

	var got []string
	for _, s := range snap.Sessions {
		got = append(got, s.ID)
	}
	want := []string{"b", "a", "c"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("order = %v, want %v (live first, then the base order preserved)", got, want)
		}
	}
}

// TestSessionOrderIsStableWithinGroups: the live-first partition must not
// disturb the order Sessions() gave — that order carries the user's manual
// arrangement (or the recent-first sort), and reshuffling it inside a group
// would look like sessions moving on their own.
func TestSessionOrderIsStableWithinGroups(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{sess("a", "/wt/a"), sess("b", "/wt/b"), sess("c", "/wt/c"), sess("d", "/wt/d")},
		alive:    map[string]bool{"a": true, "c": true},
	}
	var got []string
	for _, s := range Once(core, "", nil).Sessions {
		got = append(got, s.ID)
	}
	want := []string{"a", "c", "b", "d"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestPromptRescannedWhenAgentChanges: a first prompt is recovered from that
// agent's own logs, so switching a session's agent invalidates it. Without
// this, promptDue's "found it, never scan again" rule left a session moved
// from claude to codex showing the claude-log prompt for the life of the
// process.
func TestPromptRescannedWhenAgentChanges(t *testing.T) {
	s := sess("demo:a", "/wt/a")
	s.Agent = "claude"
	core := &fakeCore{sessions: []session.Session{s}, alive: map[string]bool{"demo:a": true}}
	w := newWatcher(core)
	w.Home = "/home/nobody"
	w.prompts["demo:a"] = promptEntry{text: "from the claude log", agent: "claude", checkedAt: time.Now()}

	if w.promptDue(core.sessions[0]) {
		t.Fatal("a prompt already recovered for this agent must not be re-scanned")
	}

	core.sessions[0].Agent = "codex"
	if !w.promptDue(core.sessions[0]) {
		t.Error("changing the agent must invalidate the recovered prompt")
	}
	// And it must not be shown in the meantime.
	w.refreshAlive()
	snap, _ := w.build()
	if got := snap.Views["demo:a"].Prompt; got == "from the claude log" {
		t.Errorf("Prompt = %q, want the other agent's log not to be shown", got)
	}
}
