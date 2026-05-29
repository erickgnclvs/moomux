// Package prompt extracts the first user prompt from an agent session
// so moomux can show "what is this session doing?".
package prompt

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// EncodeCwd mirrors Claude Code's project-dir encoding: both '/' and '.'
// become '-'. Existing hyphens are preserved.
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
	query := `SELECT json_extract(p.data, '$.text')
FROM part p
JOIN message m ON p.message_id = m.id
JOIN session s ON s.id = m.session_id
WHERE s.directory = '` + strings.ReplaceAll(worktreePath, "'", "''") + `'
  AND json_extract(m.data, '$.role') = 'user'
  AND json_extract(p.data, '$.type') = 'text'
ORDER BY m.time_created ASC, p.time_created ASC
LIMIT 1`
	out, err := exec.Command("sqlite3", dbPath, query).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// FirstCodex returns the first user prompt from a Codex CLI session by
// querying the threads table in the state SQLite files.
func FirstCodex(home, worktreePath string) string {
	globs := codexDBGlobs(home)
	query := "SELECT first_user_message FROM threads WHERE cwd = '" +
		strings.ReplaceAll(worktreePath, "'", "''") +
		"' AND first_user_message != '' ORDER BY created_at ASC LIMIT 1"
	for _, pattern := range globs {
		paths, err := filepath.Glob(pattern)
		if err != nil || len(paths) == 0 {
			continue
		}
		for _, p := range paths {
			out, err := exec.Command("sqlite3", p, query).Output()
			if err != nil {
				continue
			}
			if s := strings.TrimSpace(string(out)); s != "" {
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
		if bestPrompt == "" || ts < bestTS {
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
	sort.Slice(out, func(i, j int) bool { return out[i].mod < out[j].mod })
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
