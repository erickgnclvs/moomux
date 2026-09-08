// Package watcher polls agent session state and emits Snapshots.
package watcher

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// State describes what a session is doing.
type State int

const (
	Unknown State = iota
	Parked
	Done
	Working
	// NeedsInput means the agent is blocked on the user: a permission
	// prompt, an idle-prompt timeout, or similar. Ranked above Working so a
	// stale "busy" write from the agent's own status file never hides it.
	NeedsInput
)

// MarshalJSON writes the name, not the iota.
//
// State crosses the socket on every snapshot (see internal/sessionview), and
// these values are deliberately *ranked* — see NeedsInput above — so the
// numbers exist to be reordered. As bare ints, one re-rank for a reason that
// has nothing to do with the wire would silently reassign every state a
// second front end shows, with no compile error on either side. The names
// are also what config.Themes keys its per-state colors by, so a client gets
// one vocabulary instead of two.
func (s State) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

func (s *State) UnmarshalJSON(b []byte) error {
	name := strings.Trim(string(b), `"`)
	for _, st := range []State{Unknown, Parked, Done, Working, NeedsInput} {
		if st.String() == name {
			*s = st
			return nil
		}
	}
	return fmt.Errorf("unknown session state %q", name)
}

func (s State) String() string {
	switch s {
	case Parked:
		return "parked"
	case Done:
		return "done"
	case Working:
		return "working"
	case NeedsInput:
		return "needs-input"
	}
	return "unknown"
}

// Snapshot maps worktree path → state observed at PollTime.
//
// Err is set (non-nil) when this tick failed to fully determine state for
// one or more paths (e.g. a subprocess failure, unreadable directory, or an
// unparsable file). When Err is set, States may be incomplete for affected
// paths — callers should treat this as "unknown, not necessarily unchanged"
// rather than silently trusting the last-known state forever.
type Snapshot struct {
	States   map[string]State
	PollTime time.Time
	Err      error
}

// Watcher is implemented by every agent-specific watcher.
type Watcher interface {
	Run(ctx context.Context, out chan<- Snapshot)
}

// DirWatcher watches a directory of JSON session files (used by Claude and
// Codex) for changes. Run is implemented per-platform: dirwatcher_fsevents.go
// (darwin) watches the directory for filesystem events instead of polling it
// on a timer; dirwatcher_poll.go (everywhere else) ticks Interval (default
// 2s) as before.
type DirWatcher struct {
	Dir      string
	Interval time.Duration

	scanner dirScanner
}

func (w *DirWatcher) tick(ctx context.Context, out chan<- Snapshot) {
	snap := Snapshot{PollTime: time.Now()}
	states, readErr, parseErr := w.scanner.scan(w.Dir)
	if readErr != nil {
		snap.States = map[string]State{}
		snap.Err = fmt.Errorf("read %s: %w", w.Dir, readErr)
		send(ctx, out, snap)
		return
	}
	snap.States = states
	snap.Err = parseErr
	send(ctx, out, snap)
}

func send(ctx context.Context, out chan<- Snapshot, snap Snapshot) {
	select {
	case out <- snap:
	case <-ctx.Done():
	}
}
