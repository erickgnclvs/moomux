package watcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fakeSQLite3 puts a counting stub for sqlite3 on PATH. It appends a line to
// the returned counter file per invocation, so a test can assert how many
// subprocesses were actually spawned, and prints rows.
func fakeSQLite3(t *testing.T, rows string) (counter string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	binDir := t.TempDir()
	counter = filepath.Join(binDir, "calls")
	script := "#!/bin/sh\necho x >> " + counter + "\nprintf '%s'  '" + rows + "'\n"
	if err := os.WriteFile(filepath.Join(binDir, "sqlite3"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return counter
}

func callCount(t *testing.T, counter string) int {
	t.Helper()
	b, err := os.ReadFile(counter)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}

// TestQueryCachedSkipsUnchangedDB is the CPU fix: the sqlite3 subprocess
// (~4ms of fork and exec) must not be spawned again for a database that
// hasn't been written since the last tick.
func TestQueryCachedSkipsUnchangedDB(t *testing.T) {
	counter := fakeSQLite3(t, "/wt/a\t1700000000000\n")

	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	if err := os.WriteFile(dbPath, []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &SQLiteWatcher{DB: dbPath, Query: "irrelevant"}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		rows, err := w.queryCached(ctx, dbPath)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if rows["/wt/a"] != 1700000000000 {
			t.Fatalf("call %d: got %v, want the queried row", i, rows)
		}
	}
	if n := callCount(t, counter); n != 1 {
		t.Errorf("spawned sqlite3 %d times for an unchanged DB, want 1", n)
	}
}

// TestQueryCachedRereadsChangedDB covers both ways a real database announces
// a write: an in-place change to the main file, and — the WAL case, which is
// how SQLite actually commits — an untouched main file beside a changed -wal.
func TestQueryCachedRereadsChangedDB(t *testing.T) {
	counter := fakeSQLite3(t, "/wt/a\t1700000000000\n")

	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	if err := os.WriteFile(dbPath, []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &SQLiteWatcher{DB: dbPath, Query: "irrelevant"}
	ctx := context.Background()

	if _, err := w.queryCached(ctx, dbPath); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(dbPath, []byte("db-grown"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.queryCached(ctx, dbPath); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, counter); n != 2 {
		t.Fatalf("after rewriting the DB: %d sqlite3 calls, want 2", n)
	}

	// A WAL commit leaves the main DB file alone. Missing this would make
	// the watcher blind to every write a WAL-mode agent makes between
	// checkpoints.
	if err := os.WriteFile(dbPath+"-wal", []byte("frames"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.queryCached(ctx, dbPath); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, counter); n != 3 {
		t.Errorf("after a -wal write: %d sqlite3 calls, want 3", n)
	}
}

// TestTickDecaysFromCachedRows guards the reason SQLiteWatcher polls on a
// timer at all: Working -> Done is a time-based transition with no
// filesystem event behind it, so reusing a cached query result must not
// freeze a session as Working forever.
func TestTickDecaysFromCachedRows(t *testing.T) {
	now := time.Now()
	fresh := now.UnixMilli()
	fakeSQLite3(t, "/wt/a\t"+strconv.FormatInt(fresh, 10)+"\n")

	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	if err := os.WriteFile(dbPath, []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &SQLiteWatcher{DB: dbPath, Query: "irrelevant"}
	ctx := context.Background()
	out := make(chan Snapshot, 2)

	w.tick(ctx, out, time.Hour) // row is well within ActiveAge
	if snap := <-out; snap.States["/wt/a"] != Working {
		t.Fatalf("got %v, want Working", snap.States["/wt/a"])
	}

	// Same DB, same stamp — so the rows come from the cache — but an
	// ActiveAge that the row no longer falls inside.
	w.tick(ctx, out, time.Nanosecond)
	if snap := <-out; snap.States["/wt/a"] != Done {
		t.Errorf("got %v, want Done — decay must still be computed per tick", snap.States["/wt/a"])
	}
}
