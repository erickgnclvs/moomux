package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/erickgnclvs/moomux/internal/atomicfile"
)

// Client is the settings a *front end* owns rather than the core: things
// that describe the machine a person is sitting at, not the sessions being
// orchestrated. It lives in its own file so a TUI attached to a remote
// `moomux serve` configures itself, not the server — Config is the core's
// and is served over the socket, which is the wrong side for these.
type Client struct {
	// DiffTool is the command the "D" shortcut runs on a session's
	// worktree — e.g. "diffier". The worktree path is appended as the last
	// argument and the command is started detached, so it has to open its
	// own window. Empty means no tool configured.
	DiffTool string `toml:"diff_tool,omitempty"`
}

// ClientPath is the front end's own config file, alongside the core's.
func ClientPath() string { return filepath.Join(filepath.Dir(DefaultPath()), "client.toml") }

// LoadClient reads path, returning zero settings if it doesn't exist yet.
func LoadClient(path string) (Client, error) {
	var c Client
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	return c, toml.Unmarshal(data, &c)
}

// SaveClient writes c to path, creating its directory if needed.
func SaveClient(path string, c Client) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, buf.Bytes(), 0o644)
}
