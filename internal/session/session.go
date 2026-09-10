// Package session persists moomux session metadata to JSON.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/erickgnclvs/moomux/internal/atomicfile"
)

type Session struct {
	ID           string    `json:"id"`
	Project      string    `json:"project"`
	Name         string    `json:"name"`
	Branch       string    `json:"branch"`
	WorktreePath string    `json:"worktree_path"`
	TmuxSession  string    `json:"tmux_session"`
	CreatedAt    time.Time `json:"created_at"`
	Agent        string    `json:"agent,omitempty"`       // "claude", "codex", "opencode"; empty = "claude"
	Dangerous    bool      `json:"dangerous,omitempty"`   // run Agent with its permission-skipping flag (claude: --dangerously-skip-permissions, codex: --yolo); no-op for opencode
	AgentPort    int       `json:"agent_port,omitempty"`  // HTTP port for OpenCode API; 0 = not applicable
	Ticket       string    `json:"ticket,omitempty"`      // ticket URL (e.g. Asana, Jira, Linear)
	PR           string    `json:"pr,omitempty"`          // pull request URL (e.g. GitHub, GitLab)
	Order        int64     `json:"order,omitempty"`       // manual sort position within a project; 0 = unset, falls back to CreatedAt
	LastOpened   time.Time `json:"last_opened,omitempty"` // when OpenSession last attached to this session; zero if never opened
	Archived     bool      `json:"archived,omitempty"`    // hidden from the default list, but not deleted
	NewBranch    bool      `json:"new_branch,omitempty"`  // true if moomux created Branch fresh (vs. checking out an existing one); safe to delete on session delete
	BaseBranch   string    `json:"base_branch,omitempty"` // the branch Branch was cut from, when NewBranch; empty for a resumed branch or a no-worktree project, where there is none to diff against
	Prompt       string    `json:"prompt,omitempty"`      // first prompt typed into the agent at creation time, captured directly rather than relying on the agent's own log
}

// CreateRequest is everything one "new session" action carries.
//
// It's a struct rather than a parameter list because creating a session is a
// transaction, not a single call: the worktree and tmux pane come up, the PR
// tag is attached, the first prompt is composed and stored, and finally the
// prompt is typed into the agent's pane. That sequence — and the rule that a
// failure after the pane exists degrades to a hint rather than a failed
// create — used to live in the TUI, which meant every front end had to
// replay it exactly. It had already drifted: `moomux spawn` composed the
// prompt differently and skipped two of the steps.
type CreateRequest struct {
	Project string
	Name    string
	Agent   string
	// Branch is an existing branch to check out; empty means cut a new one.
	Branch     string
	BaseBranch string
	Ticket     string
	PR         string
	Model      string
	Thinking   string
	// Prompt is the first task to hand the agent. Empty means don't type
	// anything into the pane.
	Prompt string
	// AutoSubmit presses Enter after typing Prompt.
	AutoSubmit bool
	// OpenTerminal says the front end is opening a terminal on the new
	// session. The core never opens one itself — that's the front end's
	// job, since it's the one with a terminal (see main.go's
	// terminalBackend) — so this only tells the core to treat the session
	// as opened now, and to skip the "attach with…" hint nobody needs.
	OpenTerminal bool
	// Dangerous runs the agent with its permission-skipping flag. nil means
	// "use the project's own default" — a plain bool can't express that,
	// since an explicit false and an unset field would be identical.
	Dangerous *bool
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
	return s.reloadLocked()
}

// reloadLocked re-reads the store file into memory. Every mutating method
// calls this first so its write lands on top of whatever other moomux
// processes (e.g. a `moomux spawn` sharing this same sessions.json) have
// saved since this Store last loaded, instead of overwriting their changes
// with a stale in-memory snapshot. Caller must hold mu.
func (s *Store) reloadLocked() error {
	if s.sessions == nil {
		s.sessions = map[string]Session{}
	}
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
	data, err := json.MarshalIndent(fileShape{Version: 1, Sessions: s.sessions}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.Path, data, 0o644)
}

func (s *Store) Put(sess Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	s.sessions[sess.ID] = sess
	return s.save()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	delete(s.sessions, id)
	return s.save()
}

// SetArchived flips a session's Archived flag and persists it. The session
// itself (worktree, tmux entry, metadata) is left untouched — archiving only
// hides it from the default list.
func (s *Store) SetArchived(id string, archived bool) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return Session{}, err
	}
	sess, ok := s.sessions[id]
	if !ok {
		return Session{}, fmt.Errorf("unknown session %q", id)
	}
	sess.Archived = archived
	s.sessions[id] = sess
	if err := s.save(); err != nil {
		return Session{}, err
	}
	return sess, nil
}

// Reload re-reads the store file from disk, picking up sessions written by
// another moomux process (e.g. `moomux spawn`) since this Store last loaded.
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloadLocked()
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
// sessions with equal Order, and finally to ID so the order is total: the
// input is a map, so any pair that ties on every other field would come
// back in a different order on every call, and clients (the Mac app's
// sidebar) re-diff their whole list when it changes.
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
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// SortByRecent reorders sessions most-recently-opened first, falling back to
// CreatedAt descending for sessions that share a LastOpened (including the
// zero value shared by every never-opened session), then ID so the order is
// total. Used in place of the manual Order sort when Config.SortRecentFirst
// is on.
func SortByRecent(sessions []Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].LastOpened.Equal(sessions[j].LastOpened) {
			return sessions[i].LastOpened.After(sessions[j].LastOpened)
		}
		if !sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
		}
		return sessions[i].ID < sessions[j].ID
	})
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
	if err := s.reloadLocked(); err != nil {
		return err
	}
	// sessions may be a snapshot the caller fetched before this reload;
	// writing it back wholesale would clobber any other field a concurrent
	// process changed since (e.g. a tag set from another moomux process).
	// Apply only the Order change, to the freshly reloaded session.
	for i, sess := range sessions {
		current, ok := s.sessions[sess.ID]
		if !ok {
			continue
		}
		current.Order = int64(i + 1)
		s.sessions[sess.ID] = current
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
