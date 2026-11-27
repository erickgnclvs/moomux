package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/session"
)

func TestInstallKnownCommandsInstallsAutomaticTagging(t *testing.T) {
	files := []struct {
		path       string
		invocation string // nonempty for discoverable skills
	}{
		{".agents/skills/tag/SKILL.md", "$tag"},
		{".codex/prompts/tag.md", ""},
		{".claude/skills/tag/SKILL.md", "/tag"},
		{".claude/commands/tag.md", ""},
		{".gemini/config/skills/tag/SKILL.md", "/tag"},
	}

	for _, scenario := range []string{"fresh", "upgrade"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, _ := newTestApp(t, nil)
			home := os.Getenv("HOME")
			if err := a.rememberAgent("codex"); err != nil {
				t.Fatal(err)
			}
			for _, agent := range []string{"claude", "antigravity"} {
				if err := a.Store.Put(session.Session{ID: "project:" + agent, Agent: agent}); err != nil {
					t.Fatal(err)
				}
			}

			if scenario == "upgrade" {
				// The previous installer-managed Codex skill.
				const oldSkill = "---\n" + `name: tag
description: Tag the current moomux session with its pull request and any tracked ticket. Use when the user invokes $tag or asks to tag a moomux session.
---

Run ` + "`moomux tag`" + ` with no flags first in a shell with ` + "`sandbox_permissions`" + ` set to ` + "`require_escalated`" + ` to see what is already tracked on this session. Use the same shell setting for the update command below.

Find the open pull request for the current branch and run:

    moomux tag -pr <that PR URL>

Leave out ` + "`-ticket`" + ` because moomux keeps the existing ticket when that flag is omitted. If no ticket is tracked yet, look for a ticket link in the pull request title or body, branch name, and recent commit messages. Recognize Asana, Jira, and Linear ticket references. If you find one, pass it too:

    moomux tag -pr <PR URL> -ticket <ticket URL>

If there is no open pull request, say so instead of guessing. Do not guess a ticket link either.
`
				path := filepath.Join(home, ".agents", "skills", "tag", "SKILL.md")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(oldSkill), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			a.InstallKnownCommands()

			for _, file := range files {
				data, err := os.ReadFile(filepath.Join(home, file.path))
				if err != nil {
					t.Fatal(err)
				}
				_, document, _ := strings.Cut(string(data), "---\n")
				frontmatter, body, ok := strings.Cut(document, "\n---\n")
				if !ok {
					t.Fatalf("%s: missing frontmatter", file.path)
				}
				if file.invocation != "" {
					for _, text := range []string{"name: tag", "description: Automatically tag", "immediately after creating the PR", file.invocation} {
						if !strings.Contains(frontmatter, text) {
							t.Errorf("%s: frontmatter missing %q", file.path, text)
						}
					}
				}
				for _, text := range []string{
					"`moomux tag` (no flags)",
					"moomux tag -ticket '<ticket URL>'\n",
					"moomux tag -pr '<PR URL>'\n",
					"moomux tag -ticket '<ticket URL>' -pr '<PR URL>'",
					"An omitted flag preserves the other tag",
					"already matches, skip the update",
					"the user has decided there is no ticket",
					"background, comparison, or another task",
				} {
					if !strings.Contains(body, text) {
						t.Errorf("%s: instructions missing %q", file.path, text)
					}
				}
			}
		})
	}
}
