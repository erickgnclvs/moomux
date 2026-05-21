# Mosaic View Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `m` keybinding to the moomux TUI that tiles all live agent sessions as panes in a new tmux window called `moomux-mosaic`.

**Architecture:** A new `internal/mosaic` package holds the layout logic. Eight thin methods are added to `tmux.Client` (using the existing injectable `Runner` interface). `App.OpenMosaic` guards on `$TMUX` and live sessions, then delegates to the mosaic client. The TUI wires a single `m` keybinding that fires `OpenMosaic` as a `tea.Cmd`.

**Tech Stack:** Go 1.24, charmbracelet/bubbletea, tmux CLI (already required by the project)

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| MODIFY | `internal/tmux/tmux.go` | 8 new window/pane methods |
| MODIFY | `internal/tmux/tmux_test.go` | Tests for the 8 new methods |
| CREATE | `internal/mosaic/mosaic.go` | Mosaic layout orchestration |
| CREATE | `internal/mosaic/mosaic_test.go` | Tests for mosaic orchestration |
| MODIFY | `internal/app/app.go` | `OpenMosaic()` method |
| MODIFY | `internal/tui/model.go` | Add `OpenMosaic()` to `Backend` interface |
| MODIFY | `internal/tui/keys.go` | Add `Mosaic` keybinding |
| MODIFY | `internal/tui/messages.go` | Add `MosaicOpenedMsg` |
| MODIFY | `internal/tui/update.go` | Handle `m` key + `MosaicOpenedMsg` |
| MODIFY | `internal/tui/view.go` | Add `m:mosaic` to footer hint |

---

## Task 1: Add tmux window/pane methods

**Files:**
- Modify: `internal/tmux/tmux.go`
- Modify: `internal/tmux/tmux_test.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/tmux/tmux_test.go` (after `TestKillSession`):

```go
func TestKillWindow(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.KillWindow("moomux-mosaic"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"kill-window", "-t", "moomux-mosaic"}) {
		t.Fatalf("got %v", got)
	}
}

func TestNewWindow(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.NewWindow("moomux-mosaic"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"new-window", "-d", "-n", "moomux-mosaic"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSplitWindow(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SplitWindow("moomux-mosaic"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"split-window", "-t", "moomux-mosaic"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSendKeys(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SendKeys("moomux-mosaic", "tmux attach -t moomux-foo"); err != nil {
		t.Fatal(err)
	}
	want := []string{"send-keys", "-t", "moomux-mosaic", "tmux attach -t moomux-foo", "Enter"}
	if got := fr.calls[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestSelectLayout(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SelectLayout("moomux-mosaic", "tiled"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"select-layout", "-t", "moomux-mosaic", "tiled"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSetPaneBorderStatus(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SetPaneBorderStatus("moomux-mosaic", "top"); err != nil {
		t.Fatal(err)
	}
	want := []string{"set-option", "-t", "moomux-mosaic", "pane-border-status", "top"}
	if got := fr.calls[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestSelectPane(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SelectPane("moomux-mosaic.0", "auth-refactor"); err != nil {
		t.Fatal(err)
	}
	want := []string{"select-pane", "-t", "moomux-mosaic.0", "-T", "auth-refactor"}
	if got := fr.calls[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestSelectWindow(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SelectWindow("moomux-mosaic"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"select-window", "-t", "moomux-mosaic"}) {
		t.Fatalf("got %v", got)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/tmux/... -run 'TestKillWindow|TestNewWindow|TestSplitWindow|TestSendKeys|TestSelectLayout|TestSetPaneBorderStatus|TestSelectPane|TestSelectWindow' -v
```

Expected: `undefined: (*Client).KillWindow` (or similar compile error)

- [ ] **Step 3: Implement the 8 methods**

Append to `internal/tmux/tmux.go` (after `KillSession`):

```go
func (c *Client) KillWindow(name string) error {
	_, err := c.Runner.Run("kill-window", "-t", name)
	return err
}

func (c *Client) NewWindow(name string) error {
	_, err := c.Runner.Run("new-window", "-d", "-n", name)
	return err
}

func (c *Client) SplitWindow(target string) error {
	_, err := c.Runner.Run("split-window", "-t", target)
	return err
}

func (c *Client) SendKeys(target, cmd string) error {
	_, err := c.Runner.Run("send-keys", "-t", target, cmd, "Enter")
	return err
}

func (c *Client) SelectLayout(target, layout string) error {
	_, err := c.Runner.Run("select-layout", "-t", target, layout)
	return err
}

func (c *Client) SetPaneBorderStatus(target, val string) error {
	_, err := c.Runner.Run("set-option", "-t", target, "pane-border-status", val)
	return err
}

func (c *Client) SelectPane(target, title string) error {
	_, err := c.Runner.Run("select-pane", "-t", target, "-T", title)
	return err
}

func (c *Client) SelectWindow(name string) error {
	_, err := c.Runner.Run("select-window", "-t", name)
	return err
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/tmux/... -v
```

Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tmux/tmux.go internal/tmux/tmux_test.go
git commit -m "feat(tmux): add window and pane management methods"
```

---

## Task 2: Create mosaic package

**Files:**
- Create: `internal/mosaic/mosaic.go`
- Create: `internal/mosaic/mosaic_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/mosaic/mosaic_test.go`:

```go
package mosaic

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tmux"
)

type fakeRunner struct {
	calls  [][]string
	failOn map[string]bool
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.failOn[key] {
		return "", errors.New("injected failure")
	}
	return "", nil
}

func makeSessions(names ...string) []session.Session {
	out := make([]session.Session, len(names))
	for i, n := range names {
		out[i] = session.Session{Name: n, TmuxSession: "moomux-" + n}
	}
	return out
}

func assertContains(t *testing.T, calls [][]string, want []string) {
	t.Helper()
	for _, call := range calls {
		if reflect.DeepEqual(call, want) {
			return
		}
	}
	t.Fatalf("expected call %v not found in %v", want, calls)
}

func TestOpenEmpty(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Tmux: &tmux.Client{Runner: fr}}
	if err := c.Open(nil); err == nil {
		t.Fatal("expected error for empty sessions")
	}
}

func TestOpenSingleSession(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Tmux: &tmux.Client{Runner: fr}}

	if err := c.Open(makeSessions("auth")); err != nil {
		t.Fatal(err)
	}

	assertContains(t, fr.calls, []string{"kill-window", "-t", "moomux-mosaic"})
	assertContains(t, fr.calls, []string{"new-window", "-d", "-n", "moomux-mosaic"})
	assertContains(t, fr.calls, []string{"send-keys", "-t", "moomux-mosaic", "tmux attach -t moomux-auth", "Enter"})
	assertContains(t, fr.calls, []string{"select-layout", "-t", "moomux-mosaic", "tiled"})
	assertContains(t, fr.calls, []string{"select-window", "-t", "moomux-mosaic"})
}

func TestOpenTwoSessions(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Tmux: &tmux.Client{Runner: fr}}

	if err := c.Open(makeSessions("auth", "payment")); err != nil {
		t.Fatal(err)
	}

	assertContains(t, fr.calls, []string{"send-keys", "-t", "moomux-mosaic", "tmux attach -t moomux-auth", "Enter"})
	assertContains(t, fr.calls, []string{"split-window", "-t", "moomux-mosaic"})
	assertContains(t, fr.calls, []string{"send-keys", "-t", "moomux-mosaic", "tmux attach -t moomux-payment", "Enter"})
}

func TestOpenThreeSessions_PaneTitles(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Tmux: &tmux.Client{Runner: fr}}

	if err := c.Open(makeSessions("auth", "payment", "ui")); err != nil {
		t.Fatal(err)
	}

	assertContains(t, fr.calls, []string{"select-pane", "-t", "moomux-mosaic.0", "-T", "auth"})
	assertContains(t, fr.calls, []string{"select-pane", "-t", "moomux-mosaic.1", "-T", "payment"})
	assertContains(t, fr.calls, []string{"select-pane", "-t", "moomux-mosaic.2", "-T", "ui"})
}

func TestOpenNewWindowFails(t *testing.T) {
	fr := &fakeRunner{failOn: map[string]bool{"new-window -d -n moomux-mosaic": true}}
	c := &Client{Tmux: &tmux.Client{Runner: fr}}

	if err := c.Open(makeSessions("auth")); err == nil {
		t.Fatal("expected error when new-window fails")
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/mosaic/... -v
```

Expected: compile error — package `mosaic` does not exist yet

- [ ] **Step 3: Implement the mosaic package**

Create `internal/mosaic/mosaic.go`:

```go
package mosaic

import (
	"fmt"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tmux"
)

const windowName = "moomux-mosaic"

type Client struct {
	Tmux *tmux.Client
}

func (c *Client) Open(sessions []session.Session) error {
	if len(sessions) == 0 {
		return fmt.Errorf("no sessions to tile")
	}

	_ = c.Tmux.KillWindow(windowName)

	if err := c.Tmux.NewWindow(windowName); err != nil {
		return fmt.Errorf("new-window: %w", err)
	}

	if err := c.Tmux.SendKeys(windowName, "tmux attach -t "+sessions[0].TmuxSession); err != nil {
		return fmt.Errorf("send-keys pane 0: %w", err)
	}

	for _, s := range sessions[1:] {
		if err := c.Tmux.SplitWindow(windowName); err != nil {
			return fmt.Errorf("split-window for %s: %w", s.Name, err)
		}
		if err := c.Tmux.SendKeys(windowName, "tmux attach -t "+s.TmuxSession); err != nil {
			return fmt.Errorf("send-keys for %s: %w", s.Name, err)
		}
	}

	if err := c.Tmux.SelectLayout(windowName, "tiled"); err != nil {
		return fmt.Errorf("select-layout: %w", err)
	}

	_ = c.Tmux.SetPaneBorderStatus(windowName, "top")

	for i, s := range sessions {
		_ = c.Tmux.SelectPane(fmt.Sprintf("%s.%d", windowName, i), s.Name)
	}

	return c.Tmux.SelectWindow(windowName)
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/mosaic/... -v
```

Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mosaic/
git commit -m "feat(mosaic): add mosaic layout orchestration package"
```

---

## Task 3: Add OpenMosaic to App

**Files:**
- Modify: `internal/app/app.go`

- [ ] **Step 1: Add `OpenMosaic` to `app.go`**

Add this import to the existing import block in `internal/app/app.go` (`os` is already present — just add the mosaic line):

```go
"github.com/erickgnclvs/moomux/internal/mosaic"
```

Append this method to `internal/app/app.go` (after `DeleteSession`):

```go
func (a *App) OpenMosaic() error {
	if os.Getenv("TMUX") == "" {
		return fmt.Errorf("mosaic requires running inside a tmux session")
	}
	var live []session.Session
	for _, s := range a.Store.All() {
		if ok, _ := a.Tmux.HasSession(s.TmuxSession); ok {
			live = append(live, s)
		}
	}
	if len(live) == 0 {
		return fmt.Errorf("no live sessions to tile")
	}
	mc := mosaic.Client{Tmux: a.Tmux}
	return mc.Open(live)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/app/...
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/app/app.go
git commit -m "feat(app): add OpenMosaic method"
```

---

## Task 4: Wire TUI

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/keys.go`
- Modify: `internal/tui/messages.go`
- Modify: `internal/tui/update.go`
- Modify: `internal/tui/view.go`

- [ ] **Step 1: Add `OpenMosaic` to the Backend interface**

In `internal/tui/model.go`, find the `Backend` interface and add one line:

```go
// Before (existing interface, abbreviated):
type Backend interface {
    CreateSession(project, name string) (session.Session, error)
    OpenSession(id string) error
    DeleteSession(id string) error
    KillTmux(id string) error
    TmuxAlive(id string) bool
    Sessions() []session.Session
    Projects() []string
    AddProject(name string, p config.Project) error
    InitProjectAndAdd(name string, p config.Project) error
    AddPlainProject(name string, p config.Project) error
    RemoveProject(name string) error
}

// After — add one line at the end of the interface:
type Backend interface {
    CreateSession(project, name string) (session.Session, error)
    OpenSession(id string) error
    DeleteSession(id string) error
    KillTmux(id string) error
    TmuxAlive(id string) bool
    Sessions() []session.Session
    Projects() []string
    AddProject(name string, p config.Project) error
    InitProjectAndAdd(name string, p config.Project) error
    AddPlainProject(name string, p config.Project) error
    RemoveProject(name string) error
    OpenMosaic() error
}
```

- [ ] **Step 2: Add `Mosaic` to KeyMap**

In `internal/tui/keys.go`, add `Mosaic` to the struct and `DefaultKeyMap`:

```go
// Struct — add after DelProject:
type KeyMap struct {
    Up         key.Binding
    Down       key.Binding
    Open       key.Binding
    New        key.Binding
    Delete     key.Binding
    Kill       key.Binding
    Refresh    key.Binding
    Tab        key.Binding
    Quit       key.Binding
    Cancel     key.Binding
    Confirm    key.Binding
    NewProject key.Binding
    DelProject key.Binding
    Mosaic     key.Binding
}

// DefaultKeyMap — add after DelProject:
Mosaic: key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mosaic")),
```

- [ ] **Step 3: Add MosaicOpenedMsg**

In `internal/tui/messages.go`, append:

```go
type MosaicOpenedMsg struct{}
```

- [ ] **Step 4: Wire the handler in update.go**

In `internal/tui/update.go`, in `updateList` add this case (after the `case key.Matches(msg, m.keys.Open):` block, before the closing `}`):

```go
case key.Matches(msg, m.keys.Mosaic):
    return m, func() tea.Msg {
        if err := m.backend.OpenMosaic(); err != nil {
            return ErrorMsg{Err: err}
        }
        return MosaicOpenedMsg{}
    }
```

Also in `Update`, add a handler for `MosaicOpenedMsg` (after the `SessionOpenedMsg` case):

```go
case MosaicOpenedMsg:
    m.flash = "mosaic open — Ctrl+b p to return"
    m.flashTime = time.Now()
    return m, nil
```

- [ ] **Step 5: Update footer hint**

In `internal/tui/view.go`, find this line in `renderFooter`:

```go
left := "n:new  enter:open  x:kill  d:delete  tab:switch  r:refresh  q:quit"
```

Change it to:

```go
left := "n:new  enter:open  x:kill  d:delete  m:mosaic  tab:switch  r:refresh  q:quit"
```

- [ ] **Step 6: Verify everything compiles and all tests pass**

```bash
go build ./... && go test ./...
```

Expected: build succeeds, all tests PASS

- [ ] **Step 7: Commit**

```bash
git add internal/tui/model.go internal/tui/keys.go internal/tui/messages.go internal/tui/update.go internal/tui/view.go
git commit -m "feat(tui): wire m keybinding to mosaic view"
```

---

## Smoke Test Checklist (manual)

After all tasks are done, verify end-to-end:

- [ ] Run `make build` — binary builds without errors
- [ ] Launch moomux inside a tmux session: `tmux new-session -s test \; send-keys "moomux" Enter`
- [ ] Create 2+ sessions, press `m` — a `moomux-mosaic` window appears with tiled panes
- [ ] Each pane shows its session name in the pane border at the top
- [ ] Each pane is interactive (can type into Claude)
- [ ] `Ctrl+b p` returns to the moomux TUI window
- [ ] Press `m` again after creating a new session — mosaic rebuilds with the new pane
- [ ] Launch moomux outside tmux (regular terminal), press `m` — flash error "mosaic requires running inside a tmux session"
- [ ] With all sessions parked (no live tmux), press `m` — flash error "no live sessions to tile"
