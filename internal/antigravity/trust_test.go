package antigravity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func read(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTrustWorkspaceCreatesFile(t *testing.T) {
	home := t.TempDir()
	if err := TrustWorkspace(home, "/wt/one"); err != nil {
		t.Fatal(err)
	}
	got, _ := read(t, home)["trustedWorkspaces"].([]any)
	if len(got) != 1 || got[0] != "/wt/one" {
		t.Fatalf("trustedWorkspaces = %v", got)
	}
}

func TestTrustWorkspaceAppendsAndKeepsOtherSettings(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const existing = `{"model":"Gemini 3.8 Flash (Medium)","trustedWorkspaces":["/wt/old"]}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := TrustWorkspace(home, "/wt/new"); err != nil {
		t.Fatal(err)
	}
	root := read(t, home)
	if root["model"] != "Gemini 3.8 Flash (Medium)" {
		t.Fatalf("clobbered unrelated settings: %v", root)
	}
	got, _ := root["trustedWorkspaces"].([]any)
	if len(got) != 2 || got[0] != "/wt/old" || got[1] != "/wt/new" {
		t.Fatalf("trustedWorkspaces = %v", got)
	}

	// Idempotent: trusting the same path twice must not duplicate it.
	if err := TrustWorkspace(home, "/wt/new"); err != nil {
		t.Fatal(err)
	}
	if got, _ := read(t, home)["trustedWorkspaces"].([]any); len(got) != 2 {
		t.Fatalf("re-trust duplicated: %v", got)
	}
}

// TestTrustWorkspaceRefusesUnexpectedShape guards the user's existing
// approvals: if trustedWorkspaces ever holds something other than a list,
// overwriting it would silently un-trust every workspace already there.
func TestTrustWorkspaceRefusesUnexpectedShape(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const existing = `{"trustedWorkspaces":{"/wt/old":true}}`
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := TrustWorkspace(home, "/wt/new"); err == nil {
		t.Fatal("TrustWorkspace = nil, want an error for a non-list trustedWorkspaces")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Fatalf("file rewritten despite the error:\n got %s\nwant %s", got, existing)
	}
}
