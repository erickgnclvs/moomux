package antigravity

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/erickgnclvs/moomux/internal/atomicfile"
)

// Antigravity's skills are its slash commands: a SKILL.md discovered under a
// customization root is invocable as /<name> (and can also be picked up on
// its own from the description). Workflows — the older
// ~/.gemini/config/workflows/*.md — are deprecated in favour of these, so
// moomux installs skills only.
//
// ~/.gemini/config is the global (machine-local) customization root, so these
// land in every worktree without a per-project step. Like claudehook's
// commands and codexhook's skills, they only ever run when the user types
// them, so there's no trust/review hop — unlike TrustWorkspace's problem.
//
// The instructions are deliberately direct ("run it now, don't ask"): a skill
// hands the agent prose, not a command to execute, so it still has to decide
// to shell out.
const (
	killSkillDescription   = "Park the current moomux session, keeping its worktree and branch. Use when the user invokes /kill or asks to park this moomux session."
	tagSkillDescription    = "Tag the current moomux session with its pull request and any tracked ticket. Use when the user invokes /tag or asks to tag this moomux session."
	spawnSkillDescription  = "Spawn a new moomux worktree and agent session for a delegated task. Use when the user invokes /spawn or asks to delegate work through moomux."
	reseedSkillDescription = "Re-run this moomux session's worktree-create userscripts with force enabled. Use when the user invokes /reseed or asks to re-seed its worktree."

	killSkillInstructions = `Run ` + "`moomux park`" + ` now, without asking a separate confirmation question first.

Despite the name, this parks rather than deletes: it stops the tmux session and closes its terminal tab, but keeps the worktree and branch so the session can be reopened later (the same as moomux's own ` + "`x`" + ` key). Report success only if the command succeeded; otherwise say the session was not parked and include the error.
`

	tagSkillInstructions = `Run ` + "`moomux tag`" + ` with no flags first, to see what is already tracked on this session.

Find the open pull request for the current branch (e.g. ` + "`gh pr view --json url,body --jq '.url + \"\\n\" + .body'`" + `) and run:

    moomux tag -pr <that PR URL>

Leave out ` + "`-ticket`" + ` — moomux keeps this session's existing ticket when you don't pass one. If ` + "`moomux tag`" + ` showed no ticket tracked yet, look for a ticket link in the pull request title and body, the branch name, and recent commit messages (` + "`git log --oneline -20`" + `). Recognize Asana (` + "`https://app.asana.com/.../task/...`" + `), Jira (` + "`https://<org>.atlassian.net/browse/<KEY>-<num>`" + `, or a bare ` + "`<KEY>-<num>`" + ` you can expand to that URL), and Linear (` + "`https://linear.app/<org>/issue/<KEY>-<num>`" + `). If you find one, pass it too:

    moomux tag -pr <PR URL> -ticket <ticket URL>

If there is no open pull request yet, say so instead of guessing one. Don't guess a ticket link either — only pass ` + "`-ticket`" + ` when you actually found one.
`

	spawnSkillInstructions = `Treat the task description supplied with this invocation as literal text — don't resolve issue-like tokens such as ` + "`#123`" + ` against GitHub or anything else.

**Project**: run ` + "`moomux spawn -list`" + ` and match the current repo (e.g. ` + "`basename $(git rev-parse --show-toplevel)`" + `, or ` + "`git remote get-url origin`" + `) against a listed project name. If the task explicitly names a different project, use that instead. If nothing matches, ask rather than guessing.

**Task**: write a clear, self-contained prompt — the spawned agent starts with no context beyond what you pass in ` + "`-prompt`" + `.

**Name**: derive a short kebab-case session name from the task description.

Then run:

    moomux spawn -project <project> -name <name> -prompt "<task prompt>"

This is fire-and-forget — it creates the worktree/branch, tmux session, and agent, types the prompt in, and returns immediately. Don't wait on or try to check the spawned session's progress.
`

	reseedSkillInstructions = `Run ` + "`moomux reseed`" + ` now, without asking a separate confirmation question first. This re-runs this session's worktree-create userscripts with ` + "`MOOMUX_FORCE=1`" + `, which overwrites template-managed files in this worktree. Then report what it printed.
`
)

// ensureSkill writes a SKILL.md to ~/.gemini/config/skills/<name>/, creating
// the directory if needed and skipping the write when the content is already
// there. changed reports whether this call wrote the file.
func ensureSkill(home, name, description, instructions string) (changed bool, err error) {
	path := filepath.Join(home, ".gemini", "config", "skills", name, "SKILL.md")
	body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", name, description, instructions)

	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == body {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, atomicfile.Write(path, []byte(body), 0o644)
}

// EnsureKillCommand installs the /kill skill — see ensureSkill.
func EnsureKillCommand(home string) (bool, error) {
	return ensureSkill(home, "kill", killSkillDescription, killSkillInstructions)
}

// EnsureTagCommand installs the /tag skill — see ensureSkill.
func EnsureTagCommand(home string) (bool, error) {
	return ensureSkill(home, "tag", tagSkillDescription, tagSkillInstructions)
}

// EnsureSpawnCommand installs the /spawn skill — see ensureSkill.
func EnsureSpawnCommand(home string) (bool, error) {
	return ensureSkill(home, "spawn", spawnSkillDescription, spawnSkillInstructions)
}

// EnsureReseedCommand installs the /reseed skill — see ensureSkill.
func EnsureReseedCommand(home string) (bool, error) {
	return ensureSkill(home, "reseed", reseedSkillDescription, reseedSkillInstructions)
}
