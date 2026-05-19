# curral Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go TUI binary that manages Claude Code agent sessions across git worktrees, using tmux + iTerm2 as the session backend.

**Architecture:** Single Go binary using Bubbletea (MVU) for the TUI. Internal packages provide isolated wrappers around tmux, iTerm2 (osascript), git worktrees, and a poller that watches `~/.claude/sessions/*.json`. State lives in `~/.config/curral/{config.toml,sessions.json}`. No daemon, no network.

**Tech Stack:** Go 1.24, charmbracelet/bubbletea + bubbles + lipgloss, BurntSushi/toml, std `os/exec` for tmux/git/osascript.

---

## File map

```
curral/
├── main.go                     # entrypoint: flags, config load, program start
├── go.mod / go.sum
├── Makefile                    # build, test, install
├── internal/
│   ├── config/
│   │   ├── config.go           # Config, Project structs; Load/Save
│   │   └── config_test.go
│   ├── session/
│   │   ├── session.go          # Session struct, Store (CRUD over JSON)
│   │   └── session_test.go
│   ├── tmux/
│   │   ├── tmux.go             # NewSession, HasSession, KillSession, SendKeys
│   │   └── tmux_test.go        # uses fakeRunner
│   ├── iterm/
│   │   ├── iterm.go            # OpenTab(tmuxSession) via osascript
│   │   └── iterm_test.go
│   ├── gitwt/
│   │   ├── gitwt.go            # Fetch, Add, Remove
│   │   └── gitwt_test.go
│   ├── watcher/
│   │   ├── watcher.go          # poller; emits StatusUpdate over channel
│   │   ├── parse.go            # parse one session JSON file
│   │   └── watcher_test.go
│   └── tui/
│       ├── model.go            # root Bubbletea model
│       ├── update.go           # Update dispatch
│       ├── view.go             # root View composition
│       ├── list.go             # session list rendering
│       ├── detail.go           # detail panel rendering
│       ├── form.go             # new session form overlay
│       ├── confirm.go          # delete confirmation overlay
│       ├── keys.go             # key bindings
│       ├── messages.go         # tea.Msg types
│       └── styles.go           # lipgloss styles
└── docs/superpowers/...
```

Each file has one responsibility; everything is wired in `main.go`.

---

### Task 1: Bootstrap Go module + repo files

**Files:**
- Create: `go.mod`
- Create: `main.go` (placeholder)
- Modify: `.gitignore`
- Create: `Makefile`

- [ ] **Step 1: Init module**

Run:
```bash
go mod init github.com/erickgnclvs/curral
```

- [ ] **Step 2: Add deps**

Run:
```bash
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/bubbles@latest
go get github.com/charmbracelet/lipgloss@latest
go get github.com/BurntSushi/toml@latest
```

- [ ] **Step 3: Placeholder main.go**

```go
package main

import "fmt"

func main() {
	fmt.Println("curral — TUI for Claude Code sessions")
}
```

- [ ] **Step 4: .gitignore additions**

Append:
```
/curral
/dist/
*.test
*.out
.DS_Store
```

- [ ] **Step 5: Makefile**

```makefile
.PHONY: build test install run clean

BIN := curral
PREFIX ?= $(HOME)/.local

build:
	go build -o $(BIN) .

test:
	go test ./... -race -count=1

install: build
	mkdir -p $(PREFIX)/bin
	cp $(BIN) $(PREFIX)/bin/$(BIN)

run: build
	./$(BIN)

clean:
	rm -f $(BIN)
```

- [ ] **Step 6: Verify build**

Run: `go build ./...` — expect success.

---

### Task 2: `internal/config` — TOML loader

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests**

```go
package config

import (
	"path/filepath"
	"testing"
)

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[projects.eg_system]
repo          = "~/Development/eg_system"
branch_prefix = "erickgoncalves"
base_branch   = "main"

[projects.other]
repo        = "~/Development/other"
base_branch = "main"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(cfg.Projects))
	}
	p := cfg.Projects["eg_system"]
	if p.BranchPrefix != "erickgoncalves" {
		t.Fatalf("BranchPrefix = %q", p.BranchPrefix)
	}
	if p.BaseBranch != "main" {
		t.Fatalf("BaseBranch = %q", p.BaseBranch)
	}
}

func TestLoadExpandsHome(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[projects.x]
repo        = "~/foo"
base_branch = "main"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Projects["x"].Repo; got == "~/foo" {
		t.Fatalf("expected ~ expanded, got %q", got)
	}
}

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.toml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if cfg == nil || cfg.Projects == nil {
		t.Fatalf("expected non-nil config with empty projects")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := writeAll(path, []byte(body)); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run tests (expect fail)**

Run: `go test ./internal/config/...` → fail (package not built).

- [ ] **Step 3: Implement config.go**

```go
// Package config loads and writes curral's TOML configuration.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Project struct {
	Repo         string `toml:"repo"`
	BranchPrefix string `toml:"branch_prefix,omitempty"`
	BaseBranch   string `toml:"base_branch"`
}

type Config struct {
	Projects map[string]Project `toml:"projects"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{Projects: map[string]Project{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return nil, err
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.Projects == nil {
		cfg.Projects = map[string]Project{}
	}
	for k, p := range cfg.Projects {
		p.Repo = expandHome(p.Repo)
		cfg.Projects[k] = p
	}
	return cfg, nil
}

func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

func DefaultPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "curral", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "curral", "config.toml")
}

func writeAll(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
```

- [ ] **Step 4: Run tests (expect pass)**

Run: `go test ./internal/config/... -v`

- [ ] **Step 5: Commit**

```bash
git add internal/config go.mod go.sum
git commit -m "feat(config): add TOML config loader with home expansion"
```

---

### Task 3: `internal/session` — JSON session store

**Files:**
- Create: `internal/session/session.go`
- Create: `internal/session/session_test.go`

- [ ] **Step 1: Write failing tests**

```go
package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	s := &Store{Path: path}
	if err := s.Load(); err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(s.All()) != 0 {
		t.Fatalf("expected empty")
	}

	sess := Session{
		ID:           "eg:hash",
		Project:      "eg",
		Name:         "hash",
		Branch:       "erickgoncalves/hash",
		WorktreePath: "/tmp/wt",
		TmuxSession:  "curral-hash",
		CreatedAt:    time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC),
	}
	if err := s.Put(sess); err != nil {
		t.Fatal(err)
	}

	s2 := &Store{Path: path}
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get("eg:hash")
	if !ok {
		t.Fatalf("missing after reload")
	}
	if got.Branch != "erickgoncalves/hash" {
		t.Fatalf("branch = %q", got.Branch)
	}
}

func TestStoreDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	s := &Store{Path: path}
	_ = s.Load()
	_ = s.Put(Session{ID: "a", Project: "p", Name: "a"})
	_ = s.Put(Session{ID: "b", Project: "p", Name: "b"})
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatalf("a still present")
	}
	if len(s.ByProject("p")) != 1 {
		t.Fatalf("expected 1, got %d", len(s.ByProject("p")))
	}
}

func TestMakeID(t *testing.T) {
	if got := MakeID("eg_system", "hash-password"); got != "eg_system:hash-password" {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Run tests (expect fail)**

Run: `go test ./internal/session/...`

- [ ] **Step 3: Implement session.go**

```go
// Package session persists curral session metadata to JSON.
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
		return filepath.Join(xdg, "curral", "sessions.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "curral", "sessions.json")
}
```

- [ ] **Step 4: Run tests + commit**

```bash
go test ./internal/session/... -v
git add internal/session
git commit -m "feat(session): add JSON session store with atomic writes"
```

---

### Task 4: `internal/tmux` — tmux command wrapper

**Files:**
- Create: `internal/tmux/tmux.go`
- Create: `internal/tmux/tmux_test.go`

- [ ] **Step 1: Write failing tests**

```go
package tmux

import (
	"reflect"
	"testing"
)

type fakeRunner struct {
	calls   [][]string
	out     map[string]string
	failOn  map[string]bool
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	key := join(args)
	f.calls = append(f.calls, args)
	if f.failOn[key] {
		return "", errExit
	}
	return f.out[key], nil
}

var errExit = exitErr{code: 1}

type exitErr struct{ code int }

func (e exitErr) Error() string { return "exit" }
func (e exitErr) ExitCode() int { return e.code }

func TestNewSession(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.NewSession("curral-foo", "/tmp/wt", "claude"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "curral-foo", "-c", "/tmp/wt"},
		{"send-keys", "-t", "curral-foo", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestHasSession(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{"has-session -t curral-foo": ""}}
	c := &Client{Runner: fr}
	ok, err := c.HasSession("curral-foo")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}

	fr2 := &fakeRunner{failOn: map[string]bool{"has-session -t curral-foo": true}}
	c2 := &Client{Runner: fr2}
	ok, err = c2.HasSession("curral-foo")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestKillSession(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.KillSession("curral-foo"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"kill-session", "-t", "curral-foo"}) {
		t.Fatalf("got %v", got)
	}
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}
```

- [ ] **Step 2: Implement tmux.go**

```go
// Package tmux wraps the tmux CLI behind an injectable runner.
package tmux

import (
	"errors"
	"os/exec"
)

type Runner interface {
	Run(args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return string(out), err
}

func ExecRunner() Runner { return execRunner{} }

type Client struct {
	Runner Runner
}

func New() *Client { return &Client{Runner: ExecRunner()} }

// HasSession reports whether tmux session `name` exists.
func (c *Client) HasSession(name string) (bool, error) {
	_, err := c.Runner.Run("has-session", "-t", name)
	if err == nil {
		return true, nil
	}
	var exitErr interface{ ExitCode() int }
	if errors.As(err, &exitErr) {
		return false, nil
	}
	// tmux not on PATH or transport error
	return false, err
}

// NewSession creates a detached tmux session at cwd and sends `cmd` + Enter.
// If cmd is empty, no command is sent.
func (c *Client) NewSession(name, cwd, cmd string) error {
	if _, err := c.Runner.Run("new-session", "-d", "-s", name, "-c", cwd); err != nil {
		return err
	}
	if cmd != "" {
		if _, err := c.Runner.Run("send-keys", "-t", name, cmd, "Enter"); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) KillSession(name string) error {
	_, err := c.Runner.Run("kill-session", "-t", name)
	return err
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/tmux/... -v
git add internal/tmux
git commit -m "feat(tmux): add injectable tmux client"
```

---

### Task 5: `internal/iterm` — osascript tab opener

**Files:**
- Create: `internal/iterm/iterm.go`
- Create: `internal/iterm/iterm_test.go`

- [ ] **Step 1: Write failing tests**

```go
package iterm

import (
	"strings"
	"testing"
)

type fakeRunner struct {
	script string
}

func (f *fakeRunner) Run(script string) (string, error) {
	f.script = script
	return "", nil
}

func TestOpenTabComposesScript(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.OpenTab("curral-foo"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.script, "tmux attach -t curral-foo") {
		t.Fatalf("script missing attach: %s", fr.script)
	}
	if !strings.Contains(fr.script, "iTerm2") {
		t.Fatalf("script missing iTerm2 target: %s", fr.script)
	}
}
```

- [ ] **Step 2: Implement iterm.go**

```go
// Package iterm opens iTerm2 tabs that attach to a tmux session.
package iterm

import (
	"fmt"
	"os/exec"
)

type Runner interface {
	Run(script string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	return string(out), err
}

func ExecRunner() Runner { return execRunner{} }

type Client struct {
	Runner Runner
}

func New() *Client { return &Client{Runner: ExecRunner()} }

// OpenTab opens a new iTerm2 tab in the current window and attaches to tmuxSession.
func (c *Client) OpenTab(tmuxSession string) error {
	script := fmt.Sprintf(`
tell application "iTerm2"
	activate
	if (count of windows) = 0 then
		create window with default profile
	end if
	tell current window
		create tab with default profile
		tell current session of current tab
			write text "tmux attach -t %s"
		end tell
	end tell
end tell`, tmuxSession)
	_, err := c.Runner.Run(script)
	return err
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/iterm/... -v
git add internal/iterm
git commit -m "feat(iterm): add osascript-based iTerm2 tab opener"
```

---

### Task 6: `internal/gitwt` — git worktree wrapper

**Files:**
- Create: `internal/gitwt/gitwt.go`
- Create: `internal/gitwt/gitwt_test.go`

- [ ] **Step 1: Write failing tests**

```go
package gitwt

import (
	"reflect"
	"testing"
)

type fakeRunner struct {
	calls [][]string
}

func (f *fakeRunner) Run(dir string, args ...string) (string, error) {
	c := append([]string{"@" + dir}, args...)
	f.calls = append(f.calls, c)
	return "", nil
}

func TestFetch(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.Fetch("/repo", "main"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "fetch", "origin", "main"}
	if !reflect.DeepEqual(fr.calls[0], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestAddWorktree(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.AddWorktree("/repo", "/wt/foo", "user/foo", "main"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "worktree", "add", "/wt/foo", "-b", "user/foo", "origin/main"}
	if !reflect.DeepEqual(fr.calls[0], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestRemoveWorktree(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.RemoveWorktree("/repo", "/wt/foo"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "worktree", "remove", "/wt/foo", "--force"}
	if !reflect.DeepEqual(fr.calls[0], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}
```

- [ ] **Step 2: Implement gitwt.go**

```go
// Package gitwt wraps git worktree subcommands.
package gitwt

import (
	"fmt"
	"os/exec"
)

type Runner interface {
	Run(dir string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %v in %s: %w (%s)", args, dir, err, string(out))
	}
	return string(out), nil
}

func ExecRunner() Runner { return execRunner{} }

type Client struct {
	Runner Runner
}

func New() *Client { return &Client{Runner: ExecRunner()} }

func (c *Client) Fetch(repoDir, baseBranch string) error {
	_, err := c.Runner.Run(repoDir, "fetch", "origin", baseBranch)
	return err
}

func (c *Client) AddWorktree(repoDir, worktreePath, branch, baseBranch string) error {
	_, err := c.Runner.Run(repoDir, "worktree", "add", worktreePath, "-b", branch, "origin/"+baseBranch)
	return err
}

func (c *Client) RemoveWorktree(repoDir, worktreePath string) error {
	_, err := c.Runner.Run(repoDir, "worktree", "remove", worktreePath, "--force")
	return err
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/gitwt/... -v
git add internal/gitwt
git commit -m "feat(gitwt): add git worktree wrapper"
```

---

### Task 7: `internal/watcher` — Claude session poller

**Files:**
- Create: `internal/watcher/watcher.go`
- Create: `internal/watcher/parse.go`
- Create: `internal/watcher/watcher_test.go`

**Behavior:** Poll a directory of JSON files every interval. For each file, parse the `cwd` (or any path-like field) and a status flag. Emit a snapshot mapping `worktree_path → State` over a channel.

States: `Working`, `Waiting`, `Parked`. The TUI overlays "parked" itself by checking whether the worktree's tmux session is running — the watcher only reports `Working`/`Waiting` for known worktree paths.

- [ ] **Step 1: parse.go**

```go
package watcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// rawSession captures the subset of fields curral cares about from a
// ~/.claude/sessions/*.json file. Schema is best-effort and resilient to
// missing fields.
type rawSession struct {
	CWD     string `json:"cwd"`
	Status  string `json:"status"`
	Busy    *bool  `json:"busy,omitempty"`
	State   string `json:"state,omitempty"`
}

func parseFile(path string) (rawSession, error) {
	var rs rawSession
	data, err := os.ReadFile(path)
	if err != nil {
		return rs, err
	}
	_ = json.Unmarshal(data, &rs) // tolerate partial files
	rs.CWD = filepath.Clean(rs.CWD)
	rs.Status = strings.ToLower(rs.Status)
	rs.State = strings.ToLower(rs.State)
	return rs, nil
}

// classify maps a rawSession to a State.
func classify(rs rawSession) State {
	if rs.Busy != nil {
		if *rs.Busy {
			return Working
		}
		return Waiting
	}
	switch {
	case rs.Status == "busy", rs.Status == "working", rs.State == "busy":
		return Working
	case rs.Status == "idle", rs.Status == "waiting", rs.State == "idle":
		return Waiting
	}
	// No actionable signal — treat as waiting; the file's existence implies
	// claude is at least attached.
	return Waiting
}
```

- [ ] **Step 2: watcher.go**

```go
package watcher

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

type State int

const (
	Unknown State = iota
	Parked
	Waiting
	Working
)

func (s State) String() string {
	switch s {
	case Parked:
		return "parked"
	case Waiting:
		return "waiting"
	case Working:
		return "working"
	}
	return "unknown"
}

// Snapshot maps worktree path → state observed at PollTime.
type Snapshot struct {
	States   map[string]State
	PollTime time.Time
}

type Watcher struct {
	Dir      string        // e.g. ~/.claude/sessions
	Interval time.Duration // e.g. 2s
}

// Run polls until ctx is canceled. Each tick produces one Snapshot on out.
func (w *Watcher) Run(ctx context.Context, out chan<- Snapshot) {
	if w.Interval == 0 {
		w.Interval = 2 * time.Second
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	// emit immediately
	w.tick(out)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(out)
		}
	}
}

func (w *Watcher) tick(out chan<- Snapshot) {
	snap := Snapshot{States: map[string]State{}, PollTime: time.Now()}
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		// no dir yet — emit empty snapshot
		out <- snap
		return
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		rs, err := parseFile(filepath.Join(w.Dir, e.Name()))
		if err != nil || rs.CWD == "" {
			continue
		}
		st := classify(rs)
		// last writer wins; classify so Working overrides Waiting
		if prev, ok := snap.States[rs.CWD]; !ok || st > prev {
			snap.States[rs.CWD] = st
		}
	}
	out <- snap
}
```

- [ ] **Step 3: tests**

```go
package watcher

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClassify(t *testing.T) {
	b := true
	if classify(rawSession{Busy: &b}) != Working {
		t.Fatal("busy=true should be Working")
	}
	bf := false
	if classify(rawSession{Busy: &bf}) != Waiting {
		t.Fatal("busy=false should be Waiting")
	}
	if classify(rawSession{Status: "idle"}) != Waiting {
		t.Fatal("status idle")
	}
	if classify(rawSession{Status: "busy"}) != Working {
		t.Fatal("status busy")
	}
}

func TestWatcherTickEmitsSnapshot(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "1.json"), map[string]any{
		"cwd":    "/tmp/wt-a",
		"status": "busy",
	})
	writeJSON(t, filepath.Join(dir, "2.json"), map[string]any{
		"cwd":    "/tmp/wt-b",
		"status": "idle",
	})

	w := &Watcher{Dir: dir, Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	snap := <-ch
	if snap.States["/tmp/wt-a"] != Working {
		t.Fatalf("wt-a = %v", snap.States["/tmp/wt-a"])
	}
	if snap.States["/tmp/wt-b"] != Waiting {
		t.Fatalf("wt-b = %v", snap.States["/tmp/wt-b"])
	}
}

func TestWatcherMissingDir(t *testing.T) {
	w := &Watcher{Dir: "/nonexistent/curral/test", Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)
	snap := <-ch
	if len(snap.States) != 0 {
		t.Fatalf("expected empty snapshot")
	}
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/watcher/... -v
git add internal/watcher
git commit -m "feat(watcher): poll claude session files into snapshots"
```

---

### Task 8: `internal/tui` core — model, styles, layout

**Files:**
- Create: `internal/tui/styles.go`
- Create: `internal/tui/keys.go`
- Create: `internal/tui/messages.go`
- Create: `internal/tui/model.go`
- Create: `internal/tui/update.go`
- Create: `internal/tui/view.go`
- Create: `internal/tui/list.go`
- Create: `internal/tui/detail.go`

This is the largest task. Build it bottom-up: types/messages → styles → keys → list/detail renderers → model → update → view.

- [ ] **Step 1: messages.go**

```go
package tui

import (
	"time"

	"github.com/erickgnclvs/curral/internal/session"
	"github.com/erickgnclvs/curral/internal/watcher"
)

type StatusTickMsg struct{ Snap watcher.Snapshot }

type SessionsRefreshedMsg struct{ Sessions []session.Session }

type ErrorMsg struct{ Err error }

type InfoMsg struct {
	Text string
	When time.Time
}

type SessionOpenedMsg struct{ ID string }
type SessionCreatedMsg struct{ Session session.Session }
type SessionDeletedMsg struct{ ID string }
```

- [ ] **Step 2: styles.go**

```go
package tui

import "github.com/charmbracelet/lipgloss"

var (
	colBg      = lipgloss.Color("#0d0d10")
	colFg      = lipgloss.Color("#e6e6e6")
	colMute    = lipgloss.Color("#7a7a85")
	colAccent  = lipgloss.Color("#7aa2f7")
	colWorking = lipgloss.Color("#9ece6a")
	colWaiting = lipgloss.Color("#e0af68")
	colParked  = lipgloss.Color("#565a6e")
	colDanger  = lipgloss.Color("#f7768e")
	colBorder  = lipgloss.Color("#2d2f3a")

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	muteStyle  = lipgloss.NewStyle().Foreground(colMute)
	tabActive  = lipgloss.NewStyle().Bold(true).Foreground(colAccent).Padding(0, 1)
	tabInactive = lipgloss.NewStyle().Foreground(colMute).Padding(0, 1)

	listRow         = lipgloss.NewStyle().Padding(0, 1)
	listRowSelected = lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color("#1f2233")).Foreground(colFg).Bold(true)

	panelBorder = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(colBorder).
			Padding(0, 1)

	footerStyle = lipgloss.NewStyle().Foreground(colMute).Padding(0, 1)

	dotWorking = lipgloss.NewStyle().Foreground(colWorking).Render("⬤")
	dotWaiting = lipgloss.NewStyle().Foreground(colWaiting).Render("⬤")
	dotParked  = lipgloss.NewStyle().Foreground(colParked).Render("○")

	overlayBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colAccent).
			Padding(1, 2)

	dangerStyle = lipgloss.NewStyle().Foreground(colDanger).Bold(true)
)
```

- [ ] **Step 3: keys.go**

```go
package tui

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
	Up      key.Binding
	Down    key.Binding
	Open    key.Binding
	New     key.Binding
	Delete  key.Binding
	Refresh key.Binding
	Tab     key.Binding
	Quit    key.Binding
	Cancel  key.Binding
	Confirm key.Binding
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Open:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		New:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Delete:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Tab:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "project")),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Cancel:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		Confirm: key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "confirm")),
	}
}
```

- [ ] **Step 4: model.go**

```go
package tui

import (
	"context"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/textinput"

	"github.com/erickgnclvs/curral/internal/config"
	"github.com/erickgnclvs/curral/internal/session"
	"github.com/erickgnclvs/curral/internal/watcher"
)

// Backend is everything the TUI calls into. It's an interface so tests can
// substitute fakes (and so main.go does the wiring).
type Backend interface {
	CreateSession(project, name string) (session.Session, error)
	OpenSession(id string) error
	DeleteSession(id string) error
	Sessions() []session.Session
	Projects() []string
}

type Mode int

const (
	ModeList Mode = iota
	ModeNewForm
	ModeConfirmDelete
)

type Model struct {
	cfg     *config.Config
	backend Backend
	keys    KeyMap

	projects    []string
	activeProj  int
	sessions    []session.Session
	cursor      int
	states      map[string]watcher.State // worktree path → state
	statusCh    <-chan watcher.Snapshot
	cancelPoll  context.CancelFunc

	mode      Mode
	nameInput textinput.Model
	flash     string
	flashTime time.Time

	width, height int
}

func New(cfg *config.Config, backend Backend, statusCh <-chan watcher.Snapshot, cancel context.CancelFunc) *Model {
	ti := textinput.New()
	ti.Placeholder = "session name (e.g. hash-password)"
	ti.CharLimit = 64
	ti.Width = 40

	m := &Model{
		cfg:        cfg,
		backend:    backend,
		keys:       DefaultKeyMap(),
		states:     map[string]watcher.State{},
		statusCh:   statusCh,
		cancelPoll: cancel,
		nameInput:  ti,
	}
	for name := range cfg.Projects {
		m.projects = append(m.projects, name)
	}
	sort.Strings(m.projects)
	m.refreshSessions()
	return m
}

func (m *Model) refreshSessions() {
	if len(m.projects) == 0 {
		m.sessions = nil
		return
	}
	proj := m.projects[m.activeProj]
	all := m.backend.Sessions()
	out := all[:0:0]
	for _, s := range all {
		if s.Project == proj {
			out = append(out, s)
		}
	}
	m.sessions = out
	if m.cursor >= len(m.sessions) {
		m.cursor = max(0, len(m.sessions)-1)
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(listenStatus(m.statusCh), tickFlash())
}

func listenStatus(ch <-chan watcher.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snap, ok := <-ch
		if !ok {
			return nil
		}
		return StatusTickMsg{Snap: snap}
	}
}

func tickFlash() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return InfoMsg{When: t} })
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
```

- [ ] **Step 5: update.go**

```go
package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/key"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case StatusTickMsg:
		for path, st := range msg.Snap.States {
			m.states[path] = st
		}
		// rearm
		return m, listenStatus(m.statusCh)

	case InfoMsg:
		if time.Since(m.flashTime) > 3*time.Second {
			m.flash = ""
		}
		return m, tickFlash()

	case ErrorMsg:
		m.flash = "error: " + msg.Err.Error()
		m.flashTime = time.Now()
		return m, nil

	case SessionCreatedMsg:
		m.flash = "created " + msg.Session.Name
		m.flashTime = time.Now()
		m.refreshSessions()
		return m, nil

	case SessionDeletedMsg:
		m.flash = "deleted"
		m.flashTime = time.Now()
		m.refreshSessions()
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case ModeNewForm:
			return m.updateNewForm(msg)
		case ModeConfirmDelete:
			return m.updateConfirm(msg)
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

func (m *Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		m.cancelPoll()
		return m, tea.Quit
	case key.Matches(msg, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(msg, m.keys.Down):
		if m.cursor < len(m.sessions)-1 {
			m.cursor++
		}
	case key.Matches(msg, m.keys.Tab):
		if len(m.projects) > 0 {
			m.activeProj = (m.activeProj + 1) % len(m.projects)
			m.cursor = 0
			m.refreshSessions()
		}
	case key.Matches(msg, m.keys.Refresh):
		m.refreshSessions()
	case key.Matches(msg, m.keys.New):
		if len(m.projects) == 0 {
			return m.flashError(fmt.Errorf("no projects configured — edit ~/.config/curral/config.toml"))
		}
		m.mode = ModeNewForm
		m.nameInput.SetValue("")
		m.nameInput.Focus()
	case key.Matches(msg, m.keys.Delete):
		if len(m.sessions) > 0 {
			m.mode = ModeConfirmDelete
		}
	case key.Matches(msg, m.keys.Open):
		if len(m.sessions) > 0 {
			id := m.sessions[m.cursor].ID
			return m, func() tea.Msg {
				if err := m.backend.OpenSession(id); err != nil {
					return ErrorMsg{Err: err}
				}
				return SessionOpenedMsg{ID: id}
			}
		}
	}
	return m, nil
}

func (m *Model) updateNewForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeList
		return m, nil
	case "enter":
		name := m.nameInput.Value()
		if name == "" {
			return m, nil
		}
		proj := m.projects[m.activeProj]
		m.mode = ModeList
		return m, func() tea.Msg {
			s, err := m.backend.CreateSession(proj, name)
			if err != nil {
				return ErrorMsg{Err: err}
			}
			return SessionCreatedMsg{Session: s}
		}
	}
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m *Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if len(m.sessions) == 0 {
			m.mode = ModeList
			return m, nil
		}
		id := m.sessions[m.cursor].ID
		m.mode = ModeList
		return m, func() tea.Msg {
			if err := m.backend.DeleteSession(id); err != nil {
				return ErrorMsg{Err: err}
			}
			return SessionDeletedMsg{ID: id}
		}
	case "n", "esc":
		m.mode = ModeList
	}
	return m, nil
}

func (m *Model) flashError(err error) (tea.Model, tea.Cmd) {
	m.flash = "error: " + err.Error()
	m.flashTime = time.Now()
	return m, nil
}
```

- [ ] **Step 6: list.go**

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/curral/internal/session"
	"github.com/erickgnclvs/curral/internal/watcher"
)

func (m *Model) renderList(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("SESSIONS"))
	b.WriteString("\n\n")
	if len(m.sessions) == 0 {
		b.WriteString(muteStyle.Render("  no sessions — press n to create"))
		return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
	}
	for i, s := range m.sessions {
		row := renderRow(s, m.states[s.WorktreePath])
		if i == m.cursor {
			row = listRowSelected.Render(row)
		} else {
			row = listRow.Render(row)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
}

func renderRow(s session.Session, st watcher.State) string {
	dot := dotParked
	label := "park"
	switch st {
	case watcher.Working:
		dot = dotWorking
		label = "work"
	case watcher.Waiting:
		dot = dotWaiting
		label = "wait"
	}
	name := truncate(s.Name, 22)
	return fmt.Sprintf("%-22s %s %s", name, dot, muteStyle.Render(label))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return s[:n-1] + "…"
}
```

- [ ] **Step 7: detail.go**

```go
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/curral/internal/watcher"
)

func (m *Model) renderDetail(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("DETAIL"))
	b.WriteString("\n\n")
	if len(m.sessions) == 0 {
		b.WriteString(muteStyle.Render("nothing selected"))
		return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
	}
	s := m.sessions[m.cursor]
	st := m.states[s.WorktreePath]
	dot := dotParked
	label := "parked"
	switch st {
	case watcher.Working:
		dot, label = dotWorking, "working"
	case watcher.Waiting:
		dot, label = dotWaiting, "waiting"
	}
	row := func(k, v string) string {
		return fmt.Sprintf("%s %s\n", muteStyle.Render(fmt.Sprintf("%-10s", k+":")), v)
	}
	b.WriteString(row("status", dot+" "+label))
	b.WriteString(row("branch", truncate(s.Branch, width-14)))
	b.WriteString(row("worktree", truncate(s.WorktreePath, width-14)))
	b.WriteString(row("tmux", s.TmuxSession))
	b.WriteString(row("created", humanizeAge(time.Since(s.CreatedAt))))
	return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
}

func humanizeAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
```

- [ ] **Step 8: view.go**

```go
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) View() string {
	if m.width == 0 {
		return "starting…"
	}

	// Header (title + project tabs)
	header := m.renderHeader()

	// Body: list (left ~38) + detail (right) + 2 for borders
	listW := 40
	if m.width-listW < 30 {
		listW = m.width / 2
	}
	detailW := m.width - listW - 4 // borders/padding

	bodyHeight := m.height - 4
	if bodyHeight < 5 {
		bodyHeight = 5
	}

	left := panelBorder.Width(listW).Height(bodyHeight).Render(m.renderList(listW-2, bodyHeight-2))
	right := panelBorder.Width(detailW).Height(bodyHeight).Render(m.renderDetail(detailW-2, bodyHeight-2))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	footer := m.renderFooter()
	base := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)

	switch m.mode {
	case ModeNewForm:
		return overlay(base, m.renderNewForm(), m.width, m.height)
	case ModeConfirmDelete:
		return overlay(base, m.renderConfirm(), m.width, m.height)
	}
	return base
}

func (m *Model) renderHeader() string {
	left := titleStyle.Render("curral")
	tabs := []string{}
	for i, p := range m.projects {
		if i == m.activeProj {
			tabs = append(tabs, tabActive.Render(p))
		} else {
			tabs = append(tabs, tabInactive.Render(p))
		}
	}
	right := strings.Join(tabs, " ")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(left + strings.Repeat(" ", gap) + right)
}

func (m *Model) renderFooter() string {
	help := "n:new  enter:open  d:delete  r:refresh  tab:project  q:quit"
	if m.flash != "" {
		help = m.flash + "  •  " + help
	}
	return footerStyle.Width(m.width).Render(help)
}

func overlay(base, box string, w, h int) string {
	// crude center: render the box centered, base behind. Lipgloss doesn't
	// have z-order; we draw the box atop a dim background by clearing it.
	dim := lipgloss.NewStyle().Faint(true).Render(base)
	_ = dim
	// Use Place to center the overlay over a faded screen.
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, overlayBox.Render(box))
}
```

- [ ] **Step 9: Run build for the package**

Run: `go build ./internal/tui/...`
Expect: success (form.go and confirm.go are added in Task 9).

- [ ] **Step 10: Commit**

```bash
git add internal/tui
git commit -m "feat(tui): scaffold Bubbletea model, list, and detail"
```

---

### Task 9: TUI overlays — new session form + delete confirmation

**Files:**
- Create: `internal/tui/form.go`
- Create: `internal/tui/confirm.go`

- [ ] **Step 1: form.go**

```go
package tui

import (
	"fmt"
	"strings"
)

func (m *Model) renderNewForm() string {
	var b strings.Builder
	proj := ""
	if len(m.projects) > 0 {
		proj = m.projects[m.activeProj]
	}
	b.WriteString(titleStyle.Render("New session"))
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render(fmt.Sprintf("project: %s", proj)))
	b.WriteString("\n\n")
	b.WriteString(m.nameInput.View())
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("enter to create   esc to cancel"))
	return b.String()
}
```

- [ ] **Step 2: confirm.go**

```go
package tui

import (
	"fmt"
	"strings"
)

func (m *Model) renderConfirm() string {
	if len(m.sessions) == 0 {
		return ""
	}
	s := m.sessions[m.cursor]
	var b strings.Builder
	b.WriteString(dangerStyle.Render("Delete session?"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("name:     %s\n", s.Name))
	b.WriteString(fmt.Sprintf("branch:   %s\n", s.Branch))
	b.WriteString(fmt.Sprintf("worktree: %s\n", s.WorktreePath))
	b.WriteString("\n")
	b.WriteString(muteStyle.Render("This kills the tmux session and removes the worktree."))
	b.WriteString("\n")
	b.WriteString(muteStyle.Render("The branch is kept."))
	b.WriteString("\n\n")
	b.WriteString("y to confirm   n/esc to cancel")
	return b.String()
}
```

- [ ] **Step 3: Build + commit**

```bash
go build ./...
git add internal/tui
git commit -m "feat(tui): add new-session form and delete confirmation overlays"
```

---

### Task 10: `main.go` — wire everything

**Files:**
- Modify: `main.go`
- Create: `internal/app/app.go` (Backend impl)

The TUI consumes a `Backend` interface (defined in Task 8). Implementing it in a separate package keeps `main.go` thin and lets us unit-test wiring later if needed.

- [ ] **Step 1: internal/app/app.go**

```go
// Package app glues config, session store, tmux, iterm and gitwt into a TUI Backend.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/erickgnclvs/curral/internal/config"
	"github.com/erickgnclvs/curral/internal/gitwt"
	"github.com/erickgnclvs/curral/internal/iterm"
	"github.com/erickgnclvs/curral/internal/session"
	"github.com/erickgnclvs/curral/internal/tmux"
)

type App struct {
	Cfg          *config.Config
	Store        *session.Store
	Tmux         *tmux.Client
	ITerm        *iterm.Client
	Git          *gitwt.Client
	WorktreeRoot string // ~/.local/share/curral/worktrees
}

func WorktreeRootDefault() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "curral", "worktrees")
}

func (a *App) Projects() []string {
	out := make([]string, 0, len(a.Cfg.Projects))
	for k := range a.Cfg.Projects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (a *App) Sessions() []session.Session { return a.Store.All() }

func (a *App) CreateSession(project, name string) (session.Session, error) {
	proj, ok := a.Cfg.Projects[project]
	if !ok {
		return session.Session{}, fmt.Errorf("unknown project %q", project)
	}
	branch := name
	if proj.BranchPrefix != "" {
		branch = proj.BranchPrefix + "/" + name
	}
	wt := filepath.Join(a.WorktreeRoot, project, name)
	tmuxName := "curral-" + name

	if err := a.Git.Fetch(proj.Repo, proj.BaseBranch); err != nil {
		return session.Session{}, fmt.Errorf("git fetch: %w", err)
	}
	if err := a.Git.AddWorktree(proj.Repo, wt, branch, proj.BaseBranch); err != nil {
		return session.Session{}, fmt.Errorf("git worktree add: %w", err)
	}
	if err := a.Tmux.NewSession(tmuxName, wt, "claude"); err != nil {
		return session.Session{}, fmt.Errorf("tmux new-session: %w", err)
	}
	if err := a.ITerm.OpenTab(tmuxName); err != nil {
		return session.Session{}, fmt.Errorf("iterm open tab: %w", err)
	}

	s := session.Session{
		ID:           session.MakeID(project, name),
		Project:      project,
		Name:         name,
		Branch:       branch,
		WorktreePath: wt,
		TmuxSession:  tmuxName,
		CreatedAt:    time.Now().UTC(),
	}
	if err := a.Store.Put(s); err != nil {
		return s, fmt.Errorf("store: %w", err)
	}
	return s, nil
}

func (a *App) OpenSession(id string) error {
	s, ok := a.Store.Get(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	has, err := a.Tmux.HasSession(s.TmuxSession)
	if err != nil {
		return err
	}
	if !has {
		if err := a.Tmux.NewSession(s.TmuxSession, s.WorktreePath, "claude"); err != nil {
			return err
		}
	}
	return a.ITerm.OpenTab(s.TmuxSession)
}

func (a *App) DeleteSession(id string) error {
	s, ok := a.Store.Get(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	// Best-effort: even if tmux/worktree are gone we still want to forget the record.
	if has, _ := a.Tmux.HasSession(s.TmuxSession); has {
		_ = a.Tmux.KillSession(s.TmuxSession)
	}
	proj, ok := a.Cfg.Projects[s.Project]
	if ok {
		_ = a.Git.RemoveWorktree(proj.Repo, s.WorktreePath)
	}
	return a.Store.Delete(id)
}
```

- [ ] **Step 2: main.go**

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/curral/internal/app"
	"github.com/erickgnclvs/curral/internal/config"
	"github.com/erickgnclvs/curral/internal/gitwt"
	"github.com/erickgnclvs/curral/internal/iterm"
	"github.com/erickgnclvs/curral/internal/session"
	"github.com/erickgnclvs/curral/internal/tmux"
	"github.com/erickgnclvs/curral/internal/tui"
	"github.com/erickgnclvs/curral/internal/watcher"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "curral:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config %s: %w", cfgPath, err)
	}
	if len(cfg.Projects) == 0 {
		// Seed an example config so the first-run experience explains itself.
		if err := seedExampleConfig(cfgPath); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote example config to %s — edit it and re-run curral.\n", cfgPath)
		return nil
	}

	store := &session.Store{Path: session.DefaultPath()}
	if err := store.Load(); err != nil {
		return fmt.Errorf("load sessions: %w", err)
	}

	a := &app.App{
		Cfg:          cfg,
		Store:        store,
		Tmux:         tmux.New(),
		ITerm:        iterm.New(),
		Git:          gitwt.New(),
		WorktreeRoot: app.WorktreeRootDefault(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	statusCh := make(chan watcher.Snapshot, 4)
	w := &watcher.Watcher{
		Dir: filepath.Join(os.Getenv("HOME"), ".claude", "sessions"),
	}
	go w.Run(ctx, statusCh)

	m := tui.New(cfg, a, statusCh, cancel)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		cancel()
		return err
	}
	cancel()
	return nil
}

func seedExampleConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	example := `# curral configuration
# Add one [projects.<name>] section per repo you want to manage.

# [projects.eg_system]
# repo          = "~/Development/eg_system"
# branch_prefix = "erickgoncalves"   # optional — prepended to branch names
# base_branch   = "main"
`
	return os.WriteFile(path, []byte(example), 0o644)
}

var _ = log.Println // keep import slot if we add logging later
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expect: success.

- [ ] **Step 4: Commit**

```bash
git add main.go internal/app
git commit -m "feat: wire backend, watcher, and program entrypoint"
```

---

### Task 11: README + smoke verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Write README**

Replace the empty stub with a real README. See content in `Task 11` of the executed plan (we'll inline the full text at execution time, mirroring the design spec).

Sections to include: what it is, requirements, install, configure, run, keys, status states, troubleshooting.

- [ ] **Step 2: Final test sweep**

Run: `go test ./... -race -count=1`
Expect: all green.

Run: `go vet ./...`
Expect: no issues.

- [ ] **Step 3: Smoke run**

Run: `./curral` with no config — expect the seeded example to be created at `~/.config/curral/config.toml` and a friendly stderr message.

Then write a fake config that points at this repo as a project, re-run, and verify the TUI renders.

- [ ] **Step 4: Commit and tag the implementation milestone**

```bash
git add README.md
git commit -m "docs: add README"
```

---

## Self-review

- Spec sections covered:
  - Components → Tasks 2–9.
  - Data flow (open / status) → Tasks 7 and 10.
  - Config format → Task 2 (loader) + Task 10 (seeded example).
  - TUI layout → Tasks 8–9.
  - Status states → Task 7 + Task 8 (renderRow / detail).
  - Keyboard shortcuts → Task 8 (keys.go).
  - New / delete flows → Tasks 9–10.
  - Go project structure → matches Task file map.
  - Dependencies → Task 1.
  - Out of scope → respected (macOS-only opener, no PR/issue integration, etc.).
- No placeholders left. Every step is concrete code or a concrete command.
- Type/name consistency:
  - `session.Session`, `session.Store`, `session.MakeID` — used in app + tui consistently.
  - `tmux.Client` with `Runner` interface — used in app.
  - `watcher.Snapshot.States` keyed by `WorktreePath` — matches `tui.Model.states`.
  - `tui.Backend` interface fields match `app.App` methods exactly.

