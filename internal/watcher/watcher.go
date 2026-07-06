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
	Waiting
	Working
)

func (s State) String() string {
	switch s {
	case Parked:
		return "parked"
	case Waiting:
		return "waiting"
	case Working:
		return "working"
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

// DirWatcher polls a directory of JSON session files (used by Claude and Codex).
type DirWatcher struct {
	Dir      string
	Interval time.Duration
}

// Run polls until ctx is canceled. Each tick produces one Snapshot on out.
func (w *DirWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	if w.Interval == 0 {
		w.Interval = 2 * time.Second
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	w.tick(ctx, out)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx, out)
		}
	}
}

func (w *DirWatcher) tick(ctx context.Context, out chan<- Snapshot) {
	snap := Snapshot{States: map[string]State{}, PollTime: time.Now()}
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		snap.Err = fmt.Errorf("read %s: %w", w.Dir, err)
		send(ctx, out, snap)
		return
	}
	var parseErrs []error
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		rs, err := parseFile(filepath.Join(w.Dir, e.Name()))
		if err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("parse %s: %w", e.Name(), err))
			continue
		}
		if rs.CWD == "" {
			continue
		}
		st := classify(rs)
		if prev, ok := snap.States[rs.CWD]; !ok || st > prev {
			snap.States[rs.CWD] = st
		}
	}
	if len(parseErrs) > 0 {
		snap.Err = errors.Join(parseErrs...)
	}
	send(ctx, out, snap)
}

func send(ctx context.Context, out chan<- Snapshot, snap Snapshot) {
	select {
	case out <- snap:
	case <-ctx.Done():
	}
}
