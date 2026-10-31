package watcher

import (
	"context"
	"errors"
)

// MultiWatcher fans the output of multiple Watchers into one Snapshot channel.
//
// Each emitted Snapshot is the merge of every watcher's latest one, taking
// the highest-ranked State per path. Passing snapshots through one by one
// let two agents that know the same worktree (a Codex thread left in a
// worktree now running Claude) overwrite each other downstream, so the
// session's state flipped with whichever watcher ticked last.
type MultiWatcher struct {
	Watchers []Watcher
}

type indexed struct {
	i    int
	snap Snapshot
}

func (m *MultiWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	if len(m.Watchers) == 0 {
		<-ctx.Done()
		return
	}
	merged := make(chan indexed, len(m.Watchers)*4)
	for i, w := range m.Watchers {
		ch := make(chan Snapshot)
		go w.Run(ctx, ch)
		go func() {
			for {
				select {
				case snap := <-ch:
					select {
					case merged <- indexed{i, snap}:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	latest := make([]Snapshot, len(m.Watchers))
	for {
		select {
		case <-ctx.Done():
			return
		case in := <-merged:
			if in.snap.Err != nil {
				// States may be incomplete on an error; keep what this
				// watcher last knew for the paths it couldn't read.
				states := make(map[string]State, len(latest[in.i].States)+len(in.snap.States))
				for p, st := range latest[in.i].States {
					states[p] = st
				}
				for p, st := range in.snap.States {
					states[p] = st
				}
				in.snap.States = states
			}
			latest[in.i] = in.snap
			send(ctx, out, mergeLatest(latest, in.snap))
		}
	}
}

// mergeLatest max-merges every watcher's latest States, joins their errors,
// and stamps the result with the snapshot that triggered it.
func mergeLatest(latest []Snapshot, trigger Snapshot) Snapshot {
	snap := Snapshot{States: map[string]State{}, PollTime: trigger.PollTime}
	var errs []error
	for _, l := range latest {
		for p, st := range l.States {
			if prev, ok := snap.States[p]; !ok || st > prev {
				snap.States[p] = st
			}
		}
		if l.Err != nil {
			errs = append(errs, l.Err)
		}
	}
	snap.Err = errors.Join(errs...)
	return snap
}
