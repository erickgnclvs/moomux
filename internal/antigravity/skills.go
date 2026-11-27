package antigravity

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/erickgnclvs/moomux/internal/atomicfile"
	"github.com/erickgnclvs/moomux/internal/tagguide"
)

// Antigravity's skills are its slash commands: a SKILL.md discovered under a
// customization root is invocable as /<name> (and can also be picked up on
// its own from the description). Workflows — the older
// ~/.gemini/config/workflows/*.md — are deprecated in favour of these, so
// moomux installs skills only.
//
// ~/.gemini/config is the global (machine-local) customization root, so these
// land in every worktree without a per-project trust/review step.
//
// The instructions are deliberately direct ("run it now, don't ask"): a skill
// hands the agent prose, not a command to execute, so it still has to decide
// to shell out.
const (
	killSkillDescription   = "Park the current moomux session, keeping its worktree and branch. Use when the user invokes /kill or asks to park this moomux session."
	tagSkillDescription    = tagguide.Description + " Also use when the user invokes /tag or asks to tag this session."
	spawnSkillDescription  = "Spawn a new moomux worktree and agent session for a delegated task. Use when the user invokes /spawn or asks to delegate work through moomux."
	reseedSkillDescription = "Re-run this moomux session's worktree-create userscripts with force enabled. Use when the user invokes /reseed or asks to re-seed its worktree."

	killSkillInstructions = `Run ` + "`moomux park`" + ` now, without asking a separate confirmation question first.

Despite the name, this parks rather than deletes: it stops the tmux session and closes its terminal tab, but keeps the worktree and branch so the session can be reopened later (the same as moomux's own ` + "`x`" + ` key). Report success only if the command succeeded; otherwise say the session was not parked and include the error.
`

	spawnSkillInstructions = `Treat the task description supplied with this invocation as literal text — don't resolve issue-like tokens such as ` + "`#123`" + ` against GitHub or anything else.

**Project**: run ` + "`moomux spawn -list`" + ` and match the current repo (e.g. ` + "`basename $(git rev-parse --show-toplevel)`" + `, or ` + "`git remote get-url origin`" + `) against a listed project name. If the task explicitly names a different project, use that instead. If nothing matches, ask rather than guessing.

**Task**: write a clear, self-contained prompt — the spawned agent starts with no context beyond what you pass in ` + "`-prompt`" + `.

**Name**: derive a short kebab-case session name from the task description.

Then run:

    moomux spawn -project <project> -name <name> -prompt "<task prompt>"

This is fire-and-forget — it creates the worktree/branch, tmux session, and agent, types the prompt in, and returns immediately. Don't wait on or try to check the spawned session's progress.

**Coordinated work**: if the task is one of several sessions that have to agree on a shared contract (one per repo for a cross-repo feature, say), add ` + "`-peers <other session names, comma-separated> -contract <path to the shared plan>`" + `. moomux appends the coordination rules to the prompt itself, so don't paste them into ` + "`-prompt`" + `. Leave both out for independent tasks.

**Folder**: if the task asks for the session to go into a folder, add ` + "`-folder <name>`" + ` (created if it doesn't exist yet).
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
	return ensureSkill(home, "tag", tagSkillDescription, tagguide.Instructions)
}

// EnsureSpawnCommand installs the /spawn skill — see ensureSkill.
func EnsureSpawnCommand(home string) (bool, error) {
	return ensureSkill(home, "spawn", spawnSkillDescription, spawnSkillInstructions)
}

// EnsureReseedCommand installs the /reseed skill — see ensureSkill.
func EnsureReseedCommand(home string) (bool, error) {
	return ensureSkill(home, "reseed", reseedSkillDescription, reseedSkillInstructions)
}
