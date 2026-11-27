// Package opencode reads the choices that the installed OpenCode client can launch.
package opencode

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/erickgnclvs/moomux/internal/config"
)

type cli struct {
	ctx                   context.Context
	dir, server, password string
}

func (c cli) run(args ...string) ([]byte, error) {
	if c.server != "" {
		args = append(args, "--server", c.server)
	}
	cmd := exec.CommandContext(c.ctx, "opencode", args...)
	cmd.Dir = c.dir
	if c.password != "" {
		cmd.Env = append(cmd.Environ(), "OPENCODE_PASSWORD="+c.password)
	}
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("opencode %s: %w", args[0], err)
	}
	return out, nil
}

// A disabled background service starts a fresh, cold server for each CLI call.
// Hold the same private server across warm-up and reads, just as OpenCode's TUI does.
func (c *cli) standalone() (func(), error) {
	cmd := exec.CommandContext(c.ctx, "opencode", "serve", "--stdio", "--hostname", "127.0.0.1", "--port", "0")
	cmd.Dir = c.dir
	c.password = rand.Text()
	cmd.Env = append(cmd.Environ(), "OPENCODE_PASSWORD="+c.password)
	lease, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		lease.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		lease.Close()
		return nil, err
	}
	stop := func() {
		_ = lease.Close()
		_ = cmd.Wait()
	}
	reader := bufio.NewReader(out)
	line, err := reader.ReadBytes('\n')
	var ready struct{ URL string }
	if err == nil {
		err = json.Unmarshal(line, &ready)
	}
	if err != nil || ready.URL == "" {
		stop()
		return nil, fmt.Errorf("OpenCode private server did not report its URL")
	}
	go io.Copy(io.Discard, reader)
	c.server = ready.URL
	return stop, nil
}

func IsV1(version string) bool {
	release := strings.TrimPrefix(strings.TrimSpace(version), "opencode ")
	return strings.HasPrefix(strings.TrimPrefix(release, "v"), "1.")
}

func (c cli) models() ([]string, error) {
	out, err := c.run("models")
	if err != nil {
		return nil, err
	}
	var ids []string
	for line := range strings.Lines(string(out)) {
		id := strings.TrimSpace(line)
		provider, model, ok := strings.Cut(id, "/")
		if ok && provider != "" && model != "" && !strings.ContainsFunc(id, unicode.IsControl) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

type modelRef struct {
	ID         string
	ProviderID string
}

func (r modelRef) key() string {
	if r.ID == "" || r.ProviderID == "" {
		return ""
	}
	return r.ProviderID + "/" + r.ID
}

func get[T any](c cli, path string) (T, error) {
	var reply struct{ Data T }
	out, err := c.run("api", "GET", "/api/"+path+"?"+url.Values{"location[directory]": {c.dir}}.Encode())
	if err == nil {
		err = json.Unmarshal(out, &reply)
	}
	return reply.Data, err
}

// Read waits for provider plugins via the models command before reading the same location's API.
func Read(dir string) (config.OpenCodeOptions, error) {
	var opts config.OpenCodeOptions
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return opts, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return opts, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := cli{ctx: ctx, dir: dir}
	version, err := c.run("--version")
	if err != nil {
		return opts, err
	}
	if IsV1(string(version)) {
		opts.Models, err = c.models()
		return opts, err
	}
	disabled, err := c.run("service", "get", "disabled")
	if err != nil {
		return opts, err
	}
	if strings.TrimSpace(string(disabled)) == "true" {
		stop, err := c.standalone()
		if err != nil {
			return opts, err
		}
		defer stop()
	}
	listed, err := c.models()
	if err != nil {
		return opts, err
	}
	models, err := get[[]struct {
		modelRef
		Enabled  bool
		Variants []struct{ ID string }
	}](c, "model")
	if err != nil {
		return opts, err
	}
	opts.Variants = make(map[string][]string)
	for _, m := range models {
		if !m.Enabled {
			continue
		}
		key := m.key()
		levels := make([]string, 0, len(m.Variants))
		for _, v := range m.Variants {
			if v.ID != "" && v.ID != "default" && !slices.Contains(levels, v.ID) {
				levels = append(levels, v.ID)
			}
		}
		opts.Variants[key] = levels
	}
	for _, id := range listed {
		if _, ok := opts.Variants[id]; ok {
			opts.Models = append(opts.Models, id)
		}
	}
	agents, err := get[[]struct {
		ID, Name, Mode string
		Hidden         bool
		Model          modelRef
	}](c, "agent")
	if err != nil {
		return opts, err
	}
	for _, a := range agents {
		if a.Hidden || a.Mode == "subagent" || a.ID == "" {
			continue
		}
		opts.Agents = append(opts.Agents, config.OpenCodeAgent{ID: a.ID, Name: a.Name, Model: a.Model.key()})
	}
	model, err := get[modelRef](c, "model/default")
	if err != nil {
		return opts, err
	}
	opts.DefaultModel = model.key()
	return opts, nil
}
