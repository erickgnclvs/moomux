package watcher

import (
	"context"
	"testing"
	"time"
)

// staticWatcher emits one snapshot immediately and then blocks until ctx done.
type staticWatcher struct {
	states map[string]State
}

func (s *staticWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	snap := Snapshot{States: s.states, PollTime: time.Now()}
	send(ctx, out, snap)
	<-ctx.Done()
}

func TestMultiWatcherMergesSnapshots(t *testing.T) {
	wa := &staticWatcher{states: map[string]State{"/wt-a": Working}}
	wb := &staticWatcher{states: map[string]State{"/wt-b": Done}}

	m := &MultiWatcher{Watchers: []Watcher{wa, wb}}
	ch := make(chan Snapshot, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go m.Run(ctx, ch)

	seen := map[string]State{}
	deadline := time.After(150 * time.Millisecond)
	for len(seen) < 2 {
		select {
		case snap := <-ch:
			for path, st := range snap.States {
				seen[path] = st
			}
		case <-deadline:
			t.Fatalf("timed out; seen so far: %v", seen)
		}
	}
	if seen["/wt-a"] != Working {
		t.Fatalf("/wt-a = %v, want Working", seen["/wt-a"])
	}
	if seen["/wt-b"] != Done {
		t.Fatalf("/wt-b = %v, want Done", seen["/wt-b"])
	}
}

// chanWatcher forwards whatever the test feeds it.
type chanWatcher chan Snapshot

func (c chanWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	for {
		select {
		case snap := <-c:
			send(ctx, out, snap)
		case <-ctx.Done():
			return
		}
	}
}

// Two agents that know the same worktree (a Codex thread left behind where
// Claude now runs) must not overwrite each other: the session's state used to
// flip with whichever watcher ticked last.
func TestMultiWatcherMaxMergesSamePathAcrossWatchers(t *testing.T) {
	claude, codex := make(chanWatcher), make(chanWatcher)
	m := &MultiWatcher{Watchers: []Watcher{claude, codex}}
	out := make(chan Snapshot)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go m.Run(ctx, out)

	next := func(feed chanWatcher, st State) State {
		t.Helper()
		feed <- Snapshot{States: map[string]State{"/wt": st}}
		select {
		case snap := <-out:
			return snap.States["/wt"]
		case <-ctx.Done():
			t.Fatal("timed out waiting for a snapshot")
			return Unknown
		}
	}

	next(claude, NeedsInput)
	for range 3 {
		if got := next(codex, Done); got != NeedsInput {
			t.Fatalf("codex's Done tick: got %v, want NeedsInput from claude", got)
		}
	}
	// A watcher's own state can still drop: claude answered, now Working,
	// and Working still outranks codex's Done.
	if got := next(claude, Working); got != Working {
		t.Fatalf("after claude went Working: got %v, want Working", got)
	}
	if got := next(claude, Done); got != Done {
		t.Fatalf("after claude went Done: got %v, want Done", got)
	}
}

func TestMultiWatcherEmpty(t *testing.T) {
	m := &MultiWatcher{}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// Should not block or panic with zero watchers.
	go m.Run(ctx, ch)
	<-ctx.Done()
}
