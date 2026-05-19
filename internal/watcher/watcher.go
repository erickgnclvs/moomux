// Package watcher polls ~/.claude/sessions/*.json and emits state snapshots.
package watcher

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

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
type Snapshot struct {
	States   map[string]State
	PollTime time.Time
}

type Watcher struct {
	Dir      string
	Interval time.Duration
}

// Run polls until ctx is canceled. Each tick produces one Snapshot on out.
func (w *Watcher) Run(ctx context.Context, out chan<- Snapshot) {
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

func (w *Watcher) tick(ctx context.Context, out chan<- Snapshot) {
	snap := Snapshot{States: map[string]State{}, PollTime: time.Now()}
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		send(ctx, out, snap)
		return
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		rs, err := parseFile(filepath.Join(w.Dir, e.Name()))
		if err != nil || rs.CWD == "" {
			continue
		}
		st := classify(rs)
		if prev, ok := snap.States[rs.CWD]; !ok || st > prev {
			snap.States[rs.CWD] = st
		}
	}
	send(ctx, out, snap)
}

func send(ctx context.Context, out chan<- Snapshot, snap Snapshot) {
	select {
	case out <- snap:
	case <-ctx.Done():
	}
}
