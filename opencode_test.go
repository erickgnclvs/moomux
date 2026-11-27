package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOpenCodeModelArgs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	bin := filepath.Join(dir, "opencode")
	const script = `#!/bin/sh
if [ "$1" = --version ]; then echo "$OC_VERSION"; exit; fi
[ "$1" = api ] && [ "$2" = POST ] && [ "$3" = /api/session ] && [ "$4" = --data ] || exit 1
printf '%s' "$5" > request.json
printf '%s' "$OC_RESPONSE"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	const model = "provider/nested/model's name"
	for _, version := range []string{"1.18.0", "opencode v1.18.0"} {
		t.Setenv("OC_VERSION", version)
		args, err := openCodeModelArgs(t.Context(), bin, model, "")
		if err != nil || !slices.Equal(args, []string{"--model", model}) {
			t.Fatalf("%s args %v, err %v", version, args, err)
		}
	}
	t.Setenv("OC_VERSION", "opencode v2.4.2")
	t.Setenv("OC_RESPONSE", `{"data":{"id":"ses_fixture"}}`)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"", "high"} {
		selection := model
		if variant != "" {
			selection += "#" + variant
		}
		args, err := openCodeModelArgs(t.Context(), bin, selection, "review")
		if err != nil || !slices.Equal(args, []string{"--session", "ses_fixture"}) {
			t.Fatalf("v2 args %v, err %v", args, err)
		}
		var payload struct {
			Agent    string
			Model    map[string]string
			Location struct{ Directory string }
		}
		body, err := os.ReadFile(filepath.Join(dir, "request.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Agent != "review" || payload.Model["providerID"] != "provider" || payload.Model["id"] != "nested/model's name" || payload.Model["variant"] != variant || payload.Location.Directory != cwd {
			t.Fatalf("unexpected session request %s", body)
		}
	}
	for _, model := range []string{"no-provider", "/model", "provider/", "provider/model#", "provider/model#x#y"} {
		if _, err := openCodeModelArgs(t.Context(), bin, model, ""); err == nil {
			t.Errorf("accepted invalid model %q", model)
		}
	}
	t.Setenv("OC_RESPONSE", `{"data":{}}`)
	if _, err := openCodeModelArgs(t.Context(), bin, model, ""); err == nil {
		t.Fatal("opened a session without its ID")
	}
}
