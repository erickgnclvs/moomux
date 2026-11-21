// Package userscript runs user-provided scripts at points in a session's
// lifecycle (currently: right after a worktree is created, and right before
// one is removed).
package userscript

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

// runTimeout bounds each script so a hung one can't stall session
// creation/deletion.
var runTimeout = 30 * time.Second

// globalDir returns the directory moomux scans for scripts that run for
// every project, for the given lifecycle event. It lives under the user's
// config dir, outside any repo moomux manages, so scripts placed here are
// never committed or pushed by the project itself.
func globalDir(home, event string) string {
	return filepath.Join(home, ".config", "moomux", "userscripts", event)
}

// projectDir returns the directory moomux scans for scripts scoped to a
// single project, for the given lifecycle event.
func projectDir(home, project, event string) string {
	return filepath.Join(home, ".config", "moomux", "userscripts", project, event)
}

// Env carries the values passed to every script as environment variables.
type Env struct {
	Project  string
	Worktree string
	Repo     string
	Branch   string
	// Force asks a worktree-create script to redo setup on a worktree it may
	// have already seeded (e.g. re-copy template files after they changed),
	// rather than skipping work it thinks is already done. Surfaced to
	// scripts as MOOMUX_FORCE=1; a script that doesn't care can ignore it.
	Force bool
}

// RunWorktreeCreate runs every executable worktree-create script (global
// scripts first, then any scoped to env.Project), with cwd set to
// env.Worktree. The returned messages include both warnings (failing or
// timed-out scripts) and any output a successful script printed, so callers
// can surface them to the user.
func RunWorktreeCreate(home string, env Env) []string {
	return run(home, "worktree-create", env)
}

// RunWorktreeDelete runs every executable worktree-delete script (global
// scripts first, then any scoped to env.Project), with cwd set to
// env.Worktree. Callers should run this before the worktree is removed, so
// scripts can still read files from it.
func RunWorktreeDelete(home string, env Env) []string {
	return run(home, "worktree-delete", env)
}

// run runs every executable file in the global and then the project-specific
// script directory for the given event, in name-sorted order within each.
// Best-effort: a failing or timed-out script is reported in the returned
// warnings rather than as an error — one broken script shouldn't stop
// session creation or deletion.
func run(home, event string, env Env) []string {
	var warnings []string
	warnings = append(warnings, runDir(globalDir(home, event), env)...)
	warnings = append(warnings, runDir(projectDir(home, env.Project, event), env)...)
	return warnings
}

func runDir(dir string, env Env) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no userscripts dir: nothing to do
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	extraEnv := []string{
		"MOOMUX_PROJECT=" + env.Project,
		"MOOMUX_WORKTREE=" + env.Worktree,
		"MOOMUX_REPO=" + env.Repo,
		"MOOMUX_BRANCH=" + env.Branch,
	}
	if env.Force {
		extraEnv = append(extraEnv, "MOOMUX_FORCE=1")
	}

	var warnings []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Mode()&0o111 == 0 {
			continue // not executable
		}
		path := filepath.Join(dir, e.Name())
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		cmd := exec.CommandContext(ctx, path)
		cmd.Dir = env.Worktree
		cmd.Env = append(os.Environ(), extraEnv...)
		// A script that leaves a background child holding stdout would
		// otherwise block CombinedOutput until the child exits.
		cmd.WaitDelay = 2 * time.Second
		out, err := cmd.CombinedOutput()
		cancel()
		switch {
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("userscript %s: %v (%s)", e.Name(), err, string(out)))
		case len(out) > 0:
			warnings = append(warnings, fmt.Sprintf("userscript %s: %s", e.Name(), string(out)))
		}
	}
	return warnings
}
