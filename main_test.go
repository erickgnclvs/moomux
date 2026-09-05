package main

import (
	"bytes"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/erickgnclvs/moomux/internal/app"
	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tmux"
)

// explicitFlagOverride must tell an unset -dangerous apart from an
// explicitly passed one — a plain flag.Bool default can't, and runSpawn used
// to force every spawned session non-dangerous regardless of the project's
// configured default before this distinction existed.
func TestExplicitFlagOverride(t *testing.T) {
	newFlagSet := func(args []string) (*flag.FlagSet, *bool) {
		fs := flag.NewFlagSet("spawn", flag.ContinueOnError)
		dangerous := fs.Bool("dangerous", false, "")
		if err := fs.Parse(args); err != nil {
			t.Fatalf("parse: %v", err)
		}
		return fs, dangerous
	}

	fs, dangerous := newFlagSet(nil)
	if got := explicitFlagOverride(fs, "dangerous", *dangerous); got != nil {
		t.Fatalf("unset flag: got override %v, want nil", *got)
	}

	fs, dangerous = newFlagSet([]string{"-dangerous=true"})
	if got := explicitFlagOverride(fs, "dangerous", *dangerous); got == nil || !*got {
		t.Fatalf("-dangerous=true: got %v, want pointer to true", got)
	}

	fs, dangerous = newFlagSet([]string{"-dangerous=false"})
	if got := explicitFlagOverride(fs, "dangerous", *dangerous); got == nil || *got {
		t.Fatalf("-dangerous=false: got %v, want pointer to false", got)
	}
}

// saveConfig must log a Save failure rather than silently discard it —
// otherwise cfg.TmuxSetupAsked/AutoTmuxAsked being lost would reopen the
// first-run prompts on every subsequent launch with no indication why.
func TestSaveConfigLogsFailure(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	saveConfig(filepath.Join(blocker, "config.toml"), &config.Config{})

	if !bytes.Contains(buf.Bytes(), []byte("config save failed")) {
		t.Fatalf("expected a logged failure, got: %s", buf.String())
	}
}

// stubTmuxRunner answers "display-message -p #S" with a fixed session name,
// simulating a process running inside that tmux session regardless of cwd.
type stubTmuxRunner struct{ currentSessionName string }

func (r stubTmuxRunner) Run(args ...string) (string, error) {
	return r.currentSessionName, nil
}

// TestCurrentSessionPrefersTmuxOverStaleCwd reproduces the /kill bug where a
// stray `cd` earlier in the conversation (e.g. to read a sibling worktree's
// file) left the process cwd inside session "b"'s worktree while the agent
// was still running in session "a"'s tmux session and pane. Resolving purely
// from cwd (the old behavior) would identify session "b" and park/tag it
// instead — currentSession must resolve via the actual tmux session instead.
func TestCurrentSessionPrefersTmuxOverStaleCwd(t *testing.T) {
	root := t.TempDir()
	wtA := filepath.Join(root, "a")
	wtB := filepath.Join(root, "b")
	for _, d := range []string{wtA, wtB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	store := &session.Store{Path: filepath.Join(root, "sessions.json")}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", WorktreePath: wtA, TmuxSession: "moomux-a-1111"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", WorktreePath: wtB, TmuxSession: "moomux-b-2222"}); err != nil {
		t.Fatal(err)
	}

	a := &app.App{
		Store: store,
		Tmux:  &tmux.Client{Runner: stubTmuxRunner{currentSessionName: "moomux-a-1111"}},
	}

	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Chdir(wtB) // stale cwd: left over in session a's shell from an earlier `cd` into b

	s, err := currentSession(a)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "demo:a" {
		t.Fatalf("currentSession = %+v, want session a (the tmux session we're actually in), not the stale-cwd session", s)
	}
}

// TestParkHelperCommandIsDetached reproduces the Codex /kill failure where
// closing the iTerm tab killed `moomux park` before it reached tmux. The
// helper must have its own session so it survives closing the invoking tab.
func TestParkHelperCommandIsDetached(t *testing.T) {
	for _, subcommand := range []string{"__park-detached", "__park-worker"} {
		cmd := newParkHelperCommand("/tmp/moomux", subcommand, "demo:a")
		if cmd.Path != "/tmp/moomux" {
			t.Fatalf("helper path = %q, want /tmp/moomux", cmd.Path)
		}
		wantArgs := []string{"/tmp/moomux", subcommand, "demo:a"}
		if len(cmd.Args) != len(wantArgs) {
			t.Fatalf("helper args = %q, want %q", cmd.Args, wantArgs)
		}
		for i := range wantArgs {
			if cmd.Args[i] != wantArgs[i] {
				t.Fatalf("helper args = %q, want %q", cmd.Args, wantArgs)
			}
		}
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
			t.Fatal("park helper must start in a detached process session")
		}
		// Regression: a /kill invocation's stdout is a pipe the host CLI
		// reads to capture command output, not a stable tty. Wiring the
		// detached helper's stdio to it (as runPark used to, via
		// cmd.Stdout/Stderr = os.Stdout/os.Stderr after construction) ties
		// the helper's lifetime to that pipe staying open, so the read side
		// blocking on/timing out waiting for EOF can kill the helper before
		// it reaches CloseTab — closing tmux but leaving the tab open.
		if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
			t.Fatal("park helper must not inherit the caller's stdio — leave it unset so it goes to /dev/null")
		}
	}
}

// TestOrNone covers the formatting `moomux tag` (called with neither flag
// set) relies on to report an untagged field as "none" rather than a blank,
// easy-to-miss line.
func TestOrNone(t *testing.T) {
	if got := orNone(""); got != "none" {
		t.Fatalf("orNone(%q) = %q, want %q", "", got, "none")
	}
	if got := orNone("https://example.com/x"); got != "https://example.com/x" {
		t.Fatalf("orNone did not pass through a non-empty value: %q", got)
	}
}
