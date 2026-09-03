package tui

import (
	"testing"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// TestNewSelectsFirstProjectWithActiveSessions is the regression test for the
// startup bug where a config-order-first project with nothing going on
// (e.g. "moomux" itself, added early and rarely used directly) always won
// out over a later project that actually has active work, since m.activeProj
// defaulted to 0 regardless of what the projects actually contained.
func TestNewSelectsFirstProjectWithActiveSessions(t *testing.T) {
	cfg := &config.Config{
		Order: []string{"moomux", "other"},
		Projects: map[string]config.Project{
			"moomux": {Repo: "/tmp/moomux"},
			"other":  {Repo: "/tmp/other"},
		},
	}
	be := &fakeBackend{sessions: []session.Session{
		{ID: "other:work", Project: "other", Name: "work"},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})

	if got := m.projects[m.activeProj]; got != "other" {
		t.Fatalf("active project = %q, want %q (the one with an active session)", got, "other")
	}
}

// TestNewFallsBackToFirstProjectWhenNoneHaveActiveSessions guards the
// no-active-sessions-anywhere case: activeProj must still land on a valid
// index (project 0) rather than get stuck unset or out of range.
func TestNewFallsBackToFirstProjectWhenNoneHaveActiveSessions(t *testing.T) {
	cfg := &config.Config{
		Order: []string{"moomux", "other"},
		Projects: map[string]config.Project{
			"moomux": {Repo: "/tmp/moomux"},
			"other":  {Repo: "/tmp/other"},
		},
	}
	be := &fakeBackend{}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})

	if got := m.projects[m.activeProj]; got != "moomux" {
		t.Fatalf("active project = %q, want %q (fallback to first)", got, "moomux")
	}
}
