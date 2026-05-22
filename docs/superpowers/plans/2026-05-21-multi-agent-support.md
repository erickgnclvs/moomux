# Multi-Agent Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Codex and OpenCode as first-class agents alongside Claude, letting each project declare which agent to run.

**Architecture:** Agent is configured per-project in `config.toml` (defaulting to `claude`). The `Session` struct gains an `Agent` field so every session knows which binary launched it. The monolithic `watcher.Watcher` struct becomes a `Watcher` interface; Claude and Codex share a file-based `DirWatcher`, while OpenCode gets an HTTP-polling `OpenCodeWatcher` (one per session, each on its own port). A `MultiWatcher` fans multiple watcher streams into the single channel the TUI already consumes.

**Tech Stack:** Go 1.22+, `encoding/json`, `net/http`, `net/http/httptest` (tests), Bubbletea TUI, `github.com/BurntSushi/toml`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `internal/session/session.go` | Add `Agent`, `AgentPort` fields |
| Modify | `internal/config/config.go` | Add `Agent` field to Project |
| Modify | `internal/watcher/watcher.go` | Add `Watcher` interface; rename struct → `DirWatcher` |
| Modify | `internal/watcher/watcher_test.go` | Update to use `DirWatcher` |
| Create | `internal/watcher/opencode.go` | `OpenCodeWatcher` — HTTP poll per session |
| Create | `internal/watcher/opencode_test.go` | Tests using `httptest.NewServer` |
| Create | `internal/watcher/multi.go` | `MultiWatcher` — fans sub-watchers into one channel |
| Create | `internal/watcher/multi_test.go` | Tests for multi-watcher merge |
| Modify | `internal/app/app.go` | `agentCmd()`, `nextOpenCodePort()`, wire agent into CreateSession/OpenSession |
| Modify | `main.go` | Build `MultiWatcher` from agent inventory |
| Modify | `internal/tui/detail.go` | Show `agent` row in detail panel |

---

## Task 1: Add Agent fields to Session and Project config

**Files:**
- Modify: `internal/session/session.go`
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add `Agent` and `AgentPort` to `session.Session`**

Replace the `Session` struct in `internal/session/session.go`:

```go
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
}
```

- [ ] **Step 2: Add a helper that normalizes empty agent to "claude"**

Add after the struct in `internal/session/session.go`:

```go
// AgentName returns the effective agent name, defaulting to "claude" for legacy sessions.
func (s Session) AgentName() string {
	if s.Agent == "" {
		return "claude"
	}
	return s.Agent
}
```

- [ ] **Step 3: Add `Agent` to `config.Project`**

In `internal/config/config.go`, update the `Project` struct:

```go
type Project struct {
	Kind         string `toml:"kind,omitempty"`          // "git" (default) or "plain"
	Repo         string `toml:"repo"`
	BranchPrefix string `toml:"branch_prefix,omitempty"`
	BaseBranch   string `toml:"base_branch,omitempty"`
	Agent        string `toml:"agent,omitempty"`         // "claude" (default), "codex", "opencode"
}
```

- [ ] **Step 4: Add a helper that normalizes empty agent to "claude"**

Add after the struct in `internal/config/config.go`:

```go
// AgentName returns the effective agent name, defaulting to "claude".
func (p Project) AgentName() string {
	if p.Agent == "" {
		return "claude"
	}
	return p.Agent
}
```

- [ ] **Step 5: Run existing tests to confirm nothing is broken**

```bash
go test ./internal/session/... ./internal/config/... -v
```

Expected: all existing tests pass (new fields are `omitempty` so JSON round-trips are unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/session/session.go internal/config/config.go
git commit -m "feat: add Agent and AgentPort fields to Session and Project config"
```

---

## Task 2: Refactor `watcher.Watcher` struct into a `Watcher` interface + `DirWatcher`

**Files:**
- Modify: `internal/watcher/watcher.go`
- Modify: `internal/watcher/watcher_test.go`

The current `Watcher` struct becomes `DirWatcher`. We add a `Watcher` interface so all agent-specific watchers are interchangeable.

- [ ] **Step 1: Rewrite `internal/watcher/watcher.go`**

```go
// Package watcher polls agent session state and emits Snapshots.
package watcher

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// State describes what a session is doing.
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

// Watcher is implemented by every agent-specific watcher.
type Watcher interface {
	Run(ctx context.Context, out chan<- Snapshot)
}

// DirWatcher polls a directory of JSON session files (used by Claude and Codex).
type DirWatcher struct {
	Dir      string
	Interval time.Duration
}

// Run polls until ctx is canceled. Each tick produces one Snapshot on out.
func (w *DirWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	if w.Interval == 0 {
		w.Interval = 2 * time.Second
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	w.tick(ctx, out)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx, out)
		}
	}
}

func (w *DirWatcher) tick(ctx context.Context, out chan<- Snapshot) {
	snap := Snapshot{States: map[string]State{}, PollTime: time.Now()}
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		send(ctx, out, snap)
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
		if prev, ok := snap.States[rs.CWD]; !ok || st > prev {
			snap.States[rs.CWD] = st
		}
	}
	send(ctx, out, snap)
}

func send(ctx context.Context, out chan<- Snapshot, snap Snapshot) {
	select {
	case out <- snap:
	case <-ctx.Done():
	}
}
```

- [ ] **Step 2: Update `internal/watcher/watcher_test.go` to use `DirWatcher`**

Replace every `&Watcher{` with `&DirWatcher{`:

```go
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

	w := &DirWatcher{Dir: dir, Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.States["/tmp/wt-a"] != Working {
			t.Fatalf("wt-a = %v", snap.States["/tmp/wt-a"])
		}
		if snap.States["/tmp/wt-b"] != Waiting {
			t.Fatalf("wt-b = %v", snap.States["/tmp/wt-b"])
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for snapshot")
	}
}

func TestWatcherMissingDir(t *testing.T) {
	w := &DirWatcher{Dir: "/nonexistent/moomux/test", Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)
	select {
	case snap := <-ch:
		if len(snap.States) != 0 {
			t.Fatalf("expected empty snapshot, got %v", snap.States)
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}
```

- [ ] **Step 3: Fix the reference in `main.go`**

In `main.go`, change:
```go
w := &watcher.Watcher{
    Dir: filepath.Join(home, ".claude", "sessions"),
}
go w.Run(ctx, statusCh)
```
to (temporary — Task 6 replaces this entirely):
```go
w := &watcher.DirWatcher{
    Dir: filepath.Join(home, ".claude", "sessions"),
}
go w.Run(ctx, statusCh)
```

- [ ] **Step 4: Run all tests**

```bash
go test ./... -race
```

Expected: all pass (rename is mechanical, no logic changed).

- [ ] **Step 5: Commit**

```bash
git add internal/watcher/watcher.go internal/watcher/watcher_test.go main.go
git commit -m "refactor: rename watcher.Watcher to DirWatcher, add Watcher interface"
```

---

## Task 3: Implement `OpenCodeWatcher`

OpenCode exposes a REST API at `localhost:{port}/session/status` returning `map[sessionID]string` where values are `"busy"` or `"idle"`. Each moomux session that uses OpenCode has its own HTTP server on a unique port.

**Files:**
- Create: `internal/watcher/opencode.go`
- Create: `internal/watcher/opencode_test.go`

- [ ] **Step 1: Write a failing test for OpenCodeWatcher**

Create `internal/watcher/opencode_test.go`:

```go
package watcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenCodeWatcherBusy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/status" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"sess-1": "busy",
			"sess-2": "idle",
		})
	}))
	defer srv.Close()

	entries := []OpenCodeEntry{{WorktreePath: "/tmp/wt-oc", URL: srv.URL}}
	w := &OpenCodeWatcher{Entries: entries, Interval: 10 * time.Millisecond, Client: srv.Client()}
	ch := make(chan Snapshot, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.States["/tmp/wt-oc"] != Working {
			t.Fatalf("expected Working, got %v", snap.States["/tmp/wt-oc"])
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}

func TestOpenCodeWatcherIdle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/status" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"sess-1": "idle",
		})
	}))
	defer srv.Close()

	entries := []OpenCodeEntry{{WorktreePath: "/tmp/wt-oc", URL: srv.URL}}
	w := &OpenCodeWatcher{Entries: entries, Interval: 10 * time.Millisecond, Client: srv.Client()}
	ch := make(chan Snapshot, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.States["/tmp/wt-oc"] != Waiting {
			t.Fatalf("expected Waiting, got %v", snap.States["/tmp/wt-oc"])
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}

func TestOpenCodeWatcherUnreachable(t *testing.T) {
	// Port 1 is always unreachable; watcher should emit Waiting (server not up yet).
	entries := []OpenCodeEntry{{WorktreePath: "/tmp/wt-oc", URL: "http://127.0.0.1:1"}}
	w := &OpenCodeWatcher{Entries: entries, Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.States["/tmp/wt-oc"] != Waiting {
			t.Fatalf("expected Waiting when unreachable, got %v", snap.States["/tmp/wt-oc"])
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}
```

- [ ] **Step 2: Run test — expect compilation failure**

```bash
go test ./internal/watcher/... -run TestOpenCode -v
```

Expected: fails with `undefined: OpenCodeEntry` and `undefined: OpenCodeWatcher`.

- [ ] **Step 3: Implement `internal/watcher/opencode.go`**

```go
package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OpenCodeEntry maps one moomux worktree to its OpenCode HTTP server URL.
type OpenCodeEntry struct {
	WorktreePath string
	URL          string // e.g. "http://127.0.0.1:4096"
}

// OpenCodeWatcher polls each OpenCode session's HTTP API for status.
// Unreachable servers are treated as Waiting (OpenCode may still be starting up).
type OpenCodeWatcher struct {
	Entries  []OpenCodeEntry
	Interval time.Duration
	Client   *http.Client // nil = default client with short timeout
}

func (w *OpenCodeWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	if w.Interval == 0 {
		w.Interval = 2 * time.Second
	}
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	w.tick(ctx, out, client)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx, out, client)
		}
	}
}

func (w *OpenCodeWatcher) tick(ctx context.Context, out chan<- Snapshot, client *http.Client) {
	snap := Snapshot{States: map[string]State{}, PollTime: time.Now()}
	for _, e := range w.Entries {
		snap.States[e.WorktreePath] = pollOpenCode(client, e.URL)
	}
	send(ctx, out, snap)
}

func pollOpenCode(client *http.Client, baseURL string) State {
	resp, err := client.Get(fmt.Sprintf("%s/session/status", baseURL))
	if err != nil {
		return Waiting // server not up yet
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Waiting
	}
	var statuses map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		return Waiting
	}
	for _, v := range statuses {
		if v == "busy" {
			return Working
		}
	}
	return Waiting
}
```

- [ ] **Step 4: Run tests — expect pass**

```bash
go test ./internal/watcher/... -run TestOpenCode -v
```

Expected: `TestOpenCodeWatcherBusy PASS`, `TestOpenCodeWatcherIdle PASS`, `TestOpenCodeWatcherUnreachable PASS`.

- [ ] **Step 5: Run all watcher tests**

```bash
go test ./internal/watcher/... -race -v
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/watcher/opencode.go internal/watcher/opencode_test.go
git commit -m "feat: add OpenCodeWatcher with HTTP polling"
```

---

## Task 4: Implement `MultiWatcher`

`MultiWatcher` starts N sub-watchers and fans their snapshots into the single channel the TUI already consumes.

**Files:**
- Create: `internal/watcher/multi.go`
- Create: `internal/watcher/multi_test.go`

- [ ] **Step 1: Write failing tests for MultiWatcher**

Create `internal/watcher/multi_test.go`:

```go
package watcher

import (
	"context"
	"testing"
	"time"
)

// staticWatcher emits one snapshot immediately and then blocks.
type staticWatcher struct {
	states map[string]State
}

func (s *staticWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	snap := Snapshot{States: s.states, PollTime: time.Now()}
	send(ctx, out, snap)
	<-ctx.Done()
}

func TestMultiWatcherMergesSnapshots(t *testing.T) {
	wa := &staticWatcher{states: map[string]State{"/wt-a": Working}}
	wb := &staticWatcher{states: map[string]State{"/wt-b": Waiting}}

	m := &MultiWatcher{Watchers: []Watcher{wa, wb}}
	ch := make(chan Snapshot, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go m.Run(ctx, ch)

	seen := map[string]State{}
	deadline := time.After(150 * time.Millisecond)
	for len(seen) < 2 {
		select {
		case snap := <-ch:
			for path, st := range snap.States {
				seen[path] = st
			}
		case <-deadline:
			t.Fatalf("timed out; seen so far: %v", seen)
		}
	}
	if seen["/wt-a"] != Working {
		t.Fatalf("/wt-a = %v, want Working", seen["/wt-a"])
	}
	if seen["/wt-b"] != Waiting {
		t.Fatalf("/wt-b = %v, want Waiting", seen["/wt-b"])
	}
}

func TestMultiWatcherEmpty(t *testing.T) {
	m := &MultiWatcher{}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// Should not block or panic with zero watchers.
	go m.Run(ctx, ch)
	<-ctx.Done()
}
```

- [ ] **Step 2: Run test — expect compilation failure**

```bash
go test ./internal/watcher/... -run TestMulti -v
```

Expected: fails with `undefined: MultiWatcher`.

- [ ] **Step 3: Implement `internal/watcher/multi.go`**

```go
package watcher

import "context"

// MultiWatcher fans the output of multiple Watchers into one Snapshot channel.
type MultiWatcher struct {
	Watchers []Watcher
}

func (m *MultiWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	if len(m.Watchers) == 0 {
		<-ctx.Done()
		return
	}
	merged := make(chan Snapshot, len(m.Watchers)*4)
	for _, w := range m.Watchers {
		go w.Run(ctx, merged)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case snap := <-merged:
			send(ctx, out, snap)
		}
	}
}
```

- [ ] **Step 4: Run tests — expect pass**

```bash
go test ./internal/watcher/... -race -v
```

Expected: all pass including `TestMultiWatcherMergesSnapshots` and `TestMultiWatcherEmpty`.

- [ ] **Step 5: Commit**

```bash
git add internal/watcher/multi.go internal/watcher/multi_test.go
git commit -m "feat: add MultiWatcher to fan multiple agent watchers into one channel"
```

---

## Task 5: Update `app.go` for agent-based launch

**Files:**
- Modify: `internal/app/app.go`

Two changes:
1. Use the session's `Agent` field to pick the correct binary name (`claude`, `codex`, `opencode`).
2. For OpenCode sessions, assign a unique HTTP port before launching.

- [ ] **Step 1: Add `agentCmd` helper**

Add near the top of `app.go` (after imports):

```go
// agentCmd returns the CLI binary name for the given agent.
func agentCmd(agent string) string {
	switch agent {
	case "codex":
		return "codex"
	case "opencode":
		return "opencode"
	default: // "claude" or empty
		return "claude"
	}
}
```

- [ ] **Step 2: Add `nextOpenCodePort` helper**

Add to `app.go`:

```go
// nextOpenCodePort returns the next available port for an OpenCode session.
// It starts at 4096 and increments past any port already used by an existing OpenCode session.
func (a *App) nextOpenCodePort() int {
	port := 4096
	for _, s := range a.Store.All() {
		if s.AgentName() == "opencode" && s.AgentPort >= port {
			port = s.AgentPort + 1
		}
	}
	return port
}
```

- [ ] **Step 3: Update `CreateSession` to use agent and port**

Replace the hardcoded `"claude"` in `CreateSession`. The relevant section currently reads:

```go
if err := a.Tmux.NewSession(tmuxName, wt, "claude", name); err != nil {
```

Update the full `CreateSession` method to:
1. Look up the agent from the project config.
2. Assign a port if it's an OpenCode session.
3. Launch the correct binary (for OpenCode, append `--port <port>`).

Find this block in `CreateSession` (around line 76):
```go
if err := a.Tmux.NewSession(tmuxName, wt, "claude", name); err != nil {
```

Replace the entire block that creates the tmux session and the Session struct (lines 76–100 approximately). The updated section should be:

```go
	proj, ok := a.Cfg.Projects[project]  // already fetched above

	agent := proj.AgentName()
	cmd := agentCmd(agent)
	agentPort := 0
	if agent == "opencode" {
		agentPort = a.nextOpenCodePort()
		cmd = fmt.Sprintf("opencode --port %d", agentPort)
	}

	if err := a.Tmux.NewSession(tmuxName, wt, cmd, name); err != nil {
		slog.Error("tmux new-session failed", "name", tmuxName, "cwd", wt, "err", err)
		return session.Session{}, fmt.Errorf("tmux new-session: %w", err)
	}
	slog.Info("tmux session created", "name", tmuxName)
	if err := a.Terminal.OpenSession(tmuxName, name); err != nil {
		slog.Error("terminal open failed", "tmux_session", tmuxName, "name", name, "err", err)
		return session.Session{}, fmt.Errorf("terminal open: %w", err)
	}
	slog.Info("terminal opened", "tmux_session", tmuxName)

	s := session.Session{
		ID:           session.MakeID(project, name),
		Project:      project,
		Name:         name,
		Branch:       branch,
		WorktreePath: wt,
		TmuxSession:  tmuxName,
		CreatedAt:    time.Now().UTC(),
		Agent:        agent,
		AgentPort:    agentPort,
	}
```

- [ ] **Step 4: Update `OpenSession` to use stored agent**

In `OpenSession`, the recreate path currently hardcodes `"claude"`:
```go
if err := a.Tmux.NewSession(s.TmuxSession, s.WorktreePath, "claude", s.Name); err != nil {
```

Replace with:
```go
	cmd := agentCmd(s.AgentName())
	if s.AgentName() == "opencode" && s.AgentPort > 0 {
		cmd = fmt.Sprintf("opencode --port %d", s.AgentPort)
	}
	if err := a.Tmux.NewSession(s.TmuxSession, s.WorktreePath, cmd, s.Name); err != nil {
		slog.Error("NewSession failed", "id", id, "tmux_session", s.TmuxSession, "cwd", s.WorktreePath, "err", err)
		return err
	}
```

- [ ] **Step 5: Verify the build compiles**

```bash
go build ./...
```

Expected: clean build.

- [ ] **Step 6: Run all tests**

```bash
go test ./... -race
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/app/app.go
git commit -m "feat: use per-session agent to pick launch command; assign OpenCode port"
```

---

## Task 6: Wire `MultiWatcher` in `main.go`

Replace the single `DirWatcher` with a `MultiWatcher` that includes:
- Claude `DirWatcher` (always included — handles legacy sessions)
- Codex `DirWatcher` (always included — no harm if `~/.codex/sessions/` doesn't exist)
- `OpenCodeWatcher` with entries built from all OpenCode sessions in the store

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Rewrite the watcher setup block in `main.go`**

Replace the current block:
```go
w := &watcher.DirWatcher{
    Dir: filepath.Join(home, ".claude", "sessions"),
}
go w.Run(ctx, statusCh)
```

With:
```go
	multi := buildWatcher(home, store.All())
	go multi.Run(ctx, statusCh)
```

- [ ] **Step 2: Add the `buildWatcher` function to `main.go`**

Add below `run()`:

```go
func buildWatcher(home string, sessions []session.Session) watcher.Watcher {
	watchers := []watcher.Watcher{
		// Always watch Claude and Codex dirs; they emit empty snapshots if missing.
		&watcher.DirWatcher{Dir: filepath.Join(home, ".claude", "sessions")},
		&watcher.DirWatcher{Dir: filepath.Join(home, ".codex", "sessions")},
	}

	var ocEntries []watcher.OpenCodeEntry
	for _, s := range sessions {
		if s.AgentName() == "opencode" && s.AgentPort > 0 {
			ocEntries = append(ocEntries, watcher.OpenCodeEntry{
				WorktreePath: s.WorktreePath,
				URL:          fmt.Sprintf("http://127.0.0.1:%d", s.AgentPort),
			})
		}
	}
	if len(ocEntries) > 0 {
		watchers = append(watchers, &watcher.OpenCodeWatcher{Entries: ocEntries})
	}

	return &watcher.MultiWatcher{Watchers: watchers}
}
```

- [ ] **Step 3: Add the missing `session` import if needed**

Check the imports in `main.go` — `session` is already imported. Add `fmt` if not present.

The imports block should include:
```go
import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/app"
	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/terminal"
	"github.com/erickgnclvs/moomux/internal/tmux"
	"github.com/erickgnclvs/moomux/internal/tui"
	"github.com/erickgnclvs/moomux/internal/watcher"
)
```

- [ ] **Step 4: Build and run tests**

```bash
go build ./... && go test ./... -race
```

Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add main.go
git commit -m "feat: wire MultiWatcher in main — Claude + Codex + OpenCode watchers"
```

---

## Task 7: Show agent in TUI detail panel

Add an `agent` row to the detail panel so users can see which agent is running in each session.

**Files:**
- Modify: `internal/tui/detail.go`

- [ ] **Step 1: Add `agent` row to `renderDetail`**

In `internal/tui/detail.go`, find this block after the `row` helper is defined (around line 38):

```go
	b.WriteString(row("status", dot+"  "+label))
	b.WriteString(row("name", truncate(s.Name, valueWidth)))
	b.WriteString(row("branch", truncate(s.Branch, valueWidth)))
	b.WriteString(row("worktree", truncate(s.WorktreePath, valueWidth)))
	b.WriteString(row("tmux", s.TmuxSession))
	b.WriteString(row("created", humanizeAge(time.Since(s.CreatedAt))))
```

Replace with:

```go
	b.WriteString(row("status", dot+"  "+label))
	b.WriteString(row("agent", s.AgentName()))
	b.WriteString(row("name", truncate(s.Name, valueWidth)))
	b.WriteString(row("branch", truncate(s.Branch, valueWidth)))
	b.WriteString(row("worktree", truncate(s.WorktreePath, valueWidth)))
	b.WriteString(row("tmux", s.TmuxSession))
	b.WriteString(row("created", humanizeAge(time.Since(s.CreatedAt))))
```

- [ ] **Step 2: Build**

```bash
go build ./...
```

Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/detail.go
git commit -m "feat: show agent name in session detail panel"
```

---

## Task 8: End-to-end smoke test and PR

- [ ] **Step 1: Run the full test suite one final time**

```bash
go test ./... -race -count=1
```

Expected: all pass.

- [ ] ] **Step 2: Build a release binary and smoke-test the TUI**

```bash
make build
./moomux
```

Verify:
- Existing Claude sessions still show correct status (Working / Waiting / Parked)
- Adding a project with `agent = "opencode"` in `~/.config/moomux/config.toml` and creating a session launches `opencode --port 4096` in the tmux pane
- Adding a project with `agent = "codex"` launches `codex` in the pane
- The detail panel shows the `agent:` row for selected sessions

Example config to test with:
```toml
[projects.test-opencode]
repo        = "~/Development/some-project"
base_branch = "main"
agent       = "opencode"

[projects.test-codex]
repo        = "~/Development/some-project"
base_branch = "main"
agent       = "codex"
```

- [ ] **Step 3: Update `README.md` — Requirements section**

In the **Requirements** section of `README.md`, expand the agent line:

```markdown
- `claude` CLI on `$PATH` (or `codex` / `opencode`, depending on your project config)
```

In the **Configure** section, add a note about the `agent` field:

```markdown
```toml
[projects.project1]
repo          = "~/Development/project1"
branch_prefix = "erick"
base_branch   = "main"
agent         = "claude"     # optional — "claude" (default), "codex", or "opencode"
```
```

- [ ] **Step 4: Update `README.md` — Out of scope section**

Remove `Other agents different than Claude` from the Out of scope list. Add instead:

```markdown
- Gemini CLI agent support (planned)
- Per-session agent switching (agent is set per-project)
```

- [ ] **Step 5: Commit the README changes**

```bash
git add README.md
git commit -m "docs: document multi-agent support in README"
```

- [ ] **Step 6: Push and open PR**

```bash
git push -u origin multi-agennt
gh pr create \
  --title "feat: add Codex and OpenCode agent support" \
  --base main \
  --body "$(cat <<'EOF'
## Summary

- Each project can now declare `agent = "claude" | "codex" | "opencode"` in config.toml
- Sessions store `Agent` and `AgentPort` fields; empty Agent defaults to `"claude"` (backward-compatible)
- Watcher refactored: `DirWatcher` for file-based agents (Claude + Codex), `OpenCodeWatcher` for HTTP polling, `MultiWatcher` as the root
- OpenCode sessions each get a unique port starting at 4096; the watcher polls `localhost:{port}/session/status`
- Detail panel now shows which agent is running in each session

## Test plan

- [ ] `go test ./... -race` passes clean
- [ ] Create a Claude project — sessions behave as before (no regression)
- [ ] Create a Codex project — `codex` binary launches in tmux pane; status polling works once `~/.codex/sessions/` is populated
- [ ] Create an OpenCode project — `opencode --port 4096` launches; status polling works via HTTP API
- [ ] Two simultaneous OpenCode sessions get ports 4096 and 4097 respectively
- [ ] Detail panel shows `agent:` row

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-Review

**Spec coverage:**
- ✅ Agent field on Session and Project config
- ✅ Backward compatibility (empty Agent = "claude")
- ✅ Claude: existing DirWatcher unchanged
- ✅ Codex: DirWatcher pointed at `~/.codex/sessions/` (same JSON schema assumed — adjust `parse.go` if fields differ after testing)
- ✅ OpenCode: HTTP polling via OpenCodeWatcher with per-session port
- ✅ MultiWatcher combining all agent watchers
- ✅ TUI detail panel shows agent
- ✅ README updated
- ✅ PR created

**Known limitation documented:** Codex session JSON schema is assumed to match Claude's (`cwd`, `status`, `busy`, `state` fields). If Codex uses different field names, `internal/watcher/parse.go` will need adjustment after manual testing with a real Codex session.

**Gemini:** intentionally out of scope per user direction — process-level monitoring only, degraded UX, deferred to a later PR.
