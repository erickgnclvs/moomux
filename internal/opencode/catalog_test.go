package opencode

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestProjectCatalog(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "project & docs")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	const shim = `#!/bin/sh
case "$1" in
--version) echo 'opencode v2.4.2';;
service) echo false;;
models) printf '%s\n' test/plain test/reason test/disabled;;
api)
  case "$3" in
    *'location%5Bdirectory%5D='*'project+%26+docs') ;;
    *) exit 2;;
  esac
  case "$3" in
    /api/agent\?*) echo '{"data":[{"id":"plan","name":"Plan","mode":"primary","model":{"providerID":"test","id":"reason"}},{"id":"review","mode":"all"},{"id":"helper","mode":"subagent"},{"id":"title","mode":"primary","hidden":true}]}';;
    /api/model/default\?*) echo '{"data":{"providerID":"test","id":"plain"}}';;
    /api/model\?*) echo '{"data":[{"providerID":"test","id":"plain","enabled":true},{"providerID":"test","id":"reason","enabled":true,"variants":[{"id":"low"},{"id":"high"},{"id":"high"}]},{"providerID":"test","id":"disabled","enabled":false}]}';;
    *) exit 3;;
  esac;;
*) exit 4;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	opts, err := Read(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(opts.Models, []string{"test/plain", "test/reason"}) || opts.DefaultModel != "test/plain" || !slices.Equal(opts.Variants["test/reason"], []string{"low", "high"}) || len(opts.Variants["test/plain"]) != 0 {
		t.Fatalf("model choices = %+v", opts)
	}
	if len(opts.Agents) != 2 || opts.Agents[0].ID != "plan" || opts.Agents[0].Model != "test/reason" || opts.Agents[1].ID != "review" {
		t.Fatalf("agents = %+v", opts.Agents)
	}
}
