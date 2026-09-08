// Package prompt extracts the first user prompt from an agent session
// so moomux can show "what is this session doing?".
package prompt

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// queryTimeout bounds every sqlite3 subprocess this package spawns. These
// run off the TUI's periodic status-refresh goroutine (not a
// caller-supplied context), so a locked/corrupt DB blocks that one query,
// not the whole refresh, indefinitely.
var queryTimeout = 3 * time.Second

// sqliteQuery runs query against dbPath via the sqlite3 CLI and returns its
// trimmed output. Any failure — sqlite3 missing, a locked or corrupt DB, the
// query timing out, no rows — yields "", since every caller's answer to
// "couldn't read a prompt" is the same: show none.
func sqliteQuery(dbPath, query string) string {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", dbPath, query)
	// Without WaitDelay, Output can still block past ctx's deadline: if
	// sqlite3 forked a child that inherited the output pipe, killing
	// sqlite3 alone doesn't close it.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sqlQuote renders s as a single-quoted SQL string literal. These queries go
// to the sqlite3 CLI as one argv string, so there's no placeholder binding
// to lean on — worktree paths are the only thing interpolated, and doubling
// any embedded quote is what keeps a path with an apostrophe in it from
// breaking (or reshaping) the statement.
func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// EncodeCwd mirrors Claude Code's project-dir encoding: '/', '.', and '_'
// all become '-'. Existing hyphens are preserved.
func EncodeCwd(p string) string {
	r := strings.NewReplacer("/", "-", ".", "-", "_", "-")
	return r.Replace(p)
}

type entry struct {
	Type      string          `json:"type"`
	Message   json.RawMessage `json:"message"`
	Timestamp string          `json:"timestamp"`
}

type msg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ForAgent returns the first user prompt for a session, dispatching to the
// right data source based on agent type.
func ForAgent(home, agent, worktreePath string) string {
	switch agent {
	case "opencode":
		return FirstOpenCode(home, worktreePath)
	case "codex":
		return FirstCodex(home, worktreePath)
	default:
		return First(home, worktreePath)
	}
}

// FirstOpenCode returns the first user text prompt for an OpenCode session
// by querying ~/.local/share/opencode/opencode.db.
func FirstOpenCode(home, worktreePath string) string {
	dbPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if _, err := os.Stat(dbPath); err != nil {
		// sqlite3 creates an empty db file at dbPath if it doesn't exist,
		// even for a read-only query — avoid leaving that behind when the
		// agent hasn't run yet.
		return ""
	}
	query := `SELECT json_extract(p.data, '$.text')
FROM part p
JOIN message m ON p.message_id = m.id
JOIN session s ON s.id = m.session_id
WHERE s.directory = ` + sqlQuote(worktreePath) + `
  AND json_extract(m.data, '$.role') = 'user'
  AND json_extract(p.data, '$.type') = 'text'
ORDER BY m.time_created ASC, p.time_created ASC
LIMIT 1`
	return sqliteQuery(dbPath, query)
}

// FirstCodex returns the first user prompt from a Codex CLI session by
// querying the threads table in the state SQLite files.
func FirstCodex(home, worktreePath string) string {
	query := "SELECT first_user_message FROM threads WHERE cwd = " +
		sqlQuote(worktreePath) +
		" AND first_user_message != '' ORDER BY created_at ASC LIMIT 1"
	for _, pattern := range codexDBGlobs(home) {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, p := range paths {
			if s := sqliteQuery(p, query); s != "" {
				return s
			}
		}
	}
	return ""
}

// codexDBGlobs returns glob patterns for Codex state SQLite files across
// known installation layouts (OpenAI CLI and JetBrains plugin).
func codexDBGlobs(home string) []string {
	globs := []string{
		filepath.Join(home, ".codex", "state_*.sqlite"),
	}
	if runtime.GOOS == "darwin" {
		globs = append(globs,
			filepath.Join(home, "Library", "Caches", "JetBrains", "*", "aia", "codex", "state_*.sqlite"),
		)
	}
	return globs
}

// First returns the earliest non-banner user prompt across all jsonl
// logs under ~/.claude/projects/<encoded-cwd>/, ranked by the in-file
// timestamp so resumed sessions don't shadow the original opener.
func First(home, worktreePath string) string {
	dir := filepath.Join(home, ".claude", "projects", EncodeCwd(worktreePath))
	files, err := jsonlFilesByMtime(dir)
	if err != nil {
		return ""
	}
	var bestTS, bestPrompt string
	for _, f := range files {
		ts, p := firstInFile(f)
		if p == "" {
			continue
		}
		// An empty ts (missing timestamp field) sorts before every real
		// RFC3339 value, so it must never overwrite a real one — only
		// take it when nothing better has been found yet.
		if bestPrompt == "" || (ts != "" && (bestTS == "" || ts < bestTS)) {
			bestTS, bestPrompt = ts, p
		}
	}
	return bestPrompt
}

func jsonlFilesByMtime(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type fi struct {
		path string
		mod  int64
	}
	out := make([]fi, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, fi{filepath.Join(dir, e.Name()), info.ModTime().UnixNano()})
	}
	// Path breaks mtime ties: two logs written in the same clock tick would
	// otherwise come back in either order, and the last one wins below.
	sort.Slice(out, func(i, j int) bool {
		if out[i].mod != out[j].mod {
			return out[i].mod < out[j].mod
		}
		return out[i].path < out[j].path
	})
	paths := make([]string, len(out))
	for i, f := range out {
		paths[i] = f.path
	}
	return paths, nil
}

func firstInFile(path string) (timestamp, text string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if e.Type != "user" || len(e.Message) == 0 {
			continue
		}
		var m msg
		if err := json.Unmarshal(e.Message, &m); err != nil {
			continue
		}
		if m.Role != "user" || len(m.Content) == 0 || m.Content[0] != '"' {
			continue
		}
		var s string
		if err := json.Unmarshal(m.Content, &s); err != nil {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" || strings.HasPrefix(s, "╭") || strings.HasPrefix(s, "<command-") {
			continue
		}
		return e.Timestamp, s
	}
	return "", ""
}
