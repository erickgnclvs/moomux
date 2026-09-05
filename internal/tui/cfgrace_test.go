package tui

import (
	"sync"
	"testing"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// aliasingBackend's AddProject mutates the exact *config.Config handed to
// New(), mirroring App.AddProject mutating a.Cfg in place from whatever
// goroutine the owning tea.Cmd runs on (see App.Cfg's doc comment in
// internal/app/app.go) — the shape of the race this test guards against.
// fakeBackend's own AddProject mutates its private f.cfg instead, which is
// deliberately never aliased to a Model's m.cfg, so it can't reproduce this.
type aliasingBackend struct {
	fakeBackend
	live *config.Config
}

func (b *aliasingBackend) AddProject(name string, p config.Project) error {
	if b.live.Projects == nil {
		b.live.Projects = map[string]config.Project{}
	}
	b.live.Projects[name] = p
	return nil
}

func (b *aliasingBackend) ConfigSnapshot() config.Config { return *b.live }

// TestConfigRaceFreeAcrossMutationAndRender is the directed regression test
// for the data race between a config-mutating tea.Cmd closure (AddProject
// and its 5 siblings all share this shape — a goroutine Bubble Tea runs
// concurrently with Update()/View()) and View() reading m.cfg's fields
// unlocked from the event-loop goroutine. Before the fix, New() kept m.cfg
// as the exact same *config.Config the backend mutated in place —
// aliasingBackend reproduces that aliasing here. Run with -race, this trips
// WARNING: DATA RACE against the unfixed code (confirmed by temporarily
// reverting New()'s clone — see the git history of this file's commit) and
// is clean against the fix, since m.cfg is now memory only Update() ever
// writes to.
func TestConfigRaceFreeAcrossMutationAndRender(t *testing.T) {
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &aliasingBackend{live: cfg}
	m := New(cfg, be, testAgentOptions, make(chan watcher.Snapshot), func() {})
	m.width, m.height = 80, 24
	m.mode = ModeList

	// Both sides loop independently, for a fixed iteration count each,
	// rather than one gating on the other's completion — a writer that
	// races ahead of a much slower reader (or vice versa) must not starve
	// the overlap window down to zero. The reader ranges over m.cfg.Projects
	// directly (the same map the writer's AddProject writes to, via cfg —
	// aliased with m.cfg on the unfixed code) rather than going through
	// View(), which reliably masks races on unprotected memory: rendering a
	// frame passes through enough shared synchronization elsewhere (mutexes/
	// sync.Once in dependencies) that it produces spurious happens-before
	// edges between the two goroutines, which is a real hazard when writing
	// directed race tests — a race that would fire under a straight read
	// loop can go unreported once wrapped behind such calls, without the
	// underlying bug being fixed. Real call sites hit this same map read
	// directly and unguarded too (e.g. model.go's OrderedProjectNames, used
	// by refreshProjects on every mutation's resulting Msg).
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		// Mirrors what update.go's AddProject tea.Cmd closure does: call
		// the mutation, then fetch a snapshot for Update() to apply later —
		// all on this goroutine, concurrently with the read loop below.
		for i := 0; i < 20000; i++ {
			_ = be.AddProject("newproj", config.Project{Repo: "/tmp/newproj"})
			_ = be.ConfigSnapshot()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20000; i++ {
			for range m.cfg.Projects {
			}
		}
	}()
	wg.Wait()
}
