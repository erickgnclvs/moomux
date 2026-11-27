package claudehook

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/erickgnclvs/moomux/internal/tagguide"
)

// killCommand is installed as the body of the /kill custom command — see
// EnsureKillCommand. Despite the name it parks rather than deletes: it
// stops the tmux session and closes its terminal tab but keeps the
// worktree/branch, same as moomux's own "x" (park) key — the description
// spells that out so it's not a surprise. Runs `moomux park` (App.KillTmux's
// CLI backend) rather than leaving the agent to decide, from ambiguous chat
// text, whether the word "kill" meant something destructive right now.
const killCommand = `---
description: Park this moomux session (stop tmux, close its tab, keep the worktree/branch)
allowed-tools: Bash(moomux park:*)
---

!` + "`moomux park`" + `
`

// ensureCommand writes body to ~/.claude/commands/<name>.md, creating the
// directory if needed and skipping the write when the content is already
// there. changed reports whether this call wrote the file.
//
// Custom commands live in the user's global commands dir so they're
// available from every worktree with no per-project trust approval — unlike
// a hook, a custom command is invoked by the user.
func ensureCommand(home, name, body string) (changed bool, err error) {
	return ensureManagedFile(filepath.Join(home, ".claude", "commands", name+".md"), body)
}

func ensureManagedFile(path, body string) (changed bool, err error) {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == body {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err == nil && !bytes.Contains(existing, []byte("moomux")) {
		// The user's own command of the same name: every file moomux
		// installs runs or mentions `moomux`, so one that doesn't isn't ours.
		return false, nil
	}
	if err := writeFileAtomic(path, []byte(body)); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureKillCommand installs the /kill custom command — see ensureCommand.
func EnsureKillCommand(home string) (changed bool, err error) {
	return ensureCommand(home, "kill", killCommand)
}

const tagCommand = `---
description: Tag this moomux session with a confirmed ticket, an open PR, or both
` + tagToolsAndBody

const tagSkill = `---
name: tag
description: ` + tagguide.Description + ` Also use when the user invokes /tag or asks to tag this session.
` + tagToolsAndBody

const tagToolsAndBody = `allowed-tools: Bash(gh pr view:*), Bash(git log:*), Bash(git branch:*), Bash(moomux tag:*)
---

` + tagguide.Instructions

// EnsureTagCommand installs the discoverable tag skill and refreshes the legacy /tag command.
func EnsureTagCommand(home string) (changed bool, err error) {
	commandPath := filepath.Join(home, ".claude", "commands", "tag.md")
	commandChanged, err := ensureManagedFile(commandPath, tagCommand)
	if err != nil {
		return false, err
	}
	if !commandChanged {
		// A same-named skill takes precedence over a custom command in Claude.
		// Don't install one over the user's own /tag command.
		existing, err := os.ReadFile(commandPath)
		if err != nil || string(existing) != tagCommand {
			return false, err
		}
	}
	skillChanged, skillErr := ensureManagedFile(filepath.Join(home, ".claude", "skills", "tag", "SKILL.md"), tagSkill)
	return commandChanged || skillChanged, skillErr
}

// spawnCommand is installed as the body of the /spawn custom command — see
// EnsureSpawnCommand. Wraps `moomux spawn`, which needs a project, a name,
// and a prompt derived from whatever task the user typed — that derivation
// is exactly what a slash command's agent, not the CLI itself, is suited
// for, so this hands it instructions rather than a fixed shell one-liner.
const spawnCommand = `---
description: Spawn a new moomux session (worktree + tmux + agent) for a delegated task
allowed-tools: Bash(moomux spawn:*), Bash(git remote:*), Bash(basename:*)
argument-hint: <free-text task description> [project]
---

Parse ` + "`$ARGUMENTS`" + ` as a free-text task description, optionally followed by a
project name at the end if it doesn't match the current repo. Treat it as
literal text — don't try to resolve ` + "`#N`" + ` or similar tokens against GitHub
issues/PRs or anything else.

**Project**: run ` + "`moomux spawn -list`" + ` and match the current repo (e.g.
` + "`basename $(git rev-parse --show-toplevel)`" + `, or ` + "`git remote get-url origin`" + `)
against a listed project name. If ` + "`$ARGUMENTS`" + ` explicitly names a different
project, use that instead. If nothing matches, ask rather than guessing.

**Task**: write a clear, self-contained prompt from ` + "`$ARGUMENTS`" + ` (the new
session's agent starts with no context beyond what you pass in ` + "`-prompt`" + `).

**Name**: derive a short kebab-case session name from the task description.

Then run:

    moomux spawn -project <project> -name <name> -prompt "<task prompt>"

This is fire-and-forget — it creates the worktree/branch, tmux session, and
agent, types the prompt in, and returns immediately. Don't wait on or try to
check the spawned session's progress.

**Coordinated work**: if the task is one of several sessions that have to
agree on a shared contract (one per repo for a cross-repo feature, say), add
` + "`-peers <other session names, comma-separated> -contract <path to the shared plan>`" + `.
moomux appends the coordination rules to the prompt itself, so don't paste
them into ` + "`-prompt`" + `. Leave both out for independent tasks.

**Folder**: if ` + "`$ARGUMENTS`" + ` asks for the session to go into a folder, add
` + "`-folder <name>`" + ` (created if it doesn't exist yet).
`

// EnsureSpawnCommand installs the /spawn custom command — see ensureCommand.
func EnsureSpawnCommand(home string) (changed bool, err error) {
	return ensureCommand(home, "spawn", spawnCommand)
}

// reseedCommand is installed as the body of the /reseed custom command —
// see EnsureReseedCommand. Runs `moomux reseed` directly rather than leaving
// the agent to improvise a re-seed, since MOOMUX_FORCE=1 overwrites
// template-managed files in this worktree.
const reseedCommand = `---
description: Re-run this session's worktree-create userscripts with --force, re-syncing template files
allowed-tools: Bash(moomux reseed:*)
---

!` + "`moomux reseed`" + `
`

// EnsureReseedCommand installs the /reseed custom command — see ensureCommand.
func EnsureReseedCommand(home string) (changed bool, err error) {
	return ensureCommand(home, "reseed", reseedCommand)
}
