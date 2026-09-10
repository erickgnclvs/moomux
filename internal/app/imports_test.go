package app

import (
	"os/exec"
	"strings"
	"testing"
)

// The core does not open terminals — see the package comment, AGENTS.md and
// docs/wire-protocol.md. That's a claim about a dependency, so check the
// dependency: every front end goes through terminalBackend in main.go
// instead, and the day something here imports internal/terminal again is
// the day the rule quietly stopped being true.
func TestAppDoesNotDependOnTerminal(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, dep := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(dep) == "github.com/erickgnclvs/moomux/internal/terminal" {
			t.Fatalf("internal/app must not depend on internal/terminal — " +
				"opening and closing terminals belongs to the front end (terminalBackend in main.go)")
		}
	}
}
