// Package prompt extracts the first user prompt from a Claude Code
// session log so curral can show "what is this session doing?".
package prompt

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
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
