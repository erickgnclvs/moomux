package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// macTCCFolders are the macOS user directories gated behind the "Files and
// Folders" privacy permission (System Settings → Privacy & Security). Tools
// run with a cwd inside one of these, from an app that hasn't been granted
// access, fail with permission/EPERM errors even though the Unix mode bits
// look fine.
var macTCCFolders = []string{"Desktop", "Documents", "Downloads"}

// tccWarning returns a warning line if path lives inside a macOS
// TCC-protected user folder, or "" otherwise. This matters most for plain
// (non-git) projects: every session for a plain project runs directly in
// path with no worktree isolation, so a blocked folder breaks every session.
func tccWarning(path string) string {
	if runtime.GOOS != "darwin" || path == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	for _, folder := range macTCCFolders {
		guarded := filepath.Join(home, folder)
		if abs == guarded || strings.HasPrefix(abs, guarded+string(filepath.Separator)) {
			return fmt.Sprintf(
				"⚠ inside ~/%s — macOS may block brew/claude here unless your terminal has Files and Folders access (Privacy & Security settings)",
				folder,
			)
		}
	}
	return ""
}
