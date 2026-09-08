package session

import (
	"fmt"
	"path/filepath"
	"sync"
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

// TestConcurrentWriterSurvivesMutation simulates a second moomux process
// (e.g. `moomux spawn`) adding a session to the same store file while a
// first process's Store still holds an older in-memory snapshot. Without
// reloading before mutating, the first process's later write would
// serialize its stale snapshot back out and silently drop the second
// process's session.
func TestConcurrentWriterSurvivesMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	first := &Store{Path: path}
	_ = first.Load()
	if err := first.Put(Session{ID: "p:a", Project: "p", Name: "a", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	second := &Store{Path: path}
	_ = second.Load()
	if err := second.Put(Session{ID: "p:b", Project: "p", Name: "b", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	// first still only knows about "a" in memory; this mutation must not
	// clobber "b", which second wrote to disk after first last loaded.
	if _, err := first.SetArchived("p:a", true); err != nil {
		t.Fatal(err)
	}

	check := &Store{Path: path}
	if err := check.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := check.Get("p:b"); !ok {
		t.Fatalf("session %q written by a concurrent Store was lost", "p:b")
	}
	if len(check.All()) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %+v", len(check.All()), check.All())
	}
}

// TestConcurrentSavesDoNotRaceOnTempFile simulates several separate moomux
// processes (each its own *Store, as spawn/tag/delete would be run from
// different processes sharing one sessions.json) saving at the same time.
// A shared fixed ".tmp" name lets one process's rename steal or delete
// another's in-flight temp file out from under it, surfacing as a
// "no such file" error from Put; a per-invocation temp file must not.
func TestConcurrentSavesDoNotRaceOnTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	const writers = 8
	const rounds = 20
	var wg sync.WaitGroup
	errCh := make(chan error, writers*rounds)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			st := &Store{Path: path}
			for r := 0; r < rounds; r++ {
				id := fmt.Sprintf("p:w%d-r%d", w, r)
				if err := st.Put(Session{ID: id, Project: "p", Name: id, CreatedAt: time.Now()}); err != nil {
					errCh <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Put failed: %v", err)
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

func TestSortByRecentOrdersByLastOpenedDescFallingBackToCreatedAt(t *testing.T) {
	t0 := time.Now()
	sessions := []Session{
		{ID: "manual-first", CreatedAt: t0.Add(-2 * time.Hour), LastOpened: t0.Add(-time.Minute)},
		{ID: "never-opened-old", CreatedAt: t0.Add(-3 * time.Hour)},
		{ID: "never-opened-new", CreatedAt: t0.Add(-time.Hour)},
		{ID: "opened-most-recently", CreatedAt: t0.Add(-4 * time.Hour), LastOpened: t0},
	}
	SortByRecent(sessions)

	want := []string{"opened-most-recently", "manual-first", "never-opened-new", "never-opened-old"}
	for i, id := range want {
		if sessions[i].ID != id {
			t.Fatalf("position %d: want %s, got %s (order: %v)", i, id, sessions[i].ID, ids(sessions))
		}
	}
}

func ids(sessions []Session) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.ID
	}
	return out
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

// TestReorderPreservesConcurrentFieldChange simulates a caller that
// fetched its session slice (e.g. via ByProject) before a second moomux
// process tagged one of those sessions with a PR link. Reorder must not
// clobber that PR field with the caller's stale snapshot — it should only
// touch Order.
func TestReorderPreservesConcurrentFieldChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	s := &Store{Path: path}
	_ = s.Load()
	t0 := time.Now()
	_ = s.Put(Session{ID: "a", Project: "p", CreatedAt: t0.Add(-time.Hour)})
	_ = s.Put(Session{ID: "b", Project: "p", CreatedAt: t0})

	stale := s.ByProject("p")

	other := &Store{Path: path}
	_ = other.Load()
	sessA, _ := other.Get("a")
	sessA.PR = "https://github.com/example/repo/pull/1"
	if err := other.Put(sessA); err != nil {
		t.Fatal(err)
	}

	if err := s.Reorder(stale); err != nil {
		t.Fatal(err)
	}

	check := &Store{Path: path}
	_ = check.Load()
	got, _ := check.Get("a")
	if got.PR != "https://github.com/example/repo/pull/1" {
		t.Fatalf("Reorder clobbered concurrently-set PR, got %q", got.PR)
	}
	if got.Order == 0 {
		t.Fatalf("expected Order to be set by Reorder")
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

// Store.All() ranges a map, so a comparator that lets two sessions tie
// returns them in a different order on every call — which makes every Watch
// snapshot look reordered to a client. Fresh Store loads per iteration,
// since Go randomizes map iteration per range, not per map.
func TestAllIsDeterministicWhenOrderAndCreatedAtTie(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	created := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	seed := &Store{Path: path}
	_ = seed.Load()
	for i := range 30 {
		_ = seed.Put(Session{ID: fmt.Sprintf("p:s%02d", i), Project: "p", CreatedAt: created})
	}

	var want []string
	for range 20 {
		s := &Store{Path: path}
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		got := ids(s.All())
		if want == nil {
			want = got
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("order changed between loads:\n want %v\n got  %v", want, got)
			}
		}
	}
}

func TestSortByRecentIsDeterministicWhenEveryFieldTies(t *testing.T) {
	created := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	var want []string
	for shift := range 5 {
		sessions := make([]Session, 0, 6)
		for i := range 6 {
			// Feed a different starting permutation each pass: a stable sort
			// on a comparator with ties just preserves whatever it was given.
			sessions = append(sessions, Session{ID: fmt.Sprintf("s%d", (i+shift)%6), CreatedAt: created})
		}
		SortByRecent(sessions)
		got := ids(sessions)
		if want == nil {
			want = got
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("order depends on input permutation:\n want %v\n got  %v", want, got)
			}
		}
	}
}
