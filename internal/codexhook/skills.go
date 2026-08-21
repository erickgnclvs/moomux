package codexhook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Codex replaced custom prompts with skills for reusable user workflows.
// Install these commands in the user-wide skill directory so every moomux
// worktree can discover them. The matching legacy prompts are kept for older
// Codex CLI versions that still expose /prompts:<name>.
const (
	killSkillDescription   = "Park the current moomux session while keeping its worktree and branch. Use when the user invokes $kill or asks to park the current moomux session."
	tagSkillDescription    = "Tag the current moomux session with its pull request and any tracked ticket. Use when the user invokes $tag or asks to tag a moomux session."
	spawnSkillDescription  = "Spawn a new moomux worktree and agent session for a delegated task. Use when the user invokes $spawn or asks to delegate work through moomux."
	reseedSkillDescription = "Re-run worktree-create userscripts for the current moomux session with force enabled. Use when the user invokes $reseed or asks to re-seed its worktree."

	killSkillInstructions = `Run ` + "`moomux park`" + ` in a shell with ` + "`sandbox_permissions`" + ` set to ` + "`require_escalated`" + `. The tmux socket is outside Codex's sandbox, so a sandboxed run cannot park the session. Request approval through the shell tool without asking a separate confirmation question, and do not retry inside the sandbox if approval is denied. The command stops the tmux session and closes its terminal tab, but keeps the worktree and branch so the session can be reopened later (the same behavior as moomux's ` + "`x`" + ` key). Report success only if the command succeeds; otherwise say the session was not parked and include the error.
`

	tagSkillInstructions = `Run ` + "`moomux tag`" + ` with no flags first in a shell with ` + "`sandbox_permissions`" + ` set to ` + "`require_escalated`" + ` to see what is already tracked on this session. Use the same shell setting for the update command below.

Find the open pull request for the current branch and run:

    moomux tag -pr <that PR URL>

Leave out ` + "`-ticket`" + ` because moomux keeps the existing ticket when that flag is omitted. If no ticket is tracked yet, look for a ticket link in the pull request title or body, branch name, and recent commit messages. Recognize Asana, Jira, and Linear ticket references. If you find one, pass it too:

    moomux tag -pr <PR URL> -ticket <ticket URL>

If there is no open pull request, say so instead of guessing. Do not guess a ticket link either.
`

	spawnSkillInstructions = `Use the task description supplied with this invocation as literal text. Do not resolve issue-like tokens such as ` + "`#123`" + ` unless the user explicitly asks you to.

Run ` + "`moomux spawn -list`" + ` in a shell with ` + "`sandbox_permissions`" + ` set to ` + "`require_escalated`" + ` and match the current repository against a listed project name. If the task explicitly names a different project, use that instead. Ask the user if no project can be matched safely. Use the same shell setting for the spawn command below.

Write a clear, self-contained task prompt because the spawned agent starts with no context beyond that prompt. Derive a short kebab-case session name, then run:

    moomux spawn -project <project> -name <name> -prompt "<task prompt>"

This is fire-and-forget. Do not wait for or check the spawned session's progress.
`

	reseedSkillInstructions = `Run ` + "`moomux reseed`" + ` in a shell with ` + "`sandbox_permissions`" + ` set to ` + "`require_escalated`" + `, without asking a separate confirmation question first. This re-runs the current session's worktree-create userscripts with ` + "`MOOMUX_FORCE=1`" + `. Then report what it printed.
`
)

func skillDocument(name, description, instructions string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", name, description, instructions)
}

func ensureSkill(home, name, description, instructions string) (changed bool, err error) {
	path := filepath.Join(home, ".agents", "skills", name, "SKILL.md")
	body := skillDocument(name, description, instructions)

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

func ensureCommand(home, name, prompt, description, instructions string) (changed bool, err error) {
	promptChanged, promptErr := ensurePrompt(home, name, prompt)
	skillChanged, skillErr := ensureSkill(home, name, description, instructions)
	return promptChanged || skillChanged, errors.Join(promptErr, skillErr)
}

func EnsureKillCommand(home string) (bool, error) {
	return ensureCommand(home, "kill", killPrompt, killSkillDescription, killSkillInstructions)
}

func EnsureTagCommand(home string) (bool, error) {
	return ensureCommand(home, "tag", tagPrompt, tagSkillDescription, tagSkillInstructions)
}

func EnsureSpawnCommand(home string) (bool, error) {
	return ensureCommand(home, "spawn", spawnPrompt, spawnSkillDescription, spawnSkillInstructions)
}

func EnsureReseedCommand(home string) (bool, error) {
	return ensureCommand(home, "reseed", reseedPrompt, reseedSkillDescription, reseedSkillInstructions)
}
