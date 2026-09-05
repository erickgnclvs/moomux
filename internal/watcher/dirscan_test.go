package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// write puts contents at dir/name and stamps it with mod, so a test can
// control the (size, mtime) pair the scan cache keys on.
func write(t *testing.T, dir, name, contents string, mod time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
	return p
}

func busy(cwd string) string { return `{"cwd":"` + cwd + `","status":"busy"}` }
func idle(cwd string) string { return `{"cwd":"` + cwd + `","status":"idle"}` }

// TestScanReusesUnchangedFiles is the CPU fix: a rescan must not re-read a
// file whose size and mtime are unchanged. Proven by rewriting the file's
// contents behind the cache's back while restoring its stamp — a scan that
// re-read it would report the new state.
func TestScanReusesUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	mod := time.Now().Add(-time.Hour)
	write(t, dir, "a.json", busy("/wt/a"), mod)

	var d dirScanner
	states, readErr, parseErr := d.scan(dir)
	if readErr != nil || parseErr != nil {
		t.Fatalf("scan: %v %v", readErr, parseErr)
	}
	if states["/wt/a"] != Working {
		t.Fatalf("first scan: got %v, want Working", states["/wt/a"])
	}

	// Same length, same mtime, different contents.
	write(t, dir, "a.json", idle("/wt/a"), mod)
	states, _, _ = d.scan(dir)
	if states["/wt/a"] != Working {
		t.Errorf("second scan re-read an unchanged file: got %v, want the cached Working", states["/wt/a"])
	}
}

// TestScanRereadsChangedFiles is the other half: caching must not make the
// watcher blind to a file an agent actually rewrote.
func TestScanRereadsChangedFiles(t *testing.T) {
	dir := t.TempDir()
	mod := time.Now().Add(-time.Hour)
	write(t, dir, "a.json", busy("/wt/a"), mod)

	var d dirScanner
	if states, _, _ := d.scan(dir); states["/wt/a"] != Working {
		t.Fatalf("first scan: got %v, want Working", states["/wt/a"])
	}

	t.Run("mtime changes", func(t *testing.T) {
		write(t, dir, "a.json", idle("/wt/a"), mod.Add(time.Second))
		if states, _, _ := d.scan(dir); states["/wt/a"] != Done {
			t.Errorf("got %v, want Done", states["/wt/a"])
		}
	})
	t.Run("size changes at the same mtime", func(t *testing.T) {
		mod := mod.Add(time.Second)
		write(t, dir, "a.json", `{"cwd":"/wt/a","status":"needs-input"}`, mod)
		if states, _, _ := d.scan(dir); states["/wt/a"] != NeedsInput {
			t.Errorf("got %v, want NeedsInput", states["/wt/a"])
		}
	})
}

// TestScanDropsDeletedFiles covers both halves of a delete: the path leaves
// the result, and its cache entry goes with it rather than accumulating for
// the life of the process.
func TestScanDropsDeletedFiles(t *testing.T) {
	dir := t.TempDir()
	mod := time.Now()
	write(t, dir, "a.json", busy("/wt/a"), mod)
	write(t, dir, "b.json", busy("/wt/b"), mod)

	var d dirScanner
	d.scan(dir)
	if err := os.Remove(filepath.Join(dir, "b.json")); err != nil {
		t.Fatal(err)
	}
	states, _, _ := d.scan(dir)
	if _, ok := states["/wt/b"]; ok {
		t.Error("deleted file still reported")
	}
	if len(d.cache) != 1 {
		t.Errorf("cache holds %d entries, want 1 — deleted files leak", len(d.cache))
	}
}

// TestScanCachesParseErrors keeps Snapshot.Err's contract intact across the
// cache: a half-written file nothing touches again must keep reporting its
// failure, not silently vanish from the error on the next scan.
func TestScanCachesParseErrors(t *testing.T) {
	dir := t.TempDir()
	mod := time.Now()
	write(t, dir, "bad.json", "{not json", mod)

	var d dirScanner
	if _, _, parseErr := d.scan(dir); parseErr == nil {
		t.Fatal("first scan: want a parse error")
	}
	if _, _, parseErr := d.scan(dir); parseErr == nil {
		t.Error("second scan dropped the parse error for an unchanged bad file")
	}
}
