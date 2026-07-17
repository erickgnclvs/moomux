package prompt

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEncodeCwd(t *testing.T) {
	cases := map[string]string{
		"/home/user/my.repo":  "-home-user-my-repo",
		"/a/b_c/d-e":          "-a-b-c-d-e",
		"already-hyphenated":  "already-hyphenated",
		"/wt/proj/feat.work_": "-wt-proj-feat-work-",
	}
	for in, want := range cases {
		if got := EncodeCwd(in); got != want {
			t.Errorf("EncodeCwd(%q) = %q, want %q", in, got, want)
		}
	}
}

// writeJSONL creates a jsonl file under the Claude projects dir for wt.
func writeJSONL(t *testing.T, home, wt, name string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", EncodeCwd(wt))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func userLine(ts, text string) string {
	return `{"type":"user","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}`
}

func TestFirstSkipsNoise(t *testing.T) {
	home := t.TempDir()
	wt := "/wt/demo/feat"
	writeJSONL(t, home, wt, "a.jsonl",
		`not json at all`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:00Z","message":{"role":"assistant","content":"hi"}}`,
		`{"type":"user","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"tool_result"}]}}`,
		userLine("2026-01-01T00:00:02Z", "╭ banner box"),
		userLine("2026-01-01T00:00:03Z", "<command-name>/help</command-name>"),
		userLine("2026-01-01T00:00:04Z", "   "),
		userLine("2026-01-01T00:00:05Z", "fix the login bug"),
		userLine("2026-01-01T00:00:06Z", "second prompt"),
	)
	if got := First(home, wt); got != "fix the login bug" {
		t.Fatalf("got %q", got)
	}
}

func TestFirstPicksEarliestTimestampAcrossFiles(t *testing.T) {
	home := t.TempDir()
	wt := "/wt/demo/feat"
	// Resumed session file has a NEWER mtime but the original opener has the
	// earlier in-file timestamp — the original must win.
	writeJSONL(t, home, wt, "resumed.jsonl", userLine("2026-01-02T00:00:00Z", "resumed prompt"))
	orig := writeJSONL(t, home, wt, "original.jsonl", userLine("2026-01-01T00:00:00Z", "original prompt"))
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(orig, old, old); err != nil {
		t.Fatal(err)
	}
	if got := First(home, wt); got != "original prompt" {
		t.Fatalf("got %q", got)
	}
}

func TestFirstMissingDir(t *testing.T) {
	if got := First(t.TempDir(), "/nope"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestJSONLFilesByMtime(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().Add(-age)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	mk("new.jsonl", 0)
	mk("old.jsonl", time.Hour)
	mk("ignored.txt", 0)
	if err := os.Mkdir(filepath.Join(dir, "sub.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := jsonlFilesByMtime(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || filepath.Base(got[0]) != "old.jsonl" || filepath.Base(got[1]) != "new.jsonl" {
		t.Fatalf("got %v", got)
	}
}

func TestFirstInFileMissing(t *testing.T) {
	ts, text := firstInFile(filepath.Join(t.TempDir(), "nope.jsonl"))
	if ts != "" || text != "" {
		t.Fatalf("got %q %q", ts, text)
	}
}

func TestForAgentDispatch(t *testing.T) {
	home := t.TempDir()
	wt := "/wt/demo/feat"
	writeJSONL(t, home, wt, "a.jsonl", userLine("2026-01-01T00:00:00Z", "claude prompt"))

	if got := ForAgent(home, "claude", wt); got != "claude prompt" {
		t.Fatalf("claude: got %q", got)
	}
	if got := ForAgent(home, "", wt); got != "claude prompt" {
		t.Fatalf("default: got %q", got)
	}
	// No opencode/codex databases exist under this home; both must return
	// "" without error (they shell out to sqlite3, which fails on a
	// missing file — and may not be installed at all).
	if got := ForAgent(home, "opencode", wt); got != "" {
		t.Fatalf("opencode: got %q", got)
	}
	if got := ForAgent(home, "codex", wt); got != "" {
		t.Fatalf("codex: got %q", got)
	}
}

func TestFirstCodexScansGlobs(t *testing.T) {
	home := t.TempDir()
	// A state file exists but isn't a real SQLite DB (or sqlite3 is not
	// installed) — FirstCodex must swallow the failure and return "".
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state_1.sqlite"), []byte("not a db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FirstCodex(home, "/wt/x"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestCodexDBGlobs(t *testing.T) {
	globs := codexDBGlobs("/home/u")
	if len(globs) == 0 || globs[0] != filepath.Join("/home/u", ".codex", "state_*.sqlite") {
		t.Fatalf("globs = %v", globs)
	}
	if runtime.GOOS == "darwin" && len(globs) != 2 {
		t.Fatalf("darwin should add JetBrains glob: %v", globs)
	}
}
