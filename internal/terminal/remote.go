package terminal

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// remoteOpener asks the already-running terminal instance (over its
// remote-control socket) to open the tmux session as a new tab or window,
// instead of spawning a fresh terminal process. The remote call runs
// synchronously so a failure — remote control disabled, stale socket,
// missing helper binary — can fall back to launching a new terminal.
type remoteOpener struct {
	binary   string
	args     argBuilder
	fallback TerminalOpener
	run      func(binary string, args ...string) error
}

func (r *remoteOpener) OpenSession(tmuxSession, title string) (string, error) {
	run := r.run
	if run == nil {
		run = runCombined
	}
	if err := run(r.binary, r.args(title, tmuxSession)...); err != nil {
		slog.Debug("remote terminal open failed, falling back", "binary", r.binary, "err", err)
		return r.fallback.OpenSession(tmuxSession, title)
	}
	return "", nil
}

func runCombined(binary string, args ...string) error {
	out, err := exec.Command(binary, args...).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
	}
	return err
}
