package claudehook

import (
	"os"
	"path/filepath"
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
// claudehook's hooks, a custom command only ever runs when the user
// explicitly types it, so Claude Code requires none.
func ensureCommand(home, name, body string) (changed bool, err error) {
	path := filepath.Join(home, ".claude", "commands", name+".md")

	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == body {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
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

// tagCommand is installed as the body of the /tag custom command — see
// EnsureTagCommand.
const tagCommand = `---
description: Tag this moomux session with its PR (and ticket, if one is already tracked)
allowed-tools: Bash(gh pr view:*), Bash(git log:*), Bash(git branch:*), Bash(moomux tag:*)
---

Run ` + "`moomux tag`" + ` with no flags first to see what's already tracked on this
session.

Find the open pull request for the current branch (e.g. ` + "`gh pr view --json url,body --jq '.url + \"\\n\" + .body'`" + `) and run:

    moomux tag -pr <that PR URL>

Leave out ` + "`-ticket`" + ` — moomux keeps this session's existing ticket automatically
when you don't pass one. If ` + "`moomux tag`" + ` showed no ticket tracked yet, look for
a ticket link in the PR title/body, the branch name, and recent commit
messages (` + "`git log --oneline -20`" + `). Recognize common
formats: Asana (` + "`https://app.asana.com/.../task/...`" + `), Jira
(` + "`https://<org>.atlassian.net/browse/<KEY>-<num>`" + ` or a bare ` + "`<KEY>-<num>`" + `
you can expand to that URL), and Linear (` + "`https://linear.app/<org>/issue/<KEY>-<num>`" + `).
If you find one, pass it too:
` + "`moomux tag -pr <PR URL> -ticket <ticket URL>`" + `.

If there's no open PR yet, say so instead of guessing one. Don't guess a
ticket link either — only pass ` + "`-ticket`" + ` when you actually found one.
`

// EnsureTagCommand installs the /tag custom command — see ensureCommand.
func EnsureTagCommand(home string) (changed bool, err error) {
	return ensureCommand(home, "tag", tagCommand)
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
