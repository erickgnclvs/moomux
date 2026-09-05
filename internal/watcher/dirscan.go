package watcher

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// dirScanner is scanDir plus a per-file parse cache keyed by mtime+size, so
// a rescan only re-reads the files that actually changed.
//
// This matters because these directories are append-only in practice and
// almost entirely cold: a real ~/.claude/sessions here held 343 JSON files
// (1.4 MB) of which 13 had been touched in the last day, and the macOS
// fsnotify path rescans the whole directory on every filesystem event
// (debounced to 100ms), so an active agent had moomux re-reading and
// re-unmarshalling all 343 several times a second — measured at 5.15ms and
// 476 KB of garbage per scan, ~96% of it spent re-parsing files that had
// been dead for weeks.
//
// Keying on mtime+size rather than an age cutoff keeps this a pure
// optimization: a file that stops changing keeps its last parsed state
// forever, exactly as a full rescan would return it, so nothing depends on
// guessing how long an idle-but-live session may sit unwritten. The one
// blind spot is a rewrite that changes neither size nor mtime, which needs
// two writes inside the filesystem's timestamp resolution (nanoseconds on
// APFS and ext4) to land on a byte-identical length.
type dirScanner struct {
	mu    sync.Mutex
	cache map[string]cachedParse // by file name within the scanned dir
}

// cachedParse is one file's last parse, valid as long as mtime and size
// still match. err is cached too: a half-written file that nothing touches
// again must keep reporting its parse failure rather than silently
// disappearing from Snapshot.Err on the next scan.
type cachedParse struct {
	modNano int64
	size    int64
	rs      rawSession
	err     error
}

// scan reads dir for *.json session/marker files, classifies each, and
// max-merges the result by cwd. Shared by DirWatcher (Claude's session dir)
// and SQLiteWatcher's optional MarkerDir (Codex's needs-input markers) —
// both watch a directory of small JSON files keyed by cwd. readErr means dir
// itself couldn't be read (states is unpopulated); parseErr (an errors.Join
// of per-file failures) leaves states populated with everything that did
// parse, so one half-written file doesn't blind the caller to every other
// session in the directory.
func (d *dirScanner) scan(dir string) (states map[string]State, readErr, parseErr error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err, nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Rebuilt from the entries actually seen, so a deleted file's cache
	// entry goes away with it instead of accumulating forever.
	next := make(map[string]cachedParse, len(entries))
	states = map[string]State{}
	var parseErrs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		got := d.parse(dir, name, e)
		next[name] = got
		if got.err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("parse %s: %w", name, got.err))
			continue
		}
		if got.rs.CWD == "" {
			continue
		}
		st := classify(got.rs)
		if prev, ok := states[got.rs.CWD]; !ok || st > prev {
			states[got.rs.CWD] = st
		}
	}
	d.cache = next

	if len(parseErrs) > 0 {
		parseErr = errors.Join(parseErrs...)
	}
	return states, nil, parseErr
}

// parse returns name's parse, reusing the cached one when mtime and size are
// both unchanged. Caller must hold d.mu.
func (d *dirScanner) parse(dir, name string, e fs.DirEntry) cachedParse {
	info, err := e.Info()
	if err != nil {
		// No stat to compare against, so the cache can't be trusted for
		// this file — read it and don't cache the result.
		rs, perr := parseFile(filepath.Join(dir, name))
		return cachedParse{rs: rs, err: perr}
	}
	mod, size := info.ModTime().UnixNano(), info.Size()
	if prev, ok := d.cache[name]; ok && prev.modNano == mod && prev.size == size {
		return prev
	}
	rs, perr := parseFile(filepath.Join(dir, name))
	return cachedParse{modNano: mod, size: size, rs: rs, err: perr}
}
