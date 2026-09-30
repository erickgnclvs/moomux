package ipc

import (
	"os"
	"path/filepath"
	"strings"
)

// GhosttyConfig is the GhosttyConfig method's answer: the user's Ghostty
// config files on the core's machine, so a front end with no disk of its own
// (the phone) can style its panes the way the Mac app does by reading them
// locally. Text is every file's contents in load order, each followed by a
// newline; later lines override earlier ones, the way ghostty reads them.
type GhosttyConfig struct {
	Text  string   `json:"text"`
	Files []string `json:"files,omitempty"`
}

// ghosttyConfig reads the config files in the order ghostty's own
// Config.loadDefaultFiles does, which the Mac app's
// AppState.ghosttyConfigPaths mirrors — the two must agree, since the order
// decides which setting wins. `config-file =` includes and `theme =` are
// left alone: the client resolves themes itself, and ghostty's embedded
// loader ignores includes. No config anywhere is an empty answer, not an
// error.
func ghosttyConfig() GhosttyConfig {
	home, _ := os.UserHomeDir()
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		// XDG_CONFIG_HOME replaces ~/.config; it doesn't add to it.
		xdg = filepath.Join(home, ".config")
	}
	var out GhosttyConfig
	var text strings.Builder
	for _, dir := range []string{
		filepath.Join(xdg, "ghostty"),
		filepath.Join(home, "Library", "Application Support", "com.mitchellh.ghostty"),
	} {
		for _, name := range []string{"config", "config.ghostty"} { // legacy name first
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil || len(data) == 0 {
				continue
			}
			out.Files = append(out.Files, path)
			text.Write(data)
			text.WriteByte('\n')
		}
	}
	out.Text = text.String()
	return out
}
