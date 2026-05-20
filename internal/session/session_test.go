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

func TestMakeID(t *testing.T) {
	if got := MakeID("eg_system", "hash-password"); got != "eg_system:hash-password" {
		t.Fatalf("got %q", got)
	}
}
