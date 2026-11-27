package prompt

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
		`{"type":"user","isMeta":true,"timestamp":"2026-01-01T00:00:03Z","message":{"role":"user","content":"<local-command-caveat>The command below was run directly</local-command-caveat>"}}`,
		userLine("2026-01-01T00:00:03Z", "<local-command-stdout>Set model to Opus</local-command-stdout>"),
		userLine("2026-01-01T00:00:04Z", "   "),
		userLine("2026-01-01T00:00:05Z", "fix the login bug"),
		userLine("2026-01-01T00:00:06Z", "second prompt"),
	)
	if got := First(home, wt, time.Time{}); got != "fix the login bug" {
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
	if got := First(home, wt, time.Time{}); got != "original prompt" {
		t.Fatalf("got %q", got)
	}
}

func TestFirstEmptyTimestampNeverOverridesARealOne(t *testing.T) {
	home := t.TempDir()
	wt := "/wt/demo/feat"
	// A file scanned *after* the real-timestamped one (older mtime first,
	// so it's processed earlier) whose entry is missing a timestamp must
	// not win — an empty string sorts before every real RFC3339 value.
	timed := writeJSONL(t, home, wt, "timed.jsonl", userLine("2026-01-01T00:00:00Z", "timed prompt"))
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(timed, old, old); err != nil {
		t.Fatal(err)
	}
	writeJSONL(t, home, wt, "notime.jsonl", userLine("", "notime prompt"))

	if got := First(home, wt, time.Time{}); got != "timed prompt" {
		t.Fatalf("got %q, want the real-timestamped prompt to survive", got)
	}
}

func TestFirstMissingDir(t *testing.T) {
	if got := First(t.TempDir(), "/nope", time.Time{}); got != "" {
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
	ts, text := firstInFile(filepath.Join(t.TempDir(), "nope.jsonl"), time.Time{})
	if ts != "" || text != "" {
		t.Fatalf("got %q %q", ts, text)
	}
}

func TestFirstIgnoresPromptsBeforeSince(t *testing.T) {
	home := t.TempDir()
	wt := "/home/moo"
	writeJSONL(t, home, wt, "other.jsonl", userLine("2026-09-15T21:36:08.492Z", "another session's prompt"))
	writeJSONL(t, home, wt, "mine.jsonl", userLine("2026-10-08T22:27:52.345Z", "this session's prompt"))
	since := time.Date(2026, 10, 8, 22, 27, 0, 0, time.UTC)
	if got := First(home, wt, since); got != "this session's prompt" {
		t.Fatalf("got %q", got)
	}
}

func TestForAgentDispatch(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB", "")
	home := t.TempDir()
	wt := "/wt/demo/feat"
	writeJSONL(t, home, wt, "a.jsonl", userLine("2026-01-01T00:00:00Z", "claude prompt"))

	if got := ForAgent(home, "claude", wt, time.Time{}); got != "claude prompt" {
		t.Fatalf("claude: got %q", got)
	}
	if got := ForAgent(home, "", wt, time.Time{}); got != "claude prompt" {
		t.Fatalf("default: got %q", got)
	}
	// No opencode/codex databases exist under this home; both must return
	// "" without error (they shell out to sqlite3, which fails on a
	// missing file — and may not be installed at all).
	if got := ForAgent(home, "opencode", wt, time.Time{}); got != "" {
		t.Fatalf("opencode: got %q", got)
	}
	if got := ForAgent(home, "codex", wt, time.Time{}); got != "" {
		t.Fatalf("codex: got %q", got)
	}
	if got := ForAgent(home, "antigravity", wt, time.Time{}); got != "" {
		t.Fatalf("antigravity: got %q", got)
	}
	// Deliberately no "agy" case: callers pass Session.AgentName(), which
	// has already folded the alias, so ForAgent knows one spelling.
}

func TestFirstOpenCodeMissingDBLeavesNoStrayFile(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB", "")
	home := t.TempDir()
	dbPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	// The parent dir existing (opencode installed, db not created yet) is
	// the realistic case: sqlite3 can create the db file itself, it just
	// can't create missing parent directories.
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FirstOpenCode(home, "/wt/x", time.Time{}); got != "" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(dbPath); err == nil {
		t.Fatal("sqlite3 left an empty db file behind for a missing path")
	}
}

// TestFirstOpenCodeRespectsQueryTimeout replaces "sqlite3" on PATH with a
// fake that sleeps far longer than queryTimeout. Without
// exec.CommandContext bounding the subprocess, FirstOpenCode would block
// for the full sleep instead of giving up once the timeout elapses.
func TestFirstOpenCodeRespectsQueryTimeout(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB", "")
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	home := t.TempDir()
	dbPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeDir := t.TempDir()
	fake := filepath.Join(fakeDir, "sqlite3")
	calls := filepath.Join(fakeDir, "calls")
	t.Setenv("SQLITE_CALL_LOG", calls)
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho query >> \"$SQLITE_CALL_LOG\"\nsleep 5 &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	old := queryTimeout
	queryTimeout = 400 * time.Millisecond
	t.Cleanup(func() { queryTimeout = old })

	start := time.Now()
	got := FirstOpenCode(home, "/wt/x", time.Time{})
	elapsed := time.Since(start)
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	data, err := os.ReadFile(calls)
	if err != nil || string(data) != "query\n" {
		t.Fatalf("sqlite3 invocations = %q, err = %v; expected one query before deadline", data, err)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("FirstOpenCode took %v, want to return shortly after queryTimeout", elapsed)
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
	if got := FirstCodex(home, "/wt/x", time.Time{}); got != "" {
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

func TestFirstAntigravity(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()
	dbDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "conversation_summaries.db")

	schema := `CREATE TABLE conversation_summaries (
		conversation_id text PRIMARY KEY,
		title text NOT NULL DEFAULT '',
		preview text NOT NULL DEFAULT '',
		last_modified_time datetime NOT NULL,
		workspace_uris text NOT NULL
	);`
	if err := exec.Command("sqlite3", dbPath, schema).Run(); err != nil {
		t.Fatal(err)
	}

	wt := "/Users/test/projects/mywt"
	insert := `
	INSERT INTO conversation_summaries VALUES (
		'c1', 'Title 1', 'First prompt from preview',
		'2026-09-01 10:00:00+00:00',
		'["file:///Users/test/projects/mywt"]'
	);
	INSERT INTO conversation_summaries VALUES (
		'c2', 'Title 2', 'Second prompt later',
		'2026-09-02 10:00:00+00:00',
		'["file:///Users/test/projects/mywt"]'
	);
	`
	if err := exec.Command("sqlite3", dbPath, insert).Run(); err != nil {
		t.Fatal(err)
	}

	if got := FirstAntigravity(home, wt, time.Time{}); got != "First prompt from preview" {
		t.Fatalf("FirstAntigravity = %q, want %q", got, "First prompt from preview")
	}

	// Test fallback to title when preview is empty
	wt2 := "/Users/test/projects/other"
	insert2 := `
	INSERT INTO conversation_summaries VALUES (
		'c3', 'Fallback title prompt', '',
		'2026-09-01 10:00:00+00:00',
		'["file:///Users/test/projects/other"]'
	);
	`
	if err := exec.Command("sqlite3", dbPath, insert2).Run(); err != nil {
		t.Fatal(err)
	}

	if got := FirstAntigravity(home, wt2, time.Time{}); got != "Fallback title prompt" {
		t.Fatalf("FirstAntigravity fallback = %q, want %q", got, "Fallback title prompt")
	}

	// Percent-escaped URIs: agy escapes more than the space the old
	// replace() chain decoded, so a path with a '#' in it found nothing.
	wt3 := "/Users/test/pro jects/c#d"
	insert3 := `
	INSERT INTO conversation_summaries VALUES (
		'c4', 'Escaped title', 'Escaped path prompt',
		'2026-09-01 10:00:00+00:00',
		'["file:///Users/test/pro%20jects/c%23d"]'
	);
	`
	if err := exec.Command("sqlite3", dbPath, insert3).Run(); err != nil {
		t.Fatal(err)
	}
	if got := FirstAntigravity(home, wt3, time.Time{}); got != "Escaped path prompt" {
		t.Fatalf("FirstAntigravity escaped = %q, want %q", got, "Escaped path prompt")
	}
}

// TestNonClaudeAgentsIgnorePriorSessionRows: a worktree path reused by a
// recreated session must not surface the old session's prompt.
func TestNonClaudeAgentsIgnorePriorSessionRows(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB", "")
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()
	wt := "/wt/reused"
	since := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	before, after := since.Add(-time.Hour), since.Add(time.Hour)
	run := func(db, sql string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}

	run(filepath.Join(home, ".codex", "state_1.sqlite"), `
CREATE TABLE threads (cwd text, first_user_message text, created_at integer);
INSERT INTO threads VALUES ('`+wt+`', 'old codex', `+strconv.FormatInt(before.Unix(), 10)+`);
INSERT INTO threads VALUES ('`+wt+`', 'new codex', `+strconv.FormatInt(after.Unix(), 10)+`);`)

	run(filepath.Join(home, ".local", "share", "opencode", "opencode.db"), `
CREATE TABLE session (id text, directory text);
CREATE TABLE message (id text, session_id text, data text, time_created integer);
CREATE TABLE part (id text, message_id text, data text, time_created integer);
INSERT INTO session VALUES ('s1', '`+wt+`');
INSERT INTO message VALUES ('m1', 's1', '{"role":"user"}', `+strconv.FormatInt(before.UnixMilli(), 10)+`);
INSERT INTO message VALUES ('m2', 's1', '{"role":"user"}', `+strconv.FormatInt(after.UnixMilli(), 10)+`);
INSERT INTO part VALUES ('p1', 'm1', '{"type":"text","text":"old opencode"}', 1);
INSERT INTO part VALUES ('p2', 'm2', '{"type":"text","text":"new opencode"}', 2);`)

	run(filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"), `
CREATE TABLE conversation_summaries (conversation_id text, title text, preview text, last_modified_time datetime, workspace_uris text);
INSERT INTO conversation_summaries VALUES ('c1', '', 'old agy', '`+before.Format("2006-01-02 15:04:05")+`+00:00', '["file://`+wt+`"]');
INSERT INTO conversation_summaries VALUES ('c2', '', 'new agy', '`+after.Format("2006-01-02 15:04:05")+`+00:00', '["file://`+wt+`"]');`)

	for agent, want := range map[string]string{"codex": "new codex", "opencode": "new opencode", "antigravity": "new agy"} {
		if got := ForAgent(home, agent, wt, since); got != want {
			t.Errorf("%s with since: got %q, want %q", agent, got, want)
		}
	}
	for agent, want := range map[string]string{"codex": "old codex", "opencode": "old opencode", "antigravity": "old agy"} {
		if got := ForAgent(home, agent, wt, time.Time{}); got != want {
			t.Errorf("%s zero since: got %q, want %q", agent, got, want)
		}
	}
}

func TestFirstOpenCodeV2(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()
	dir := t.TempDir()
	t.Setenv("OPENCODE_DB", filepath.Join(dir, "opencode.db"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const schema = `CREATE TABLE session_v2 (id TEXT, directory TEXT);
 CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, time_created INTEGER, seq INTEGER, data TEXT);
 INSERT INTO session_v2 VALUES ('a','/wt/other'),('b','/wt/one');
 INSERT INTO session_message VALUES
 ('m0','a','user',20,1,'{"text":"wrong directory"}'),
 ('m1','b','user',10,1,'{"text":"old"}'),
 ('m2','b','assistant',21,2,'{"text":"assistant"}'),
 ('m3','b','user',22,3,'{"text":""}'),
 ('m4','b','user',23,4,'{"text":"first prompt"}'),
 ('m5','b','user',24,5,'{"text":"second prompt"}');`
	if out, err := exec.Command("sqlite3", filepath.Join(dir, "opencode.db"), schema).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got := FirstOpenCode(home, "/wt/one", time.UnixMilli(20)); got != "first prompt" {
		t.Fatalf("got %q, want first prompt", got)
	}
}
