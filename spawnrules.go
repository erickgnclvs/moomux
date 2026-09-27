package main

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
)

// multiRepoDoc is the doc the coordination rules are written in. spawn
// embeds it rather than keeping its own copy, so the rules a spawned
// session is told and the rules a person reads can't drift apart.
//
//go:embed docs/multi-repo-sessions.md
var multiRepoDoc string

// spawnRules returns the block between the <!-- spawn-rules --> markers in
// multiRepoDoc, without the code fence that makes it render as literal
// text there.
func spawnRules() (string, error) {
	_, rest, ok := strings.Cut(multiRepoDoc, "<!-- spawn-rules -->")
	block, _, ok2 := strings.Cut(rest, "<!-- /spawn-rules -->")
	if !ok || !ok2 {
		return "", fmt.Errorf("docs/multi-repo-sessions.md has lost its spawn-rules markers")
	}
	block = strings.TrimSpace(block)
	block = strings.TrimPrefix(block, "```text")
	block = strings.TrimSuffix(block, "```")
	return strings.TrimSpace(block), nil
}

// coordinatedPrompt appends the coordination rules to prompt, for a session
// that has to agree with peers on a shared contract. With neither peers nor
// contract set it returns prompt unchanged; one without the other is an
// error, since the rules are about both.
//
// contract is resolved to an absolute path, because the spawned session
// runs in another worktree and a relative one would point nowhere there. A
// URL is left alone. coordinator is the session spawning this one; empty
// when spawn wasn't run from inside a moomux session.
func coordinatedPrompt(prompt, peers, contract, coordinator string) (string, error) {
	if peers == "" && contract == "" {
		return prompt, nil
	}
	if peers == "" || contract == "" {
		return "", fmt.Errorf("-peers and -contract go together")
	}
	var names []string
	for _, p := range strings.Split(peers, ",") {
		if p = strings.TrimSpace(p); p != "" {
			names = append(names, p)
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("-peers names no sessions")
	}
	if !strings.Contains(contract, "://") {
		abs, err := filepath.Abs(contract)
		if err != nil {
			return "", fmt.Errorf("-contract: %w", err)
		}
		contract = abs
	}
	if coordinator == "" {
		coordinator = "the session that spawned you"
	}
	rules, err := spawnRules()
	if err != nil {
		return "", err
	}
	rules = strings.NewReplacer(
		"<peers>", strings.Join(names, ", "),
		"<contract>", contract,
		"<coordinator>", coordinator,
	).Replace(rules)
	if prompt == "" {
		return rules, nil
	}
	return prompt + "\n\n" + rules, nil
}
