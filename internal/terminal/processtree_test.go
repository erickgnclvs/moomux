package terminal

import (
	"errors"
	"testing"
)

func withAttachedClient(t *testing.T, names []string) {
	t.Helper()
	origPID := attachedClientPID
	origAncestors := processAncestors
	t.Cleanup(func() {
		attachedClientPID = origPID
		processAncestors = origAncestors
	})
	attachedClientPID = func() (int, error) { return 4242, nil }
	processAncestors = func(pid int) []string { return names }
}

func TestDetectFromProcessTreeFindsITerm(t *testing.T) {
	withAttachedClient(t, []string{"zsh", "login", "iTerm2"})
	if _, ok := detectFromProcessTree().(*itermClient); !ok {
		t.Fatalf("expected *itermClient, got %T", detectFromProcessTree())
	}
}

func TestDetectFromProcessTreeFindsAppleTerminal(t *testing.T) {
	withAttachedClient(t, []string{"zsh", "login", "Terminal"})
	got := detectFromProcessTree()
	if _, ok := got.(*terminalAppClient); !ok {
		t.Fatalf("expected *terminalAppClient, got %#v", got)
	}
}

func TestDetectFromProcessTreeReturnsNilForUnknown(t *testing.T) {
	withAttachedClient(t, []string{"zsh", "sshd"})
	if got := detectFromProcessTree(); got != nil {
		t.Fatalf("expected nil, got %T", got)
	}
}

func TestDetectFromProcessTreeReturnsNilWhenClientLookupFails(t *testing.T) {
	origPID := attachedClientPID
	t.Cleanup(func() { attachedClientPID = origPID })
	attachedClientPID = func() (int, error) { return 0, errors.New("no attached client") }

	if got := detectFromProcessTree(); got != nil {
		t.Fatalf("expected nil, got %T", got)
	}
}
