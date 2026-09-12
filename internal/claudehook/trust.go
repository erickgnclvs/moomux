package claudehook

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// TrustDirectory pre-approves dir in Claude Code's own trust store
// (~/.claude.json) so `claude` doesn't stop at its "Do you trust the files
// in this folder?" dialog on first launch. Every moomux session runs in a
// brand-new worktree directory Claude has never seen, so without this the
// dialog blocks the agent's real input on every session — including
// anything typed in programmatically (see App.StartFirstPrompt), since
// there's nobody there to click through it.
//
// The per-project trust state Claude Code's own trust checks read lives
// nested under a top-level "projects" object (root.projects[dir]), not at
// root[dir] directly — the latter looks plausible but Claude Code never
// reads it, so the dialog would still show despite this having "run
// successfully". Unlike EnsureHooksInstalled's settings.json, this only
// ever touches the entry for dir — every other project's trust state is
// left untouched.
func TrustDirectory(home, dir string) error {
	path := filepath.Join(home, ".claude.json")

	root := map[string]any{}
	existing, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(existing, &root); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	projects, _ := root["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	entry, _ := projects[dir].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	// Already trusted: return without touching the file. This runs on every
	// session open, not just at create, and ~/.claude.json is rewritten
	// continuously by any live Claude Code process — so a read-modify-write
	// here would hand back a snapshot taken moments earlier and drop
	// whatever Claude wrote in between (see install_concurrent_test.go on
	// why installers stay off the frequent path).
	if entry["hasTrustDialogAccepted"] == true &&
		entry["hasCompletedProjectOnboarding"] == true &&
		entry["hasClaudeMdExternalIncludesApproved"] == true {
		return nil
	}
	entry["hasTrustDialogAccepted"] = true
	entry["hasCompletedProjectOnboarding"] = true
	entry["hasClaudeMdExternalIncludesApproved"] = true
	projects[dir] = entry
	root["projects"] = projects

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}
