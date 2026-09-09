package tui

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/erickgnclvs/moomux/internal/session"
)

// launchDiffTool starts the configured diff tool (client.toml's diff_tool)
// on s's worktree, so a session's changes can be reviewed without attaching
// to it.
//
// Deliberately nothing to do with the core: the diff tool is a window on
// the user's own screen, so it runs wherever this front end runs, and its
// command comes from this machine's own config.Client file rather than the
// core's served config — configuring one machine and executing on another
// is the one way this could be made incoherent.
//
// ponytail: started detached and never waited on, so the tool must open its
// own window (a GUI app, or something that spawns one) — a terminal differ
// would mean suspending the TUI, which is a different feature.
func (m *Model) launchDiffTool(s session.Session) error {
	fields := strings.Fields(m.client.DiffTool)
	if len(fields) == 0 {
		return fmt.Errorf("no diff tool configured — set one in settings (s), or add diff_tool = %q to %s", "diffier", m.clientPath)
	}
	if s.WorktreePath == "" {
		return fmt.Errorf("%s has no worktree path", s.Name)
	}
	cmd := exec.Command(fields[0], append(fields[1:], s.WorktreePath)...)
	cmd.Dir = s.WorktreePath
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", fields[0], err)
	}
	go func() { _ = cmd.Wait() }() // nothing waits on the tool, but something has to reap it
	return nil
}
