package watcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestQuerySQLiteRespectsContextCancellation replaces "sqlite3" on PATH with
// a fake that sleeps far longer than the query context's timeout. Without
// exec.CommandContext, canceling ctx doesn't touch the already-started
// subprocess and querySQLite blocks for the full sleep; with it, the
// subprocess is killed and querySQLite returns as soon as the context
// expires.
func TestQuerySQLiteRespectsContextCancellation(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "sqlite3")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := querySQLite(ctx, "irrelevant.db", "SELECT 1"); err == nil {
		t.Fatal("expected error once context expired")
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("querySQLite took %v, want to return shortly after the context timeout", elapsed)
	}
}

// TestQuerySQLiteSetsBusyTimeout guards against the "database is locked"
// flake (sqlite3 CLI exits 5/SQLITE_BUSY when Codex itself holds a write
// lock on state_N.sqlite at poll time): querySQLite must ask sqlite3 to wait
// for the lock via PRAGMA busy_timeout instead of failing immediately. The
// fake sqlite3 here simulates a would-be-busy DB by only succeeding when it
// sees a busy_timeout pragma among its args.
func TestQuerySQLiteSetsBusyTimeout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "sqlite3")
	script := `#!/bin/sh
for a in "$@"; do
  case "$a" in
    *busy_timeout*) echo "/tmp/wt-a	1"; exit 0 ;;
  esac
done
exit 5
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	rows, err := querySQLite(context.Background(), "irrelevant.db", "SELECT 1")
	if err != nil {
		t.Fatalf("querySQLite returned error, want busy_timeout to avoid SQLITE_BUSY: %v", err)
	}
	if rows["/tmp/wt-a"] != 1 {
		t.Fatalf("got %v, want row for /tmp/wt-a", rows)
	}
}

// TestSQLiteWatcherMarkerDirWinsOverStaleRow replaces "sqlite3" on PATH with
// a fake that reports a stale (long-idle) row for /tmp/wt-a, which alone
// would classify as Waiting. A codexhook marker for the same cwd must still
// win in the same tick — mirroring DirWatcher's Claude-side guarantee that a
// stale native status write never hides a needs-input marker.
func TestSQLiteWatcherMarkerDirWinsOverStaleRow(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "sqlite3")
	script := "#!/bin/sh\necho \"/tmp/wt-a\t1\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "state.sqlite")
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	markerDir := t.TempDir()
	writeJSON(t, filepath.Join(markerDir, "sess.json"), map[string]any{
		"cwd":    "/tmp/wt-a",
		"status": "needs-input",
	})

	w := &SQLiteWatcher{DB: dbPath, Query: "irrelevant", MarkerDir: markerDir, Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if got := snap.States["/tmp/wt-a"]; got != NeedsInput {
			t.Fatalf("got %v, want NeedsInput", got)
		}
	case <-ctx.Done():
		t.Fatal("no snapshot received")
	}
}

// TestSQLiteWatcherSkipsZeroLengthDB covers a stale ~/.codex/state_0.sqlite
// left over from a session that never wrote to it: the glob still matches
// it, but it has never been opened by sqlite3 (no schema), so querying it
// would surface a permanent "no such table: sessions" error on every tick.
// It must be skipped instead, while a real, populated DB alongside it still
// reports normally.
func TestSQLiteWatcherSkipsZeroLengthDB(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "sqlite3")
	script := `#!/bin/sh
for a in "$@"; do
  case "$a" in
    *state_0.sqlite) echo "no such table: sessions" 1>&2; exit 1 ;;
  esac
done
echo "/tmp/wt-a	$(($(date +%s) * 1000))"
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dbDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dbDir, "state_0.sqlite"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbDir, "state_5.sqlite"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &SQLiteWatcher{DB: filepath.Join(dbDir, "state_*.sqlite"), Query: "irrelevant", Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.Err != nil {
			t.Fatalf("got Err %v, want nil (zero-length DB should be skipped, not queried)", snap.Err)
		}
		if got := snap.States["/tmp/wt-a"]; got != Working {
			t.Fatalf("got %v, want Working from the populated DB", got)
		}
	case <-ctx.Done():
		t.Fatal("no snapshot received")
	}
}

// TestQuerySQLiteTreatsNoSuchTableAsEmpty covers a DB file that exists (and
// isn't zero-length) but whose schema Codex hasn't created yet: sqlite3
// exits nonzero with "no such table" on stderr, which must be treated as an
// empty result rather than a query error.
func TestQuerySQLiteTreatsNoSuchTableAsEmpty(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "sqlite3")
	script := "#!/bin/sh\necho 'Error: no such table: sessions' 1>&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	rows, err := querySQLite(context.Background(), "irrelevant.db", "SELECT 1")
	if err != nil {
		t.Fatalf("querySQLite returned error, want no such table treated as empty: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %v, want empty result", rows)
	}
}

// TestSQLiteWatcherMarkerDirWithoutDB covers a needs-input hook firing
// before Codex's own state db exists yet (e.g. very first launch): the DB
// glob matching zero files must not skip the MarkerDir scan.
func TestSQLiteWatcherMarkerDirWithoutDB(t *testing.T) {
	markerDir := t.TempDir()
	writeJSON(t, filepath.Join(markerDir, "sess.json"), map[string]any{
		"cwd":    "/tmp/wt-a",
		"status": "needs-input",
	})

	w := &SQLiteWatcher{
		DB:        filepath.Join(t.TempDir(), "nonexistent-*.sqlite"),
		Query:     "irrelevant",
		MarkerDir: markerDir,
		Interval:  10 * time.Millisecond,
	}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if got := snap.States["/tmp/wt-a"]; got != NeedsInput {
			t.Fatalf("got %v, want NeedsInput", got)
		}
	case <-ctx.Done():
		t.Fatal("no snapshot received")
	}
}
