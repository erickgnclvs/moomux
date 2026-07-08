package watcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// hasSQLite3 returns true if the sqlite3 CLI is available.
func hasSQLite3() bool {
	_, err := exec.LookPath("sqlite3")
	return err == nil
}

// createTestDB creates a temporary SQLite DB with a simple schema.
func createTestDB(t *testing.T, rows []struct {
	cwd       string
	updatedMs int64
}) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	stmts := "CREATE TABLE threads (cwd TEXT, updated_at_ms INTEGER);"
	for _, r := range rows {
		stmts += fmt.Sprintf("INSERT INTO threads VALUES ('%s', %d);", r.cwd, r.updatedMs)
	}
	cmd := exec.Command("sqlite3", dbPath, stmts)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test db: %v: %s", err, out)
	}
	return dbPath
}

func TestSQLiteWatcherWorking(t *testing.T) {
	if !hasSQLite3() {
		t.Skip("sqlite3 CLI not available")
	}
	now := time.Now().UnixMilli()
	dbPath := createTestDB(t, []struct {
		cwd       string
		updatedMs int64
	}{
		{"/tmp/proj", now - 2000}, // 2 seconds ago — within 10s ActiveAge
	})

	w := &SQLiteWatcher{
		DB:        dbPath,
		Query:     "SELECT cwd, MAX(updated_at_ms) FROM threads GROUP BY cwd",
		ActiveAge: 10 * time.Second,
		Interval:  10 * time.Millisecond,
	}
	ch := make(chan Snapshot, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.States["/tmp/proj"] != Working {
			t.Fatalf("expected Working, got %v", snap.States["/tmp/proj"])
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for snapshot")
	}
}

func TestSQLiteWatcherWaiting(t *testing.T) {
	if !hasSQLite3() {
		t.Skip("sqlite3 CLI not available")
	}
	now := time.Now().UnixMilli()
	dbPath := createTestDB(t, []struct {
		cwd       string
		updatedMs int64
	}{
		{"/tmp/proj", now - 60000}, // 60 seconds ago — stale
	})

	w := &SQLiteWatcher{
		DB:        dbPath,
		Query:     "SELECT cwd, MAX(updated_at_ms) FROM threads GROUP BY cwd",
		ActiveAge: 10 * time.Second,
		Interval:  10 * time.Millisecond,
	}
	ch := make(chan Snapshot, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.States["/tmp/proj"] != Waiting {
			t.Fatalf("expected Waiting, got %v", snap.States["/tmp/proj"])
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for snapshot")
	}
}

func TestSQLiteWatcherMissingDB(t *testing.T) {
	w := &SQLiteWatcher{
		DB:       "/nonexistent/path/to.db",
		Query:    "SELECT cwd, MAX(updated_at_ms) FROM threads GROUP BY cwd",
		Interval: 10 * time.Millisecond,
	}
	ch := make(chan Snapshot, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if len(snap.States) != 0 {
			t.Fatalf("expected empty snapshot for missing DB, got %v", snap.States)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for snapshot")
	}
}

func TestSQLiteWatcherGlob(t *testing.T) {
	if !hasSQLite3() {
		t.Skip("sqlite3 CLI not available")
	}
	now := time.Now().UnixMilli()
	dir := t.TempDir()

	// Create two DB files matching a glob pattern
	for i, path := range []string{"/tmp/proj-a", "/tmp/proj-b"} {
		dbPath := filepath.Join(dir, fmt.Sprintf("state_%d.sqlite", i))
		stmts := fmt.Sprintf(
			"CREATE TABLE threads (cwd TEXT, updated_at_ms INTEGER); INSERT INTO threads VALUES ('%s', %d);",
			path, now-1000,
		)
		if out, err := exec.Command("sqlite3", dbPath, stmts).CombinedOutput(); err != nil {
			t.Fatalf("create db %d: %v: %s", i, err, out)
		}
	}

	w := &SQLiteWatcher{
		DB:        filepath.Join(dir, "state_*.sqlite"),
		Query:     "SELECT cwd, MAX(updated_at_ms) FROM threads GROUP BY cwd",
		ActiveAge: 10 * time.Second,
		Interval:  10 * time.Millisecond,
	}
	ch := make(chan Snapshot, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	// Collect snapshots until we see both paths or timeout
	seen := map[string]State{}
	for len(seen) < 2 {
		select {
		case snap := <-ch:
			for k, v := range snap.States {
				seen[k] = v
			}
		case <-ctx.Done():
			t.Fatalf("timed out; only saw: %v", seen)
		}
	}
	for _, path := range []string{"/tmp/proj-a", "/tmp/proj-b"} {
		if seen[path] != Working {
			t.Errorf("expected Working for %s, got %v", path, seen[path])
		}
	}
	_ = os.RemoveAll(dir)
}
