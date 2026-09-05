package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/erickgnclvs/moomux/internal/claudehook"
	"github.com/erickgnclvs/moomux/internal/config"
)

// TestInstallKnownCommandsConcurrent pins the two properties that let
// several moomux processes share one $HOME with no lock and no
// already-initialized guard: the startup sweep is idempotent, and it stays
// out of settings.json.
//
// That matters because InstallKnownCommands runs from newApp, so it fires on
// every `moomux serve`, every interactive TUI, and every agent-driven
// `moomux spawn` — potentially several at once. Commands are constant
// content written whole, so concurrent installers converge; settings.json is
// a read-modify-write of the user's own global Claude config, where a
// concurrent writer genuinely could lose an update. Keeping the hook
// installers out of the sweep is what makes the sweep safe to run anywhere.
//
// Goroutines stand in for processes, which is the weaker test — same address
// space, same page cache. It still fails if either property is dropped.
func TestInstallKnownCommandsConcurrent(t *testing.T) {
	// Both agents referenced, so every installer in agentInstallers runs.
	// newTestApp sandboxes HOME itself; read it back rather than setting our
	// own, which it would overwrite.
	a, _, _, _ := newTestApp(t, map[string]config.Project{
		"claude-proj": {Kind: "plain", Agent: "claude"},
		"codex-proj":  {Kind: "plain", Agent: "codex"},
	})
	home := os.Getenv("HOME")

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.InstallKnownCommands()
		}()
	}
	wg.Wait()

	cmdDir := filepath.Join(home, ".claude", "commands")
	for _, name := range []string{"kill", "tag", "spawn", "reseed"} {
		got, err := os.ReadFile(filepath.Join(cmdDir, name+".md"))
		if err != nil {
			t.Fatalf("read %s.md: %v", name, err)
		}
		if !strings.HasPrefix(string(got), "---\n") {
			t.Fatalf("%s.md is truncated or interleaved: %q", name, string(got))
		}
	}

	// A serial install after the storm must be a no-op. If any installer
	// reports a change here it isn't idempotent, which means every concurrent
	// caller above was rewriting the file rather than skipping it — and two
	// moomux processes could then write it at the same instant forever.
	changed, err := claudehook.EnsureKillCommand(home)
	if err != nil {
		t.Fatalf("EnsureKillCommand: %v", err)
	}
	if changed {
		t.Error("EnsureKillCommand reported a change after concurrent installs; installer is not idempotent")
	}

	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("InstallKnownCommands wrote settings.json (err=%v); it must skip the hook installers", err)
	}
}

// TestConcurrentCfgAccess covers App.Cfg being read and written from
// different goroutines: locally, CreateSession and WorktreeStatus read
// Cfg.Projects from tea.Cmd goroutines while AddProject writes it from the
// Update loop, and serving App over a socket (one goroutine per connection)
// makes that overlap routine rather than narrow. Without cfgMu this is a
// fatal "concurrent map read and map write", not just a detected race.
// Run under -race.
func TestConcurrentCfgAccess(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{"seed": {Kind: "plain", Repo: t.TempDir()}})

	var wg sync.WaitGroup
	for i := range 8 {
		repo := t.TempDir()
		wg.Add(4)
		go func() { defer wg.Done(); _ = a.AddPlainProject(fmt.Sprintf("p%d", i), config.Project{Repo: repo}) }()
		go func() { defer wg.Done(); a.Projects() }()
		go func() { defer wg.Done(); a.ConfigSnapshot() }()
		go func() { defer wg.Done(); a.Sessions() }()
	}
	wg.Wait()

	// Every add must have survived: the mutators hold the write lock across
	// validate+save, so two concurrent adds can't both read the same
	// pre-write state and have one silently overwrite the other.
	got := a.ConfigSnapshot()
	for i := range 8 {
		if _, ok := got.Projects[fmt.Sprintf("p%d", i)]; !ok {
			t.Errorf("project p%d lost to a concurrent add; %d of 9 present", i, len(got.Projects))
		}
	}
}

// TestConfigSnapshotIsACopy pins that callers outside App can't reach into
// its state — the ipc server hands this straight to clients.
func TestConfigSnapshotIsACopy(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{"seed": {Kind: "plain"}})

	snap := a.ConfigSnapshot()
	snap.Projects["injected"] = config.Project{Repo: "/evil"}
	snap.Theme = "tampered"

	if _, ok := a.Cfg.Projects["injected"]; ok {
		t.Error("mutating the snapshot's Projects map reached App.Cfg")
	}
	if a.Cfg.Theme == "tampered" {
		t.Error("mutating the snapshot's fields reached App.Cfg")
	}
}
