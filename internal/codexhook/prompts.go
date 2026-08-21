package codexhook

import (
	"os"
	"path/filepath"
)

// killPrompt is installed as the body of the legacy /prompts:kill custom
// prompt — see EnsureKillPrompt. Codex's custom-prompt frontmatter only
// documents description/argument-hint, not Claude Code's allowed-tools, and
// has no bash-execution-during-expansion syntax either (Claude Code's bang-backtick
// `!command`) — per developers.openai.com/codex/custom-prompts — so this
// hands Codex an instruction rather than running anything directly; it
// still has to decide to actually run the command via its own shell tool,
// same as any other prompt. The wording below is deliberately direct ("run
// it now, don't ask") to make that one extra hop as reliable as it can be.
//
// OpenAI deprecated custom prompts in favor of skills. skills.go installs the
// current $kill command; this prompt remains for older Codex CLI versions.
const killPrompt = `---
description: Park this moomux session (stop tmux, close its tab, keep the worktree/branch)
---

` + killSkillInstructions

// ensurePrompt writes body to $CODEX_HOME/prompts/<name>.md (here, always
// ~/.codex/prompts/ — see EnsureHooks's doc comment on why this package
// doesn't honor CODEX_HOME elsewhere either), creating the directory if
// needed and skipping the write when the content is already there. changed
// reports whether this call wrote the file.
//
// Legacy custom prompts live in the global prompts dir so they're available
// from every worktree. Unlike hooks.json, they only ever run when the user
// explicitly types them, so Codex requires no trust/review step for them.
func ensurePrompt(home, name, body string) (changed bool, err error) {
	path := filepath.Join(home, ".codex", "prompts", name+".md")

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

// EnsureKillPrompt installs the /kill custom prompt — see ensurePrompt.
func EnsureKillPrompt(home string) (changed bool, err error) {
	return ensurePrompt(home, "kill", killPrompt)
}

// tagPrompt is installed as the body of the /tag custom prompt — see
// EnsureTagPrompt. Codex's custom-prompt frontmatter only documents
// description/argument-hint, not Claude Code's allowed-tools, so this
// carries no frontmatter Codex doesn't understand.
const tagPrompt = `---
description: Tag this moomux session with its PR (and ticket, if one is already tracked)
---

` + tagSkillInstructions

// EnsureTagPrompt installs the /tag custom prompt — see ensurePrompt.
func EnsureTagPrompt(home string) (changed bool, err error) {
	return ensurePrompt(home, "tag", tagPrompt)
}

// spawnPrompt is installed as the body of the /spawn custom prompt — see
// EnsureSpawnPrompt. No frontmatter Codex doesn't understand, same as
// tagPrompt.
const spawnPrompt = `---
description: Spawn a new moomux session (worktree + tmux + agent) for a delegated task
---

` + spawnSkillInstructions

// EnsureSpawnPrompt installs the /spawn custom prompt — see ensurePrompt.
func EnsureSpawnPrompt(home string) (changed bool, err error) {
	return ensurePrompt(home, "spawn", spawnPrompt)
}

// reseedPrompt is installed as the body of the /reseed custom prompt — see
// EnsureReseedPrompt.
const reseedPrompt = `---
description: Re-run this session's worktree-create userscripts with --force, re-syncing template files
---

` + reseedSkillInstructions

// EnsureReseedPrompt installs the /reseed custom prompt — see ensurePrompt.
func EnsureReseedPrompt(home string) (changed bool, err error) {
	return ensurePrompt(home, "reseed", reseedPrompt)
}
