package tui

import (
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

func cpuTestSessions(n int) []session.Session {
	out := make([]session.Session, 0, n)
	for i := 0; i < n; i++ {
		id := "demo:s" + string(rune('a'+i))
		out = append(out, session.Session{
			ID: id, Project: "demo", Name: "s" + string(rune('a'+i)),
			WorktreePath: "/wt/s" + string(rune('a'+i)), TmuxSession: "moomux-" + id,
			CreatedAt: time.Now(), Agent: "claude",
		})
	}
	return out
}

// newCPUTestModel is newTestModel with an already-closed status channel, so
// collectMsgs can run a StatusTickMsg batch — which always re-arms
// listenStatus — without parking on an open, empty channel.
func newCPUTestModel(be *fakeBackend) *Model {
	m := newTestModel(be)
	ch := make(chan watcher.Snapshot)
	close(ch)
	m.statusCh = ch
	return m
}

func emptyTick() StatusTickMsg {
	return StatusTickMsg{Snap: watcher.Snapshot{States: map[string]watcher.State{}, PollTime: time.Now()}}
}

// TestSessionsReadOncePerPass is the CPU fix: one Update or one View must
// hit the backend at most once for the session list. Both passes ask for it
// many times over — every panel-count, eligible-project and per-project
// filter helper fetches it again — and each call is a sessions.json read,
// unmarshal and sort, or a whole unix round trip on the socket-backed
// backend.
func TestSessionsReadOncePerPass(t *testing.T) {
	be := &fakeBackend{sessions: cpuTestSessions(4)}
	m := newTestModel(be)

	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"View in ModeList", func() { m.mode = ModeList; m.View() }},
		{"View in ModeMultiView", func() { m.mode = ModeMultiView; m.View() }},
		{"Update on a status tick", func() { m.Update(emptyTick()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			be.sessionsCalls.Store(0)
			tc.run()
			if n := be.sessionsCalls.Load(); n > 1 {
				t.Errorf("backend.Sessions() called %d times in one pass, want at most 1", n)
			}
		})
	}
}

// TestSessionsRereadEachPass is the other half: memoizing within a pass must
// not let a stale snapshot survive into the next one, or a session created
// by a tea.Cmd (or by another moomux process) would never appear.
func TestSessionsRereadEachPass(t *testing.T) {
	be := &fakeBackend{sessions: cpuTestSessions(1)}
	m := newTestModel(be)
	m.Update(emptyTick())

	be.sessions = cpuTestSessions(3)
	m.Update(emptyTick())
	if len(m.sessions) != 3 {
		t.Errorf("after the backend gained sessions, list has %d, want 3", len(m.sessions))
	}
}

// TestStatusTickDoesNotPollTmux is the CPU fix: tmux liveness used to be
// re-polled once per watcher snapshot — a `tmux list-sessions` subprocess
// (~4.5ms of fork and exec) per snapshot from any of the three watchers,
// several a second at idle and up to ten a second while an agent's
// filesystem writes drove the debounced rescans.
func TestStatusTickDoesNotPollTmux(t *testing.T) {
	be := &fakeBackend{sessions: cpuTestSessions(2)}
	m := newCPUTestModel(be)

	be.tmuxAliveCalls.Store(0)
	_, cmd := m.Update(emptyTick())
	collectMsgs(cmd)
	if n := be.tmuxAliveCalls.Load(); n != 0 {
		t.Errorf("a status tick spawned %d tmux-alive polls, want 0", n)
	}
}

// TestTmuxTickPollsAndReschedules covers what replaced it: liveness on its
// own steady timer, which must keep itself running.
func TestTmuxTickPollsAndReschedules(t *testing.T) {
	be := &fakeBackend{sessions: cpuTestSessions(2)}
	m := newCPUTestModel(be)

	// Keep the reschedule from actually sleeping out the real interval.
	orig := tmuxRefreshInterval
	tmuxRefreshInterval = time.Millisecond
	defer func() { tmuxRefreshInterval = orig }()

	be.tmuxAliveCalls.Store(0)
	_, cmd := m.Update(TmuxTickMsg{})
	if cmd == nil {
		t.Fatal("TmuxTickMsg produced no command")
	}
	msgs := collectMsgs(cmd)
	if n := be.tmuxAliveCalls.Load(); n != 1 {
		t.Errorf("tmux-alive polled %d times, want 1", n)
	}
	var rescheduled bool
	for _, msg := range msgs {
		if _, ok := msg.(TmuxTickMsg); ok {
			rescheduled = true
		}
	}
	if !rescheduled {
		t.Error("TmuxTickMsg did not reschedule itself; liveness polling would stop after one tick")
	}
}

// TestPromptScanBacksOff is the CPU fix: a session whose first prompt can't
// be found leaves m.prompts empty, which is the very condition that selects
// it for scanning — so it used to be re-scanned on every status tick
// forever, each scan a line-by-line JSON walk of every .jsonl under
// ~/.claude/projects/<cwd>/ or a sqlite3 subprocess per database.
func TestPromptScanBacksOff(t *testing.T) {
	be := &fakeBackend{sessions: cpuTestSessions(2)}
	m := newTestModel(be)

	// The sample worktrees have no agent logs, so every scan comes back
	// empty — exactly the case that used to retry forever.
	first := refreshStatusCmd(m)
	if got := first().(StatusRefreshedMsg).Prompts; len(got) != 2 {
		t.Fatalf("first pass scanned %d sessions, want 2", len(got))
	}

	second := refreshStatusCmd(m)
	if got := second().(StatusRefreshedMsg).Prompts; len(got) != 0 {
		t.Errorf("second pass re-scanned %d sessions, want 0 within promptRetryAfter", len(got))
	}

	// Past the backoff window it must try again — prompts do appear late,
	// once the agent has written its log.
	for id := range m.promptCheckedAt {
		m.promptCheckedAt[id] = time.Now().Add(-promptRetryAfter - time.Second)
	}
	third := refreshStatusCmd(m)
	if got := third().(StatusRefreshedMsg).Prompts; len(got) != 2 {
		t.Errorf("after promptRetryAfter, scanned %d sessions, want 2", len(got))
	}
}

// TestMergeSnapshots covers the coalescing rule: newer wins per path, and
// paths only one snapshot knows about survive — snapshots come from
// independent watchers that each cover only their own agent's paths, so
// replacing rather than merging would wipe the others.
func TestMergeSnapshots(t *testing.T) {
	older := watcher.Snapshot{
		States:   map[string]watcher.State{"/wt/a": watcher.Working, "/wt/b": watcher.Done},
		PollTime: time.Now().Add(-time.Second),
		Err:      errors.New("older"),
	}
	newer := watcher.Snapshot{
		States:   map[string]watcher.State{"/wt/a": watcher.Done, "/wt/c": watcher.NeedsInput},
		PollTime: time.Now(),
	}

	got := mergeSnapshots(older, newer)
	want := map[string]watcher.State{"/wt/a": watcher.Done, "/wt/b": watcher.Done, "/wt/c": watcher.NeedsInput}
	for path, st := range want {
		if got.States[path] != st {
			t.Errorf("%s: got %v, want %v", path, got.States[path], st)
		}
	}
	if len(got.States) != len(want) {
		t.Errorf("merged %d paths, want %d", len(got.States), len(want))
	}
	if !got.PollTime.Equal(newer.PollTime) {
		t.Error("merged snapshot did not take the newer PollTime")
	}
	if got.Err == nil {
		t.Error("merged snapshot dropped the older snapshot's error")
	}
	if older.States["/wt/a"] != watcher.Working {
		t.Error("mergeSnapshots mutated its input; those maps belong to the watcher goroutines")
	}
}

// TestListenStatusCoalesces is the CPU fix: a burst of queued snapshots must
// arrive as one message, not one full handler pass plus re-render each.
func TestListenStatusCoalesces(t *testing.T) {
	ch := make(chan watcher.Snapshot, 4)
	ch <- watcher.Snapshot{States: map[string]watcher.State{"/wt/a": watcher.Working}, PollTime: time.Now()}
	ch <- watcher.Snapshot{States: map[string]watcher.State{"/wt/b": watcher.Working}, PollTime: time.Now()}
	ch <- watcher.Snapshot{States: map[string]watcher.State{"/wt/a": watcher.Done}, PollTime: time.Now()}

	msg, ok := listenStatus(ch)().(StatusTickMsg)
	if !ok {
		t.Fatal("want a StatusTickMsg")
	}
	if len(ch) != 0 {
		t.Errorf("%d snapshots left queued; the burst was not drained", len(ch))
	}
	if msg.Snap.States["/wt/a"] != watcher.Done || msg.Snap.States["/wt/b"] != watcher.Working {
		t.Errorf("coalesced states = %v", msg.Snap.States)
	}
}

// TestPruneDeadSessions is the memory fix: per-session bookkeeping used to
// grow for the life of the process, holding entries — whole prompt strings
// included — for every session ever deleted.
func TestPruneDeadSessions(t *testing.T) {
	be := &fakeBackend{sessions: cpuTestSessions(2)}
	m := newTestModel(be)

	gone, kept := be.sessions[0], be.sessions[1]
	for _, s := range be.sessions {
		m.states[s.WorktreePath] = watcher.Working
		m.titleState[s.ID] = watcher.Working
		m.gitStatus[s.ID] = gitStatusInfo{ok: true, checkedAt: time.Now()}
		m.prStatus[s.ID] = prStatusInfo{ok: true, checkedAt: time.Now()}
		m.prompts[s.ID] = "some prompt"
		m.promptCheckedAt[s.ID] = time.Now()
		m.gitStatusPending[s.ID] = true
		m.prStatusPending[s.ID] = true
	}

	be.sessions = []session.Session{kept}
	m.Update(emptyTick())

	if _, ok := m.states[gone.WorktreePath]; ok {
		t.Error("states kept a deleted session's path")
	}
	for name, present := range map[string]bool{
		"titleState":       mapHas(m.titleState, gone.ID),
		"gitStatus":        mapHas(m.gitStatus, gone.ID),
		"prStatus":         mapHas(m.prStatus, gone.ID),
		"prompts":          mapHas(m.prompts, gone.ID),
		"promptCheckedAt":  mapHas(m.promptCheckedAt, gone.ID),
		"gitStatusPending": mapHas(m.gitStatusPending, gone.ID),
		"prStatusPending":  mapHas(m.prStatusPending, gone.ID),
	} {
		if present {
			t.Errorf("%s kept a deleted session's entry", name)
		}
	}
	if !mapHas(m.prompts, kept.ID) || !mapHas(m.titleState, kept.ID) {
		t.Error("pruning dropped a live session's entry")
	}
}

func mapHas[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

// collectMsgs runs cmd (unwrapping tea.Batch) and returns every message it
// produced, without feeding them back into Update — unlike drainAll, which
// does feed them back.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collectMsgs(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}
