// Package tmuxconf manages moomux's recommended settings block in the
// user's ~/.tmux.conf. moomux launches plain tmux sessions — mouse support,
// passthrough, scrollback size, etc. all come from the user's own tmux
// config, so without this a fresh install misses mouse clicks, pane
// scrolling, and other niceties documented in README.md.
package tmuxconf

import (
	"os"
	"path/filepath"
	"strings"
)

// Marker delimits moomux's block so AlreadyApplied can detect it and Apply
// never appends it twice.
const Marker = "# moomux: recommended tmux settings (see README.md)"

// Snippet is the block appended to ~/.tmux.conf. Keep in sync with the
// "Recommended tmux config" section in README.md.
const Snippet = `
` + Marker + `
# Essential for Claude to avoid output breaking and desktop notification issues
set -g allow-passthrough on
set -s extended-keys on
set -as terminal-features 'xterm*:extkeys'

# Enable native mouse scrolling and selection
set -g mouse on

# Increase scrollback history for Claude's massive code generations
set -g history-limit 50000

# Start windows and panes at 1 instead of 0 for easier navigation
set -g base-index 1
set -g pane-base-index 1
`

// Path returns the default tmux config location, ~/.tmux.conf.
func Path() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tmux.conf")
}

// AlreadyApplied reports whether path already contains moomux's block (or
// the user added it manually).
func AlreadyApplied(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), Marker)
}

// Apply appends Snippet to path, creating the file (and its parent
// directory) if needed. Safe to call even if the file doesn't exist yet;
// not safe to call twice — check AlreadyApplied first.
func Apply(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(Snippet)
	return err
}
