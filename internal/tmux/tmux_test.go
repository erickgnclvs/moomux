package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls  [][]string
	out    map[string]string
	failOn map[string]bool
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.failOn[key] {
		return f.out[key], exitErr{code: 1}
	}
	return f.out[key], nil
}

type exitErr struct{ code int }

func (e exitErr) Error() string { return "exit" }
func (e exitErr) ExitCode() int { return e.code }

// TestExecRunnerRespectsRunTimeout replaces "tmux" on PATH with a fake that
// sleeps far longer than runTimeout. Without exec.CommandContext bounding
// the subprocess, execRunner.Run would block for the full sleep instead of
// giving up once the timeout elapses (e.g. an unresponsive tmux server
// hanging the whole app).
func TestExecRunnerRespectsRunTimeout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	fakeDir := t.TempDir()
	fake := filepath.Join(fakeDir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	old := runTimeout
	runTimeout = 50 * time.Millisecond
	t.Cleanup(func() { runTimeout = old })

	start := time.Now()
	if _, err := (execRunner{}).Run("list-sessions"); err == nil {
		t.Fatal("expected error once runTimeout elapsed")
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("Run took %v, want to return shortly after runTimeout", elapsed)
	}
}

// TestExecRunnerDirIsStable pins execRunner.Run's cwd to the user's home
// directory rather than letting it inherit moomux's own process cwd. A
// bare `tmux` invocation with no live server spawns one, and that server
// keeps its launch cwd for its entire lifetime as the silent fallback
// target whenever a session's own -c doesn't stick (see PaneCwd's doc
// comment) — if moomux runs from inside one of its own worktrees and that
// worktree is later removed, every future session on the same server
// would fall back into a directory that no longer exists.
func TestExecRunnerDirIsStable(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	fakeDir := t.TempDir()
	fake := filepath.Join(fakeDir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\npwd\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	wantHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}

	out, err := (execRunner{}).Run("list-sessions")
	if err != nil {
		t.Fatal(err)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(out))
	if err != nil {
		t.Fatal(err)
	}
	if got != wantHome {
		t.Fatalf("tmux ran in %q, want home %q", got, wantHome)
	}
}

func TestNewSession(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{"list-panes -t =moomux-foo: -F #{pane_id}": "%3\n"}}
	c := &Client{Runner: fr}
	if err := c.NewSession("moomux-foo", "/tmp/wt", "claude", "foo"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-foo", "-c", "/tmp/wt", "-n", "foo"},
		{"set-window-option", "-t", "=moomux-foo:", "automatic-rename", "off"},
		{"set-option", "-t", "=moomux-foo:", "set-titles", "on"},
		{"set-option", "-t", "=moomux-foo:", "set-titles-string", "#{window_name}"},
		{"set-option", "-t", "=moomux-foo:", "mouse", "on"},
		{"list-panes", "-t", "=moomux-foo:", "-F", "#{pane_id}"},
		{"split-window", "-h", "-t", "=moomux-foo:", "-c", "/tmp/wt", "-l", "33%"},
		{"select-pane", "-t", "%3"},
		{"send-keys", "-t", "%3", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestNewSessionNoWindowName(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{"list-panes -t =moomux-foo: -F #{pane_id}": "%3\n"}}
	c := &Client{Runner: fr}
	if err := c.NewSession("moomux-foo", "/tmp/wt", "claude", ""); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-foo", "-c", "/tmp/wt"},
		{"set-option", "-t", "=moomux-foo:", "mouse", "on"},
		{"list-panes", "-t", "=moomux-foo:", "-F", "#{pane_id}"},
		{"split-window", "-h", "-t", "=moomux-foo:", "-c", "/tmp/wt", "-l", "33%"},
		{"select-pane", "-t", "%3"},
		{"send-keys", "-t", "%3", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestNewSessionNoCmd(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{"list-panes -t =moomux-foo: -F #{pane_id}": "%3\n"}}
	c := &Client{Runner: fr}
	if err := c.NewSession("moomux-foo", "/tmp/wt", "", "foo"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-foo", "-c", "/tmp/wt", "-n", "foo"},
		{"set-window-option", "-t", "=moomux-foo:", "automatic-rename", "off"},
		{"set-option", "-t", "=moomux-foo:", "set-titles", "on"},
		{"set-option", "-t", "=moomux-foo:", "set-titles-string", "#{window_name}"},
		{"set-option", "-t", "=moomux-foo:", "mouse", "on"},
		{"list-panes", "-t", "=moomux-foo:", "-F", "#{pane_id}"},
		{"split-window", "-h", "-t", "=moomux-foo:", "-c", "/tmp/wt", "-l", "33%"},
		{"select-pane", "-t", "%3"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestHasSessionPresent(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	ok, err := c.HasSession("moomux-foo")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestHasSessionAbsent(t *testing.T) {
	fr := &fakeRunner{failOn: map[string]bool{"has-session -t =moomux-foo": true}}
	c := &Client{Runner: fr}
	ok, err := c.HasSession("moomux-foo")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestHasSessionReturnsConnectionErrors(t *testing.T) {
	key := "has-session -t =moomux-foo"
	fr := &fakeRunner{
		failOn: map[string]bool{key: true},
		out:    map[string]string{key: "error connecting to /tmp/tmux-501/default (Operation not permitted)\n"},
	}
	c := &Client{Runner: fr}
	ok, err := c.HasSession("moomux-foo")
	if err == nil || ok {
		t.Fatalf("ok=%v err=%v, want a connection error", ok, err)
	}
	if !strings.Contains(err.Error(), "Operation not permitted") {
		t.Fatalf("err = %v, want tmux diagnostic", err)
	}
}

func TestEnsureEnvRefreshNoopOutsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.EnsureEnvRefresh(); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("must not touch tmux outside a tmux session; calls = %v", fr.calls)
	}
}

func TestEnsureEnvRefreshAppendsWhenMissing(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1234,0")
	t.Setenv("MOSHI_CLIENT", "1")
	fr := &fakeRunner{out: map[string]string{
		"show-options -g update-environment": "update-environment[0] SSH_CONNECTION\n",
	}}
	c := &Client{Runner: fr}
	if err := c.EnsureEnvRefresh(); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"show-options", "-g", "update-environment"},
		{"set-option", "-ga", "update-environment", "MOSHI_CLIENT"},
		{"set-environment", "-g", "MOSHI_CLIENT", "1"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestEnsureEnvRefreshSkipsAppendWhenAlreadyPresent(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1234,0")
	t.Setenv("MOSHI_CLIENT", "1")
	fr := &fakeRunner{out: map[string]string{
		"show-options -g update-environment": "update-environment[0] MOSHI_CLIENT\nupdate-environment[1] SSH_CONNECTION\n",
	}}
	c := &Client{Runner: fr}
	if err := c.EnsureEnvRefresh(); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"show-options", "-g", "update-environment"},
		{"set-environment", "-g", "MOSHI_CLIENT", "1"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("must not re-append when already present; calls = %v", fr.calls)
	}
}

// TestEnsureEnvRefreshSyncsGlobalTable covers the fallback fix: even once
// update-environment lists MOSHI_CLIENT, a brand-new session/window with no
// session-local override falls back to the server's global environment
// table, which update-environment never touches — so EnsureEnvRefresh must
// push this process's own live value there directly on every run.
func TestEnsureEnvRefreshSyncsGlobalTable(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1234,0")
	t.Setenv("MOSHI_CLIENT", "1")
	fr := &fakeRunner{out: map[string]string{
		"show-options -g update-environment": "update-environment[0] MOSHI_CLIENT\n",
	}}
	c := &Client{Runner: fr}
	if err := c.EnsureEnvRefresh(); err != nil {
		t.Fatal(err)
	}
	last := fr.calls[len(fr.calls)-1]
	want := []string{"set-environment", "-g", "MOSHI_CLIENT", "1"}
	if !reflect.DeepEqual(last, want) {
		t.Fatalf("last call = %v, want %v", last, want)
	}
}

// TestEnsureEnvRefreshUnsetsGlobalWhenNotSet covers a process launched
// without MOSHI_CLIENT (e.g. attaching over plain SSH): the global table
// must be actively cleared, not left holding an old value from a prior
// Moshi connection.
func TestEnsureEnvRefreshUnsetsGlobalWhenNotSet(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1234,0")
	t.Setenv("MOSHI_CLIENT", "")
	fr := &fakeRunner{out: map[string]string{
		"show-options -g update-environment": "update-environment[0] MOSHI_CLIENT\n",
	}}
	c := &Client{Runner: fr}
	if err := c.EnsureEnvRefresh(); err != nil {
		t.Fatal(err)
	}
	last := fr.calls[len(fr.calls)-1]
	want := []string{"set-environment", "-gu", "MOSHI_CLIENT"}
	if !reflect.DeepEqual(last, want) {
		t.Fatalf("last call = %v, want %v", last, want)
	}
}

func TestPaneCwd(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{"list-panes -t =moomux-foo: -F #{pane_current_path}": "/tmp/wt\n"}}
	c := &Client{Runner: fr}
	got, err := c.PaneCwd("moomux-foo")
	if err != nil || got != "/tmp/wt" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestCurrentSessionName(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{"display-message -p #S": "moomux-foo-1234\n"}}
	c := &Client{Runner: fr}
	got, err := c.CurrentSessionName()
	if err != nil || got != "moomux-foo-1234" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestKillSession(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.KillSession("moomux-foo"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"kill-session", "-t", "=moomux-foo"}) {
		t.Fatalf("got %v", got)
	}
}

// loadBufferContentRunner wraps fakeRunner to snapshot the exact content
// staged for a load-buffer call, since PasteText removes its temp file right
// after tmux would have read it — the path itself is unpredictable and not
// what these tests care about.
type loadBufferContentRunner struct {
	*fakeRunner
	staged string
}

func (r *loadBufferContentRunner) Run(args ...string) (string, error) {
	if len(args) == 2 && args[0] == "load-buffer" {
		if data, err := os.ReadFile(args[1]); err == nil {
			r.staged = string(data)
		}
	}
	return r.fakeRunner.Run(args...)
}

// TestPasteTextLoadsThenPastesExactBuffer guards the core mechanism: text
// must be staged into tmux's paste buffer via load-buffer and delivered with
// paste-buffer, not typed via send-keys — send-keys submits text as
// individual synthetic keystrokes, so a multi-line prompt's embedded
// newlines would each arrive as their own Enter instead of one atomic block.
func TestPasteTextLoadsThenPastesExactBuffer(t *testing.T) {
	fr := &loadBufferContentRunner{fakeRunner: &fakeRunner{}}
	c := &Client{Runner: fr}
	want := "line one\nline two"
	if err := c.PasteText("moomux-foo", want); err != nil {
		t.Fatal(err)
	}
	if fr.staged != want {
		t.Fatalf("staged content = %q, want %q", fr.staged, want)
	}
	if len(fr.calls) != 2 || fr.calls[0][0] != "load-buffer" {
		t.Fatalf("calls = %v, want load-buffer then paste-buffer", fr.calls)
	}
	// "=moomux-foo:" pins paste-buffer to an exact session match; a bare name
	// falls back to prefix matching and could paste into moomux-foo-2 once
	// moomux-foo is gone.
	want2 := []string{"paste-buffer", "-d", "-t", "=moomux-foo:"}
	if !reflect.DeepEqual(fr.calls[1], want2) {
		t.Fatalf("second call = %v, want %v", fr.calls[1], want2)
	}
	if _, err := os.ReadFile(fr.calls[0][1]); err == nil {
		t.Fatalf("temp file %q was not cleaned up", fr.calls[0][1])
	}
}

// TestPasteTextPropagatesLoadBufferError guards that a failing load-buffer
// (e.g. no tmux server) is surfaced rather than proceeding to paste-buffer
// with nothing staged.
func TestPasteTextPropagatesLoadBufferError(t *testing.T) {
	r := &alwaysFailRunner{}
	c := &Client{Runner: r}
	if err := c.PasteText("moomux-foo", "hi"); err == nil {
		t.Fatal("expected error")
	}
	if r.calls != 1 {
		t.Fatalf("paste-buffer must not run after load-buffer failed: %d calls", r.calls)
	}
}

// alwaysFailRunner fails every call — used to check that PasteText's
// load-buffer failure short-circuits before it ever attempts paste-buffer.
type alwaysFailRunner struct{ calls int }

func (r *alwaysFailRunner) Run(args ...string) (string, error) {
	r.calls++
	return "", exitErr{code: 1}
}
