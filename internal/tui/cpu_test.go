package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/sessionview"
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
	ch := make(chan sessionview.Snapshot)
	close(ch)
	m.statusCh = ch
	return m
}

func emptyTick() StatusTickMsg {
	return StatusTickMsg{Snap: sessionview.Snapshot{Views: map[string]sessionview.View{}, PollTime: time.Now()}}
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

// TestStatusTickDoesNoBackendWork guards the split: a snapshot is finished
// state, so applying one must not send the TUI back to the backend for
// anything. Every per-session probe a status tick used to fan out — tmux
// liveness, git status, `gh pr view`, agent-log prompt scans — is the
// core's job now (internal/sessionview), done once for every client rather
// than once per client.
func TestStatusTickDoesNoBackendWork(t *testing.T) {
	be := &fakeBackend{sessions: cpuTestSessions(2)}
	m := newCPUTestModel(be)

	be.tmuxAliveCalls.Store(0)
	_, cmd := m.Update(emptyTick())
	collectMsgs(cmd)
	if n := be.tmuxAliveCalls.Load(); n != 0 {
		t.Errorf("a status tick spawned %d tmux-alive polls, want 0", n)
	}
	if n := len(be.worktreeStatusCalls); n != 0 {
		t.Errorf("a status tick spawned %d git-status calls, want 0", n)
	}
	if n := len(be.prStatusCalls); n != 0 {
		t.Errorf("a status tick spawned %d PR-status calls, want 0", n)
	}
}

// TestMergeSnapshots covers the coalescing rule. A Snapshot is absolute
// state, so the newer one simply wins — the one thing that must survive is
// an error the newer snapshot didn't repeat, which would otherwise never
// reach the user at all.
func TestMergeSnapshots(t *testing.T) {
	older := sessionview.Snapshot{
		Views:    map[string]sessionview.View{"a": {ID: "a", State: watcher.Working}},
		PollTime: time.Now().Add(-time.Second),
		Err:      "older",
	}
	newer := sessionview.Snapshot{
		Views:    map[string]sessionview.View{"b": {ID: "b", State: watcher.Done}},
		PollTime: time.Now(),
	}

	got := mergeSnapshots(older, newer)
	if _, ok := got.Views["a"]; ok {
		t.Error("merged snapshot kept a view the newer snapshot dropped; snapshots are absolute, not deltas")
	}
	if got.Views["b"].State != watcher.Done {
		t.Errorf("merged views = %v, want the newer snapshot's", got.Views)
	}
	if !got.PollTime.Equal(newer.PollTime) {
		t.Error("merged snapshot did not take the newer PollTime")
	}
	if got.Err != "older" {
		t.Errorf("merged Err = %q, want the older snapshot's error carried forward", got.Err)
	}
}

// TestListenStatusCoalesces is the CPU fix: a burst of queued snapshots must
// arrive as one message, not one full handler pass plus re-render each.
func TestListenStatusCoalesces(t *testing.T) {
	ch := make(chan sessionview.Snapshot, 4)
	for _, st := range []watcher.State{watcher.Working, watcher.Done, watcher.NeedsInput} {
		ch <- sessionview.Snapshot{
			Views:    map[string]sessionview.View{"a": {ID: "a", State: st}},
			PollTime: time.Now(),
		}
	}

	msg, ok := listenStatus(ch)().(StatusTickMsg)
	if !ok {
		t.Fatal("want a StatusTickMsg")
	}
	if len(ch) != 0 {
		t.Errorf("%d snapshots left queued; the burst was not drained", len(ch))
	}
	if got := msg.Snap.Views["a"].State; got != watcher.NeedsInput {
		t.Errorf("coalesced state = %v, want the last snapshot in the burst", got)
	}
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

// TestSessionListRidesTheStream is the render-path fix: the session list used
// to be re-read from the backend on every Update and again on every View —
// a sessions.json read and sort locally, or a whole unix round trip on the
// socket-backed backend, twice per keystroke. It arrives on the snapshot
// stream now, already in display order.
func TestSessionListRidesTheStream(t *testing.T) {
	be := &fakeBackend{sessions: cpuTestSessions(3), tmuxAlive: map[string]bool{"demo:sa": true}}
	m := newCPUTestModel(be)
	seedViews(m, be, nil)

	be.sessionsCalls.Store(0)
	m.mode = ModeList
	m.View()
	m.mode = ModeMultiView
	m.View()
	if n := be.sessionsCalls.Load(); n != 0 {
		t.Errorf("rendering hit backend.Sessions() %d times with a snapshot in hand, want 0", n)
	}

	// And the order is the core's, not one the TUI re-derives.
	if len(m.sessions) != 3 || m.sessions[0].ID != "demo:sa" {
		t.Errorf("list = %v, want the served order (live session first)", m.sessions)
	}
}

// TestMutationFallsBackToTheBackend is the other half: right after a change
// this client made, the streamed list is a beat behind the store — so the
// handler acting on that change (moving the cursor off a deleted session,
// focusing a new one) must see the backend directly rather than a stale
// snapshot.
func TestMutationFallsBackToTheBackend(t *testing.T) {
	be := &fakeBackend{sessions: cpuTestSessions(3), tmuxAlive: map[string]bool{}}
	m := newCPUTestModel(be)
	seedViews(m, be, nil)

	var nudges int
	m.Nudge = func() { nudges++ }

	// The store loses a session; no snapshot has been emitted for it yet.
	be.sessions = cpuTestSessions(2)
	be.sessionsCalls.Store(0)
	m.Update(SessionDeletedMsg{})

	if n := be.sessionsCalls.Load(); n == 0 {
		t.Error("after a delete the model kept using the stale streamed list")
	}
	if len(m.sessions) != 2 {
		t.Errorf("list has %d sessions, want 2 — the delete must be visible immediately", len(m.sessions))
	}
	if nudges != 1 {
		t.Errorf("sent %d nudges, want 1 — the core must be asked for a fresh list", nudges)
	}
}
