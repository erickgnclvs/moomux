package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCoordinatedPrompt(t *testing.T) {
	got, err := coordinatedPrompt("You own api.", " web, infra ,", "plan.md", "coord")
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs("plan.md")
	for _, want := range []string{
		"You own api.\n\nThe shared contract lives at " + abs + ".",
		"Peer sessions are web, infra.",
		"coordinating session (coord)",
		"peer's overlapping version turns up.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
	// A placeholder left over, or the doc's code fence leaking in, means the
	// doc and this code have drifted apart.
	for _, bad := range []string{"<peers>", "<contract>", "<coordinator>", "```"} {
		if strings.Contains(got, bad) {
			t.Errorf("prompt contains %q:\n%s", bad, got)
		}
	}
}

func TestCoordinatedPromptEdges(t *testing.T) {
	if got, err := coordinatedPrompt("task", "", "", ""); err != nil || got != "task" {
		t.Errorf("no flags: got %q, %v; want the prompt unchanged", got, err)
	}
	for _, c := range []struct{ peers, contract string }{{"web", ""}, {"", "plan.md"}, {" , ", "plan.md"}} {
		if _, err := coordinatedPrompt("task", c.peers, c.contract, ""); err == nil {
			t.Errorf("peers=%q contract=%q: want an error", c.peers, c.contract)
		}
	}
	got, err := coordinatedPrompt("", "web", "https://example.com/plan", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "The shared contract lives at https://example.com/plan.") {
		t.Errorf("URL contract should pass through, and no prompt means no leading blank lines:\n%s", got)
	}
	if !strings.Contains(got, "the session that spawned you") {
		t.Errorf("unknown coordinator should fall back:\n%s", got)
	}
}
