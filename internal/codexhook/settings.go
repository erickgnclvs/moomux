package codexhook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/erickgnclvs/moomux/internal/atomicfile"
)

// EnsureHooks merges the PermissionRequest/PreToolUse/UserPromptSubmit
// hooks that report "needs input" into the user's global ~/.codex/hooks.json,
// preserving any hooks already configured there (e.g. from other tools).
// Safe to call more than once: it won't add a duplicate entry for a hook it
// already installed. changed reports whether this call actually wrote the
// file (new install or content change) as opposed to a no-op — callers use
// that to decide whether the user needs telling to re-run `/hooks` (Codex
// requires reviewing new or changed hook entries before they run).
//
// PermissionRequest ("about to ask for approval") covers the escalation
// case — verified live: sending Codex a command needing network approval
// fired PermissionRequest and produced a real needs-input marker. There is
// no separate hook event for "the agent asked a plain-text question"
// (Codex's request_user_input mechanism is app-server/IDE-protocol only,
// not part of the hooks.json system), and this deliberately does NOT
// install a Stop hook to work around that gap: Stop fires unconditionally
// at the end of every turn, not just ones ending in a question, and
// internal/watcher's max-merge (NeedsInput outranks Done) would make Done
// unreachable for Codex sessions — every finished turn would show
// needs-input until the next message. Tried and reverted the identical
// mistake for Claude first (ee3095b) before removing this. Left as a known
// gap rather than reintroducing that.
//
// This is global rather than per-worktree deliberately: Codex requires
// explicitly trusting a hook file via `/hooks` before it runs (its trust
// state in config.toml is keyed by the hook file's absolute path — see
// hooks.state entries there), and every moomux worktree lives at its own
// unique path. A per-worktree hooks.json would mean re-trusting on every
// single new session; installing into the one global path means trusting it
// once, ever.
//
// Unlike Claude Code's settings.json hooks, Codex's hook entries have no
// matcher field — event name alone selects which hooks fire (verified
// against a real, working ~/.codex/hooks.json).
func EnsureHooks(home string) (changed bool, err error) {
	path := filepath.Join(home, ".codex", "hooks.json")

	settings := map[string]any{}
	existing, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(existing, &settings); err != nil {
			return false, err
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	addHook(hooks, "PermissionRequest", "moomux hook codex set")
	addHook(hooks, "PreToolUse", "moomux hook codex clear")
	addHook(hooks, "UserPromptSubmit", "moomux hook codex clear")
	settings["hooks"] = hooks

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	if bytes.Equal(data, existing) {
		// This runs on every session open (see App.repairNeedsInputHooks), not
		// just once at creation — often while some other Codex session is
		// live and reading this same global file. Skipping a no-op write
		// avoids most opportunities for it to observe a half-written file.
		return false, nil
	}
	if err := writeFileAtomic(path, data); err != nil {
		return false, err
	}
	return true, nil
}

// writeFileAtomic writes data to path atomically, so a concurrent reader
// (Codex reloading its own hook config) never observes a partially-written
// file. It keeps an existing file's permission bits, so a user who locked
// hooks.json down to 0600 is not loosened; a new file is 0644. Prompts and
// skills share it, and those are meant to be readable.
func writeFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return atomicfile.Write(path, data, mode)
}

// addHook appends a {hooks:[{type:command,command}]} entry for event,
// unless one with the same command already exists.
func addHook(hooks map[string]any, event, command string) {
	list, _ := hooks[event].([]any)
	for _, raw := range list {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for _, rc := range asSlice(entry["hooks"]) {
			c, ok := rc.(map[string]any)
			if !ok {
				continue
			}
			if cmd, _ := c["command"].(string); cmd == command {
				return // already present
			}
		}
	}
	entry := map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": command}},
	}
	hooks[event] = append(list, entry)
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}
