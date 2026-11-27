package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/erickgnclvs/moomux/internal/codexhook"
	"github.com/erickgnclvs/moomux/internal/tmux"
)

type hookPayload struct {
	CWD string `json:"cwd"`
}

// runHook implements `moomux hook <agent> set|clear`, invoked by the hooks
// an agent's package (internal/codexhook) installs into the agent's config.
// The agent segment lets each backend define its own payload shape and
// marker mechanism without the others — or main.go's dispatch — needing to
// change.
func runHook(args []string) error {
	if (len(args) == 3 || len(args) == 4) && args[0] == "opencode" && args[1] == "launch-model" {
		var agent string
		if len(args) == 4 {
			agent = args[3]
		}
		return launchOpenCodeModel(args[2], agent)
	}
	if len(args) == 4 && tmux.ReportsTerminalState(args[0]) && args[1] == "terminal" {
		return tmux.ReportTerminalState(os.Stdin, args[0], args[2], args[3])
	}
	if len(args) != 2 || (args[1] != "set" && args[1] != "clear") {
		return fmt.Errorf("usage: moomux hook <agent> set|clear")
	}
	agent, action := args[0], args[1]
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	switch agent {
	case "claude":
		// moomux no longer installs Claude hooks (see claudehook.RemoveHooks),
		// but a session started before the upgrade keeps calling the ones it
		// loaded until it restarts. Succeed quietly rather than show it a hook
		// error on every tool call.
		return nil
	case "codex":
		return handleCodexHook(action, os.Stdin, home)
	default:
		return fmt.Errorf("unsupported agent %q", agent)
	}
}

func readHookPayload(stdin io.Reader) (hookPayload, error) {
	var p hookPayload
	data, err := io.ReadAll(stdin)
	if err != nil {
		return p, fmt.Errorf("read hook payload: %w", err)
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("parse hook payload: %w", err)
	}
	return p, nil
}

// handleCodexHook records or clears a Codex session's needs-input marker.
// The marker is keyed by cwd (see codexhook.markerPath), so only cwd is
// required here.
func handleCodexHook(action string, stdin io.Reader, home string) error {
	p, err := readHookPayload(stdin)
	if err != nil {
		return err
	}
	if p.CWD == "" {
		return fmt.Errorf("hook payload missing cwd")
	}
	if action == "set" {
		return codexhook.SetNeedsInput(home, p.CWD)
	}
	return codexhook.Clear(home, p.CWD)
}
