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

func (s *Store) All() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0, len(s.sessions))
	for _, v := range s.sessions {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
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

func MakeID(project, name string) string { return project + ":" + name }

func DefaultPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "moomux", "sessions.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "moomux", "sessions.json")
}
