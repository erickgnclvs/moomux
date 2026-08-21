package codexhook

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/erickgnclvs/moomux/internal/atomicfile"
)

// AgentExecutablePath is deliberately not named moomux. RTK applies a
// command-specific proxy to executables named moomux, which prevents Codex's
// generated commands from reliably reaching the installed binary.
func AgentExecutablePath(home string) string {
	return filepath.Join(home, ".local", "share", "moomux", "bin", "agent-command")
}

// EnsureAgentExecutable installs an exact executable copy for generated
// Codex commands. It is refreshed whenever moomux starts, so command skills
// and the binary they invoke stay in sync across upgrades.
func EnsureAgentExecutable(home, source string) (bool, error) {
	sourceBody, err := os.ReadFile(source)
	if err != nil {
		return false, fmt.Errorf("read agent executable source: %w", err)
	}
	destination := AgentExecutablePath(home)
	existing, err := os.ReadFile(destination)
	if err == nil && bytes.Equal(existing, sourceBody) {
		if chmodErr := os.Chmod(destination, 0o755); chmodErr != nil {
			return false, chmodErr
		}
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err := atomicfile.Write(destination, sourceBody, 0o755); err != nil {
		return false, err
	}
	return true, nil
}
