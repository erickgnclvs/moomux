package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestApplyUpdateRestartsARunningService: `u` used to relaunch only the TUI,
// so a `brew services` core kept running the old binary and rejected the new
// TUI's SetTerminalPane as an unknown method.
func TestApplyUpdateRestartsARunningService(t *testing.T) {
	const listing = "Name    Status  User File\nredis   started moo  ~/Library/LaunchAgents/homebrew.mxcl.redis.plist\nmoomux  %s\n"
	for _, tc := range []struct {
		name        string
		services    string // `brew services list` output; "" makes it fail
		restartErr  error
		wantRestart bool
		wantErr     string
	}{
		{name: "started", services: strings.Replace(listing, "%s", "started moo ~/Library/LaunchAgents/sh.brew.moomux.plist", 1), wantRestart: true},
		{name: "not started", services: strings.Replace(listing, "%s", "none", 1)},
		{name: "not listed", services: "Name Status User File\nredis started moo x\n"},
		{name: "no brew services", services: ""},
		{name: "restart fails", services: strings.Replace(listing, "%s", "started moo x", 1), restartErr: errors.New("exit 1"), wantRestart: true, wantErr: "brew services restart moomux"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			orig := brew
			t.Cleanup(func() { brew = orig })
			brew = func(args ...string) ([]byte, error) {
				cmd := strings.Join(args, " ")
				calls = append(calls, cmd)
				switch cmd {
				case "services list":
					if tc.services == "" {
						return []byte("Error: unknown command"), errors.New("exit 1")
					}
					return []byte(tc.services), nil
				case "services restart moomux":
					return nil, tc.restartErr
				}
				return nil, nil
			}

			err := applyUpdate()
			if tc.wantErr == "" && err != nil {
				t.Fatalf("applyUpdate: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
			if got := slices.Contains(calls, "services restart moomux"); got != tc.wantRestart {
				t.Fatalf("restarted = %v, want %v (calls %q)", got, tc.wantRestart, calls)
			}
			if slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "services start") }) {
				t.Fatalf("started a service the user doesn't run: %q", calls)
			}
		})
	}
}

// TestApplyUpdateStopsWhenUpgradeFails: a failed upgrade must not bounce the
// core onto the binary it already had.
func TestApplyUpdateStopsWhenUpgradeFails(t *testing.T) {
	var calls []string
	orig := brew
	t.Cleanup(func() { brew = orig })
	brew = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "upgrade" {
			return []byte("Error: no network"), errors.New("exit 1")
		}
		return []byte("moomux started"), nil
	}
	if err := applyUpdate(); err == nil || !strings.Contains(err.Error(), "no network") {
		t.Fatalf("err = %v, want brew's output", err)
	}
	if !slices.Equal(calls, []string{"update", "upgrade moomux"}) {
		t.Fatalf("calls = %q", calls)
	}
}
