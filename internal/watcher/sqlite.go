package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SQLiteWatcher polls an SQLite database for agent activity — this is what
// backs OpenCode status (~/.local/share/opencode/opencode.db via the
// sqlite3 CLI); an earlier OpenCodeWatcher was superseded by this generic
// implementation. Query must return two columns: (path TEXT, updated_ms
// INTEGER) where updated_ms is a Unix timestamp in milliseconds.
//
// Run is defined per-platform (sqlite_poll.go / sqlite_fsevents.go): unlike
// DirWatcher, the periodic tick itself can't be replaced by filesystem
// events even on darwin, because ActiveAge decay (Working -> Done once a
// session has been quiet for a while) is a time-based transition with no
// corresponding filesystem event. macOS instead watches the DB directory and
// MarkerDir and fires an extra out-of-cycle tick on change, so a session
// going busy (or a needs-input marker appearing) shows up immediately
// instead of waiting out the rest of the current interval; the interval
// itself is unchanged so decay is still caught on schedule.
type SQLiteWatcher struct {
	DB        string        // exact path or glob (e.g. ~/.codex/state_*.sqlite)
	Query     string        // SELECT path_col, updated_ms_col FROM ... GROUP BY path_col
	ActiveAge time.Duration // within this age = Working; default 10s
	Interval  time.Duration // poll interval; default 2s
	// MarkerDir, if set, is scanned each tick for the *.json needs-input
	// marker files internal/codexhook writes and clears (see dirScanner.scan). The
	// merge must happen in the same tick as the SQLite query: NeedsInput and
	// Working both come from Codex here, and update.go's per-watcher
	// last-snapshot-wins semantics mean combining them across separate
	// snapshots would let whichever tick lands last clobber the other.
	MarkerDir string

	markerScanner dirScanner

	rowsMu    sync.Mutex
	rowsCache map[string]cachedRows // by db path
}

// cachedRows is one DB's last query result, valid as long as dbStamp still
// matches. Every tick re-derives Working/Done from these rows against the
// current time, so the ActiveAge decay this watcher exists to catch is
// unaffected by reusing them — only the sqlite3 subprocess is skipped.
type cachedRows struct {
	stamp string
	rows  map[string]int64
}

// dbStamp fingerprints a SQLite database as (size, mtime) of the main file
// and its write-ahead log. In WAL mode a commit appends to <db>-wal and
// leaves the main file untouched until a checkpoint, so watching the main
// file alone would miss every write; -shm is deliberately excluded because
// it changes when a *reader* connects, which would include our own query
// and defeat the cache entirely.
func dbStamp(dbPath string) string {
	var b strings.Builder
	for _, p := range [2]string{dbPath, dbPath + "-wal"} {
		fi, err := os.Stat(p)
		if err != nil {
			b.WriteString("-|")
			continue
		}
		fmt.Fprintf(&b, "%d:%d|", fi.Size(), fi.ModTime().UnixNano())
	}
	return b.String()
}

// queryCached is querySQLite with the subprocess skipped when the database
// hasn't changed since the last tick. The sqlite3 CLI costs ~4ms of fork,
// exec and page-in per call, paid every Interval per matched DB (plus an
// extra out-of-cycle tick per debounced filesystem event on darwin) — for a
// database that, between agent turns, is byte-for-byte the one already
// queried.
func (w *SQLiteWatcher) queryCached(ctx context.Context, dbPath string) (map[string]int64, error) {
	stamp := dbStamp(dbPath)

	w.rowsMu.Lock()
	prev, ok := w.rowsCache[dbPath]
	w.rowsMu.Unlock()
	if ok && prev.stamp == stamp {
		return prev.rows, nil
	}

	rows, err := querySQLite(ctx, dbPath, w.Query)
	if err != nil {
		return nil, err
	}

	w.rowsMu.Lock()
	if w.rowsCache == nil {
		w.rowsCache = map[string]cachedRows{}
	}
	w.rowsCache[dbPath] = cachedRows{stamp: stamp, rows: rows}
	w.rowsMu.Unlock()
	return rows, nil
}

func (w *SQLiteWatcher) tick(ctx context.Context, out chan<- Snapshot, activeAge time.Duration) {
	snap := Snapshot{States: map[string]State{}, PollTime: time.Now()}

	dbPaths, err := filepath.Glob(w.DB)
	if err != nil {
		snap.Err = fmt.Errorf("glob %s: %w", w.DB, err)
		send(ctx, out, snap)
		return
	}
	// No matching DB yet (e.g. agent hasn't started) isn't an error, and
	// mustn't skip the MarkerDir scan below: a needs-input hook can fire
	// (and a marker can exist) before Codex's own state db does.

	var queryErrs []error
	now := time.Now()
	for _, dbPath := range dbPaths {
		if fi, err := os.Stat(dbPath); err == nil && fi.Size() == 0 {
			// A zero-length DB is one the agent hasn't opened/populated yet
			// (e.g. a stale ~/.codex/state_N.sqlite from a session that never
			// started), not a query failure.
			continue
		}
		rows, err := w.queryCached(ctx, dbPath)
		if err != nil {
			queryErrs = append(queryErrs, fmt.Errorf("query %s: %w", dbPath, err))
			continue
		}
		for path, updatedMs := range rows {
			st := Done
			if now.Sub(time.UnixMilli(updatedMs)) <= activeAge {
				st = Working
			}
			// Max-merge like DirWatcher: the glob can match several DBs
			// (CLI + IDE plugin) holding the same cwd, and iteration order
			// must not let a staler DB downgrade a Working path.
			if prev, ok := snap.States[path]; !ok || st > prev {
				snap.States[path] = st
			}
		}
	}
	if w.MarkerDir != "" {
		markerStates, readErr, parseErr := w.markerScanner.scan(w.MarkerDir)
		if readErr != nil && !os.IsNotExist(readErr) {
			// A missing MarkerDir just means no hook has ever fired yet —
			// not an error.
			queryErrs = append(queryErrs, fmt.Errorf("scan %s: %w", w.MarkerDir, readErr))
		}
		if parseErr != nil {
			queryErrs = append(queryErrs, parseErr)
		}
		for path, st := range markerStates {
			if prev, ok := snap.States[path]; !ok || st > prev {
				snap.States[path] = st
			}
		}
	}
	if len(queryErrs) > 0 {
		snap.Err = errors.Join(queryErrs...)
	}
	send(ctx, out, snap)
}

// querySQLite runs a query via the sqlite3 CLI and returns map[path]updated_ms.
// It returns an error if the subprocess fails, so callers can distinguish a
// transient query failure from a genuinely empty result set.
func querySQLite(ctx context.Context, dbPath, query string) (map[string]int64, error) {
	// busy_timeout makes sqlite3 wait for a concurrent writer (e.g. Codex
	// itself) to release its lock instead of failing immediately with
	// SQLITE_BUSY (exit status 5).
	cmd := exec.CommandContext(ctx, "sqlite3", "-separator", "\t", "-cmd", "PRAGMA busy_timeout=2000;", dbPath, query)
	// Without WaitDelay, Output can still block past ctx's deadline: if
	// sqlite3 forked a child that inherited the output pipe, killing
	// sqlite3 alone doesn't close it.
	cmd.WaitDelay = 2 * time.Second
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if strings.Contains(stderr.String(), "no such table") {
			// The DB file exists but the agent hasn't created its schema
			// yet — an empty result, not a query failure.
			return map[string]int64{}, nil
		}
		return nil, err
	}
	result := make(map[string]int64)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		ms, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			continue
		}
		result[strings.TrimSpace(parts[0])] = ms
	}
	return result, nil
}
