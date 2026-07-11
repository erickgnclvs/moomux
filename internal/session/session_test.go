package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	s := &Store{Path: path}
	if err := s.Load(); err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(s.All()) != 0 {
		t.Fatalf("expected empty")
	}

	sess := Session{
		ID:           "eg:hash",
		Project:      "eg",
		Name:         "hash",
		Branch:       "erickgoncalves/hash",
		WorktreePath: "/tmp/wt",
		TmuxSession:  "moomux-hash",
		CreatedAt:    time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC),
	}
	if err := s.Put(sess); err != nil {
		t.Fatal(err)
	}

	s2 := &Store{Path: path}
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get("eg:hash")
	if !ok {
		t.Fatalf("missing after reload")
	}
	if got.Branch != "erickgoncalves/hash" {
		t.Fatalf("branch = %q", got.Branch)
	}
}

func TestStoreDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	s := &Store{Path: path}
	_ = s.Load()
	_ = s.Put(Session{ID: "a", Project: "p", Name: "a", CreatedAt: time.Now()})
	_ = s.Put(Session{ID: "b", Project: "p", Name: "b", CreatedAt: time.Now()})
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatalf("a still present")
	}
	if len(s.ByProject("p")) != 1 {
		t.Fatalf("expected 1, got %d", len(s.ByProject("p")))
	}
}

func TestSetArchivedTogglesAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	s := &Store{Path: path}
	_ = s.Load()
	_ = s.Put(Session{ID: "a", Project: "p", Name: "a", CreatedAt: time.Now()})

	if _, err := s.SetArchived("a", true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("a")
	if !got.Archived {
		t.Fatalf("expected archived")
	}

	s2 := &Store{Path: path}
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	got2, _ := s2.Get("a")
	if !got2.Archived {
		t.Fatalf("archived flag not persisted across reload")
	}

	if _, err := s2.SetArchived("a", false); err != nil {
		t.Fatal(err)
	}
	got3, _ := s2.Get("a")
	if got3.Archived {
		t.Fatalf("expected restored (not archived)")
	}

	if _, err := s2.SetArchived("missing", true); err == nil {
		t.Fatalf("expected error for unknown session")
	}
}

func TestAllSortedByCreatedDesc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	s := &Store{Path: path}
	_ = s.Load()
	t0 := time.Now()
	_ = s.Put(Session{ID: "older", CreatedAt: t0.Add(-time.Hour)})
	_ = s.Put(Session{ID: "newer", CreatedAt: t0})
	all := s.All()
	if all[0].ID != "newer" {
		t.Fatalf("expected newer first, got %s", all[0].ID)
	}
}

func TestReorderPersistsAndOverridesCreatedAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	s := &Store{Path: path}
	_ = s.Load()
	t0 := time.Now()
	_ = s.Put(Session{ID: "a", Project: "p", CreatedAt: t0.Add(-time.Hour)})
	_ = s.Put(Session{ID: "b", Project: "p", CreatedAt: t0})

	// Without a manual order, "b" (newer) sorts first.
	all := s.ByProject("p")
	if all[0].ID != "b" {
		t.Fatalf("expected b first before reorder, got %s", all[0].ID)
	}

	// Move "a" to the front and persist.
	all[0], all[1] = all[1], all[0]
	if err := s.Reorder(all); err != nil {
		t.Fatal(err)
	}
	if got := s.ByProject("p"); got[0].ID != "a" {
		t.Fatalf("expected a first after reorder, got %s", got[0].ID)
	}

	// Order survives a reload.
	s2 := &Store{Path: path}
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	if got := s2.ByProject("p"); got[0].ID != "a" {
		t.Fatalf("expected a first after reload, got %s", got[0].ID)
	}
}

func TestUnorderedSessionSortsBeforeReorderedPeers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	s := &Store{Path: path}
	_ = s.Load()
	t0 := time.Now()
	_ = s.Put(Session{ID: "a", Project: "p", CreatedAt: t0.Add(-time.Hour)})
	_ = s.Put(Session{ID: "b", Project: "p", CreatedAt: t0})
	if err := s.Reorder(s.ByProject("p")); err != nil {
		t.Fatal(err)
	}

	// A freshly created session (Order unset) should land ahead of any
	// explicitly ordered peer, mirroring today's "newest on top" default.
	_ = s.Put(Session{ID: "c", Project: "p", CreatedAt: t0.Add(time.Minute)})
	got := s.ByProject("p")
	if got[0].ID != "c" {
		t.Fatalf("expected unordered c first, got %s", got[0].ID)
	}
}

func TestMakeID(t *testing.T) {
	if got := MakeID("eg_system", "hash-password"); got != "eg_system:hash-password" {
		t.Fatalf("got %q", got)
	}
}

func TestSessionAgentNameDefaultsToClaude(t *testing.T) {
	s := Session{}
	if got := s.AgentName(); got != "claude" {
		t.Fatalf("expected claude, got %q", got)
	}
}

func TestSessionAgentNameReturnsSetValue(t *testing.T) {
	tests := []string{"codex", "opencode"}
	for _, agent := range tests {
		s := Session{Agent: agent}
		if got := s.AgentName(); got != agent {
			t.Fatalf("expected %q, got %q", agent, got)
		}
	}
}

func TestSessionAgentPortRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	s := &Store{Path: path}
	if err := s.Load(); err != nil {
		t.Fatalf("load empty: %v", err)
	}

	sess := Session{
		ID:        "eg:hash",
		Project:   "eg",
		Name:      "hash",
		CreatedAt: time.Now(),
		Agent:     "opencode",
		AgentPort: 8080,
	}
	if err := s.Put(sess); err != nil {
		t.Fatal(err)
	}

	s2 := &Store{Path: path}
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get("eg:hash")
	if !ok {
		t.Fatalf("missing after reload")
	}
	if got.Agent != "opencode" {
		t.Fatalf("Agent = %q", got.Agent)
	}
	if got.AgentPort != 8080 {
		t.Fatalf("AgentPort = %d", got.AgentPort)
	}
}
