package app

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/tmux"
)

// overlapGit serializes access to the (unsynchronized) fake and records the
// most `git worktree add`s it saw running at once.
type overlapGit struct {
	mu         sync.Mutex
	inner      *fakeGitRunner
	inAdd, max atomic.Int32
}

func (g *overlapGit) Run(dir string, args ...string) (string, error) {
	if len(args) > 1 && args[0] == "worktree" && args[1] == "add" {
		n := g.inAdd.Add(1)
		defer g.inAdd.Add(-1)
		for m := g.max.Load(); n > m && !g.max.CompareAndSwap(m, n); m = g.max.Load() {
		}
		time.Sleep(20 * time.Millisecond)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inner.Run(dir, args...)
}

type lockedTmux struct {
	mu    sync.Mutex
	inner *fakeTmuxRunner
}

func (t *lockedTmux) Run(args ...string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inner.Run(args...)
}

// Creates overlap (the TUI no longer waits for one to finish before taking
// the next), but `git worktree add -b` writes the new branch's upstream into
// the repo's .git/config, so two in the same repo at once fail on
// config.lock. The adds have to take turns; nothing else does.
func TestConcurrentCreatesTakeTurnsAddingWorktrees(t *testing.T) {
	a, git, tm := newTestApp(t, gitProject("/repo"))
	og := &overlapGit{inner: git}
	a.Git = &gitwt.Client{Runner: og}
	a.Tmux = &tmux.Client{Runner: &lockedTmux{inner: tm}}
	for i := range 3 {
		name := fmt.Sprintf("s%d", i)
		noBranch(git, name)
		tm.out["list-panes -t ="+TmuxSessionName("demo:"+name, name)+":^ -F #{pane_id}"] = "%0\n"
	}

	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("s%d", i)
			if _, _, err := a.createSession("demo", name, "claude", "", "", false, boolPtr(false), "", "", ""); err != nil {
				t.Errorf("create %s: %v", name, err)
			}
		}()
	}
	wg.Wait()
	if got := og.max.Load(); got != 1 {
		t.Fatalf("%d worktree adds ran at once in one repo, want 1", got)
	}
}
