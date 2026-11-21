package watcher

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/erickgnclvs/moomux/internal/prompt"
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
	// For an idle session scanned with transcripts: the transcript's stat
	// when asks was computed, and whether its last message asked a question.
	tModNano, tSize int64
	asks            bool
	// good is the file's last successful parse, kept across a failed one so
	// a half-written file doesn't drop its session out of the scan.
	good rawSession
}

// scan reads dir for *.json session/marker files, classifies each, and
// max-merges the result by cwd. Shared by DirWatcher (Claude's session dir)
// and SQLiteWatcher's optional MarkerDir (Codex's needs-input markers) —
// both watch a directory of small JSON files keyed by cwd. readErr means dir
// itself couldn't be read (states is unpopulated); parseErr (an errors.Join
// of per-file failures) leaves states populated with everything that did
// parse, so one half-written file doesn't blind the caller to every other
// session in the directory.
//
// transcripts, when set, is Claude Code's ~/.claude/projects: an idle
// session whose transcript ends by asking a question reads NeedsInput.
func (d *dirScanner) scan(dir, transcripts string) (states map[string]State, readErr, parseErr error) {
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
		// <session-id>.notify.json are the needs-input markers older moomux
		// versions wrote here from Claude Code hooks. claudehook.RemoveHooks
		// deletes them; until it has run they'd pin a worktree at NeedsInput
		// long after the session that wrote them ended.
		if e.IsDir() || filepath.Ext(name) != ".json" || strings.HasSuffix(name, ".notify.json") {
			continue
		}
		got := d.parse(dir, name, e)
		if got.err != nil {
			next[name] = got
			parseErrs = append(parseErrs, fmt.Errorf("parse %s: %w", name, got.err))
			// Report the error, but keep the state this file last gave.
			if cwd := got.good.CWD; cwd != "" {
				if st := classify(got.good); st > states[cwd] {
					states[cwd] = st
				}
			}
			continue
		}
		if got.rs.CWD == "" {
			next[name] = got
			continue
		}
		st := classify(got.rs)
		if st == Done && transcripts != "" && got.rs.SessionID != "" {
			got = question(got, transcripts)
			if got.asks {
				st = NeedsInput
			}
		}
		next[name] = got
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
	prev, hadPrev := d.cache[name]
	if hadPrev && prev.modNano == mod && prev.size == size {
		return prev
	}
	rs, perr := parseFile(filepath.Join(dir, name))
	got := cachedParse{modNano: mod, size: size, rs: rs, err: perr}
	if perr != nil && hadPrev {
		got.good = prev.good
		if prev.err == nil {
			got.good = prev.rs
		}
	}
	return got
}

// question refreshes got.asks when the session's transcript has changed
// since it was last read. Claude Code writes "idle" to the session file
// ~100ms before the turn's last message reaches the transcript, so the scan
// that sees "idle" usually reads the turn before. Keying on the transcript's
// stat is what lets a later scan (DirWatcher's settle rescan, or any other)
// pick the new message up, even though the session file hasn't changed.
func question(got cachedParse, transcripts string) cachedParse {
	path := filepath.Join(transcripts, prompt.EncodeCwd(got.rs.CWD), got.rs.SessionID+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		got.tModNano, got.tSize, got.asks = 0, 0, false
		return got
	}
	mod, size := info.ModTime().UnixNano(), info.Size()
	if mod != got.tModNano || size != got.tSize {
		got.tModNano, got.tSize, got.asks = mod, size, asksQuestion(path)
	}
	return got
}
