// Package session persists moomux session metadata to JSON.
package session

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Session struct {
	ID           string    `json:"id"`
	Project      string    `json:"project"`
	Name         string    `json:"name"`
	Branch       string    `json:"branch"`
	WorktreePath string    `json:"worktree_path"`
	TmuxSession  string    `json:"tmux_session"`
	CreatedAt    time.Time `json:"created_at"`
	Agent        string    `json:"agent,omitempty"`      // "claude", "codex", "opencode"; empty = "claude"
	AgentPort    int       `json:"agent_port,omitempty"` // HTTP port for OpenCode API; 0 = not applicable
	Ticket       string    `json:"ticket,omitempty"`     // ticket URL (e.g. Asana, Jira, Linear)
	PR           string    `json:"pr,omitempty"`         // pull request URL (e.g. GitHub, GitLab)
	Order        int64     `json:"order,omitempty"`      // manual sort position within a project; 0 = unset, falls back to CreatedAt
}

// AgentName returns the effective agent name, defaulting to "claude" for legacy sessions.
func (s Session) AgentName() string {
	if s.Agent == "" {
		return "claude"
	}
	return s.Agent
}

type fileShape struct {
	Version  int                `json:"version"`
	Sessions map[string]Session `json:"sessions"`
}

type Store struct {
	Path string

	mu       sync.Mutex
	sessions map[string]Session
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = map[string]Session{}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var f fileShape
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	if f.Sessions != nil {
		s.sessions = f.Sessions
	}
	return nil
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	f := fileShape{Version: 1, Sessions: s.sessions}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

func (s *Store) Put(sess Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]Session{}
	}
	s.sessions[sess.ID] = sess
	return s.save()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return s.save()
}

func (s *Store) Get(id string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

// All returns every session ordered by manual Order ascending (0 = unset,
// so unordered sessions sort first — matching where a freshly created
// session should land), falling back to CreatedAt descending among
// sessions with equal Order.
func (s *Store) All() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0, len(s.sessions))
	for _, v := range s.sessions {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (s *Store) ByProject(project string) []Session {
	all := s.All()
	out := make([]Session, 0, len(all))
	for _, sess := range all {
		if sess.Project == project {
			out = append(out, sess)
		}
	}
	return out
}

// Reorder assigns sequential Order values (1..N) to the given sessions, in
// the order given, and persists the store in a single write. Callers pass a
// full project's sessions (e.g. from ByProject) after rearranging them, so
// the rest of that project's ordering stays self-consistent; sessions
// outside the given slice are left untouched.
func (s *Store) Reorder(sessions []Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sess := range sessions {
		sess.Order = int64(i + 1)
		s.sessions[sess.ID] = sess
	}
	return s.save()
}

func MakeID(project, name string) string { return project + ":" + name }

func DefaultPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "moomux", "sessions.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "moomux", "sessions.json")
}
