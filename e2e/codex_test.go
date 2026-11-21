//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erickgnclvs/moomux/internal/tmux"
)

type codexTestServer string

func (s codexTestServer) Run(args ...string) (string, error) {
	// -u first, like tmux's execRunner: the Runner owns it, not the callers.
	args = append([]string{"-u", "-L", string(s), "-f", "/dev/null"}, args...)
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return string(out), err
}

func TestSessionStatesWithoutUTF8Locale(t *testing.T) {
	// launchd supplies no UTF-8 locale. tmux then replaces literal tabs
	// in format output with underscores unless the client requests UTF-8.
	t.Setenv("LC_ALL", "C")
	t.Setenv("LC_CTYPE", "C")
	t.Setenv("LANG", "C")
	t.Setenv("TMUX", "")
	// Even an empty TMUX makes tmux assume a UTF-8 client. Unset it,
	// keeping Setenv's cleanup to restore the test process's environment.
	if err := os.Unsetenv("TMUX"); err != nil {
		t.Fatal(err)
	}
	server := codexTestServer(fmt.Sprintf("moomux-test-status-%d", os.Getpid()))
	if out, err := server.Run("new-session", "-d", "-s", "test", "sleep 120"); err != nil {
		t.Fatalf("start tmux: %v %s", err, out)
	}
	t.Cleanup(func() { _, _ = server.Run("kill-server") })
	c := &tmux.Client{Runner: server}
	got := c.SessionStates()
	if state, live := got["test"]; !live || state != "" || len(got) != 1 {
		t.Fatalf("session states = %v, want live test session without a native status", got)
	}
}

// Drive the real binary through a tmux output pipe on a private server.
// The helper path contains a space, and the agent pane isn't the active one.
func TestCodexPaneReportsOnlyDisplayedState(t *testing.T) {
	server := codexTestServer(fmt.Sprintf("moomux-test-codex-%d", os.Getpid()))
	run := func(args ...string) string {
		t.Helper()
		out, err := server.Run(args...)
		if err != nil {
			t.Fatalf("tmux %s: %v %s", args[0], err, out)
		}
		return strings.TrimSpace(out)
	}
	bin := filepath.Join(t.TempDir(), "moomux observer")
	if out, err := exec.Command("go", "build", "-o", bin, "..").CombinedOutput(); err != nil {
		t.Fatalf("build observer: %v %s", err, out)
	}
	run("new-session", "-d", "-s", "test", "sleep 120")
	t.Cleanup(func() { _, _ = server.Run("kill-server") })
	pane := run("display-message", "-p", "#{pane_id}")
	run("set-option", "-p", "-t", pane, "@moomux_agent_pane", "1")
	run("split-window", "sleep 120")
	tty := run("display-message", "-t", pane, "-p", "#{pane_tty}")
	c := &tmux.Client{Runner: server, CodexObserver: bin}
	c.ObserveCodex("test")
	c.ObserveCodex("test")
	emit := func(seq string) {
		t.Helper()
		f, err := os.OpenFile(tty, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(seq); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(want string) {
		t.Helper()
		for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); {
			if c.SessionStates()["test"] == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("pane status = %q, want %q", c.SessionStates()["test"], want)
	}
	emit("\x1b]7501;state=working:app=codex\x1b\\")
	wait("working")
	emit("\x1b]7501;state=blocked:app=codex:kind=permission\x1b\\")
	wait("needs-input")
	emit("\x1b]7501;state=working:app=codex\x1b\\")
	wait("working")
	emit("\x1b]7501;state=done:app=codex\x1b\\Should I continue?")
	wait("done")

	// Replacing the reader with a user's pipe must retire its last status.
	log := filepath.Join(t.TempDir(), "captured")
	run("set-option", "-p", "-t", pane, "@test_capture", log)
	run("pipe-pane", "-t", pane, "cat > #{q:@test_capture}")
	c.ObserveCodex("test")
	emit("pipe is still running\n")
	wait("")
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		data, _ := os.ReadFile(log)
		if strings.Contains(string(data), "pipe is still running") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("user pipe did not receive pane output")
}
