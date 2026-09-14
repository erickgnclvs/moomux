package tui

import (
	"testing"
	"time"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/sessionview"
)

func cfgTick(cfg config.Config, at time.Time) StatusTickMsg {
	return StatusTickMsg{Snap: sessionview.Snapshot{
		Views:    map[string]sessionview.View{},
		Cfg:      &cfg,
		PollTime: at,
	}}
}

// TestStreamedCfgLandsFromAnotherFrontEnd is the out-of-sync bug: config
// changes made by a *different* client of the same core — a project added
// in the Mac app, a theme switched in a second `moomux ui -socket` — only
// ever reached this model when it made them itself, so the list sat on a
// stale project set until the TUI was restarted.
func TestStreamedCfgLandsFromAnotherFrontEnd(t *testing.T) {
	be := &fakeBackend{}
	m := newTestModel(be)

	if got := len(m.projects); got != 1 {
		t.Fatalf("projects at start = %d, want 1", got)
	}

	remote := config.Config{
		Projects: map[string]config.Project{
			"demo":  {Repo: "/tmp/demo"},
			"added": {Repo: "/tmp/added"},
		},
		Order:   []string{"demo", "added"},
		Folders: map[string]config.FolderMeta{"shipping": {Collapsed: true}},
		Theme:   "gruvbox",
	}
	m.Update(cfgTick(remote, time.Now()))

	if got := m.projects; len(got) != 2 || got[0] != "demo" || got[1] != "added" {
		t.Errorf("projects after streamed cfg = %v, want [demo added]", got)
	}
	if !m.cfg.Folders["shipping"].Collapsed {
		t.Errorf("folder table not applied: %+v", m.cfg.Folders)
	}
	if m.cfg.Theme != "gruvbox" {
		t.Errorf("theme = %q, want gruvbox", m.cfg.Theme)
	}
}

// TestStreamedCfgDoesNotRevertOurOwnWrite covers the other half: a snapshot
// is built on the core's own timer, so one built moments *before* a local
// mutation can arrive just after it. Taken literally it would undo what the
// user just did until the next tick.
func TestStreamedCfgDoesNotRevertOurOwnWrite(t *testing.T) {
	be := &fakeBackend{}
	m := newTestModel(be)

	built := time.Now()
	fresh := config.Config{
		Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}},
		Theme:    "catppuccin",
	}
	m.applyCfg(&fresh)

	stale := config.Config{
		Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}},
		Theme:    "",
	}
	m.Update(cfgTick(stale, built))

	if m.cfg.Theme != "catppuccin" {
		t.Errorf("theme = %q, want catppuccin — a pre-write snapshot reverted a local mutation", m.cfg.Theme)
	}

	// The next snapshot, built after the write, is authoritative again.
	m.Update(cfgTick(stale, time.Now()))
	if m.cfg.Theme != "" {
		t.Errorf("theme = %q, want the core's answer once the snapshot post-dates our write", m.cfg.Theme)
	}
}

// TestSettingsToggleSurvivesAnInFlightSnapshot is the bug the streamed
// config introduced and the review caught. applySettingsRow writes m.cfg
// directly and persists it with a *blocking* call, which over a socket is a
// round trip the core keeps emitting snapshots throughout. Without stamping
// cfgAppliedAt, the snapshot built just before the toggle lands just after
// it and flips the setting back.
func TestSettingsToggleSurvivesAnInFlightSnapshot(t *testing.T) {
	row := -1
	for i, r := range settingsRows {
		if r.kind == settingsRowToggle && r.get(&config.Config{SortRecentFirst: true}) {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("no sort-mode toggle row found")
	}

	be := &fakeBackend{}
	m := newTestModel(be)

	// A snapshot the core built before the keypress, delivered after it.
	built := time.Now()
	m.applySettingsRow(row)
	if !m.cfg.SortRecentFirst {
		t.Fatal("toggle did not take effect at all")
	}

	stale := config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	m.Update(cfgTick(stale, built))

	if !m.cfg.SortRecentFirst {
		t.Error("a snapshot built before the toggle reverted it")
	}
}

// TestStreamedCfgKeepsTheActiveProject: activeProj is an index into
// m.projects, so another front end deleting a project that sorts earlier
// would slide the user onto a different project with no keypress. Re-anchor
// by name, the way ProjectMovedMsg already does.
func TestStreamedCfgKeepsTheActiveProject(t *testing.T) {
	be := &fakeBackend{}
	m := newTestModel(be)
	m.cfg.Projects = map[string]config.Project{"alpha": {}, "beta": {}, "gamma": {}}
	m.cfg.Order = []string{"alpha", "beta", "gamma"}
	m.refreshProjects()

	m.activeProj = indexOfProject(m.projects, "gamma")
	if m.activeProj < 0 {
		t.Fatal("gamma not in the project list")
	}

	// Another front end removes the project sitting before ours.
	remote := config.Config{
		Projects: map[string]config.Project{"beta": {}, "gamma": {}},
		Order:    []string{"beta", "gamma"},
	}
	m.Update(cfgTick(remote, time.Now()))

	if got := m.projectAt(m.activeProj); got != "gamma" {
		t.Errorf("active project = %q after another front end removed an earlier one, want gamma", got)
	}
}
