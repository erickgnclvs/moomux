package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/erickgnclvs/moomux/internal/opencode"
)

// Run inside the pane so OpenCode sees the worktree and the user's shell environment.
// v2's full-screen client accepts a session ID. Its session API accepts the model.
func launchOpenCodeModel(model, agent string) error {
	bin, err := exec.LookPath("opencode")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args, err := openCodeModelArgs(ctx, bin, model, agent)
	if err != nil {
		return err
	}
	return syscall.Exec(bin, append([]string{"opencode"}, args...), os.Environ())
}

func openCodeModelArgs(ctx context.Context, bin, model, agent string) ([]string, error) {
	version, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return nil, fmt.Errorf("opencode --version: %w", err)
	}
	if opencode.IsV1(string(version)) {
		var args []string
		if model != "" && model != "default" {
			args = append(args, "--model", model)
		}
		if agent != "" {
			args = append(args, "--agent", agent)
		}
		return args, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	request := map[string]any{"location": map[string]string{"directory": cwd}}
	if agent != "" {
		request["agent"] = agent
	}
	if model != "default" {
		provider, rest, ok := strings.Cut(model, "/")
		id, variant, hasVariant := strings.Cut(rest, "#")
		if !ok || provider == "" || id == "" || strings.Contains(provider, "#") || hasVariant && (variant == "" || strings.Contains(variant, "#")) {
			return nil, fmt.Errorf("invalid OpenCode model %q: use provider/model or provider/model#variant", model)
		}
		ref := map[string]string{"id": id, "providerID": provider}
		if hasVariant {
			ref["variant"] = variant
		}
		request["model"] = ref
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "api", "POST", "/api/session", "--data", string(body))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("create OpenCode session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var reply struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return nil, fmt.Errorf("read OpenCode session: %w", err)
	}
	if reply.Data.ID == "" {
		return nil, fmt.Errorf("OpenCode returned no session ID")
	}
	return []string{"--session", reply.Data.ID}, nil
}
