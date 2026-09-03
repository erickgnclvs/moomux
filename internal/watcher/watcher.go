// Package watcher polls agent session state and emits Snapshots.
package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
}

func (w *DirWatcher) tick(ctx context.Context, out chan<- Snapshot) {
	snap := Snapshot{PollTime: time.Now()}
	states, readErr, parseErr := scanDir(w.Dir)
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

// scanDir reads dir for *.json session/marker files, classifies each, and
// max-merges the result by cwd. Shared by DirWatcher (Claude's session dir)
// and SQLiteWatcher's optional MarkerDir (Codex's needs-input markers) —
// both watch a directory of small JSON files keyed by cwd. readErr means dir
// itself couldn't be read (states is unpopulated); parseErr (an errors.Join
// of per-file failures) leaves states populated with everything that did
// parse, so one half-written file doesn't blind the caller to every other
// session in the directory.
func scanDir(dir string) (states map[string]State, readErr, parseErr error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err, nil
	}
	states = map[string]State{}
	var parseErrs []error
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		rs, err := parseFile(filepath.Join(dir, e.Name()))
		if err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("parse %s: %w", e.Name(), err))
			continue
		}
		if rs.CWD == "" {
			continue
		}
		st := classify(rs)
		if prev, ok := states[rs.CWD]; !ok || st > prev {
			states[rs.CWD] = st
		}
	}
	if len(parseErrs) > 0 {
		parseErr = errors.Join(parseErrs...)
	}
	return states, nil, parseErr
}

func send(ctx context.Context, out chan<- Snapshot, snap Snapshot) {
	select {
	case out <- snap:
	case <-ctx.Done():
	}
}
