package terminal

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

// herdrClient opens sessions as tabs in herdr, a terminal workspace manager
// that runs inside some other emulator and replaces its env (TERM_PROGRAM=herdr,
// no WT_SESSION etc.), so without this every open fell through to the
// attach hint and opened nothing.
//
// `herdr tab create` takes no command, so opening is two calls: create the
// tab, then `herdr pane run` the attach line in its root pane.
type herdrClient struct {
	run       func(args ...string) (string, error)
	workspace string
	fallback  TerminalOpener
}

func newHerdrClient(fallback TerminalOpener) *herdrClient {
	return &herdrClient{run: herdrRun, workspace: os.Getenv("HERDR_WORKSPACE_ID"), fallback: fallback}
}

func herdrRun(args ...string) (string, error) {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	out, err := exec.Command(bin, args...).Output()
	return string(out), err
}

func (c *herdrClient) OpenSession(tmuxSession, title string) (string, error) {
	args := []string{"tab", "create", "--focus"}
	if c.workspace != "" {
		args = append(args, "--workspace", c.workspace)
	}
	if title != "" {
		args = append(args, "--label", title)
	}
	out, err := c.run(args...)
	if err != nil {
		slog.Debug("herdr: tab create failed, falling back", "err", err)
		return c.fallback.OpenSession(tmuxSession, title)
	}
	var resp struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil || resp.Result.RootPane.PaneID == "" {
		slog.Debug("herdr: could not read pane id from tab create", "out", out, "err", err)
		return c.fallback.OpenSession(tmuxSession, title)
	}
	// The line is typed into the pane's shell, so the "=" target is quoted:
	// zsh's EQUALS expansion would read a bare leading "=" as a path lookup.
	line := fmt.Sprintf("tmux attach -t %s", shellQuote("="+tmuxSession))
	if _, err := c.run("pane", "run", resp.Result.RootPane.PaneID, line); err != nil {
		slog.Debug("herdr: pane run failed, falling back", "err", err)
		return c.fallback.OpenSession(tmuxSession, title)
	}
	return "", nil
}
