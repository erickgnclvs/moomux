// Package antigravity holds the bits of moomux that write Antigravity's
// (`agy`) own configuration on the user's behalf.
package antigravity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/erickgnclvs/moomux/internal/atomicfile"
)

// TrustWorkspace adds dir to ~/.gemini/antigravity-cli/settings.json's
// trustedWorkspaces list, so `agy` boots straight into the session instead of
// blocking on its "do you trust this folder?" prompt.
//
// Every moomux session is a brand-new worktree path, so without this the very
// first thing a new antigravity pane does is stop and wait — which reads to a
// user as the agent demanding a re-login on every session. Same problem, same
// answer as claudehook.TrustDirectory.
//
// Unknown keys round-trip through map[string]any: the file is Antigravity's,
// and it holds the user's model choice, permissions and MCP config.
func TrustWorkspace(home, dir string) error {
	path := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")

	root := map[string]any{}
	existing, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(existing, &root); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	// Refuse to overwrite a shape we don't recognize. The plain `, _` form
	// would leave trusted nil and the append below would replace the key
	// with a one-element list, silently dropping every workspace the user
	// has already approved.
	raw, present := root["trustedWorkspaces"]
	trusted, ok := raw.([]any)
	if present && !ok {
		return fmt.Errorf("%s: trustedWorkspaces is %T, want a list", path, raw)
	}
	for _, raw := range trusted {
		if s, _ := raw.(string); s == dir {
			return nil
		}
	}
	root["trustedWorkspaces"] = append(trusted, dir)

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return atomicfile.Write(path, data, mode)
}
