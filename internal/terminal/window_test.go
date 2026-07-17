package terminal

import (
	"errors"
	"testing"
)

type fakeExec struct {
	binary string
	args   []string
}

func (f *fakeExec) Command(binary string, args ...string) error {
	f.binary = binary
	f.args = args
	return nil
}

func TestWindowOpenerKittyArgs(t *testing.T) {
	fe := &fakeExec{}
	w := &windowOpener{binary: "kitty", args: kittyArgs, exec: fe.Command}
	if _, err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if fe.binary != "kitty" {
		t.Fatalf("wrong binary: %s", fe.binary)
	}
	assertContains(t, fe.args, "--title")
	assertContains(t, fe.args, "feat/bar")
	assertContains(t, fe.args, "tmux")
	assertContains(t, fe.args, "attach")
	assertContains(t, fe.args, "-t")
	assertContains(t, fe.args, "moomux-foo")
}

func TestWindowOpenerWindowsTerminalArgs(t *testing.T) {
	fe := &fakeExec{}
	w := &windowOpener{binary: "wt.exe", args: windowsTerminalArgs, exec: fe.Command}
	if _, err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if fe.binary != "wt.exe" {
		t.Fatalf("wrong binary: %s", fe.binary)
	}
	assertContains(t, fe.args, "new-tab")
	assertContains(t, fe.args, "--title")
	assertContains(t, fe.args, "feat/bar")
	assertContains(t, fe.args, "tmux")
	assertContains(t, fe.args, "attach")
	assertContains(t, fe.args, "-t")
	assertContains(t, fe.args, "moomux-foo")
}

func TestWindowOpenerGhosttyArgs(t *testing.T) {
	fe := &fakeExec{}
	w := &windowOpener{binary: "ghostty", args: ghosttyArgs, exec: fe.Command}
	if _, err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if fe.binary != "ghostty" {
		t.Fatalf("wrong binary: %s", fe.binary)
	}
	assertContains(t, fe.args, "--title=feat/bar")
	assertContains(t, fe.args, "-e")
	assertContains(t, fe.args, "tmux")
	assertContains(t, fe.args, "attach")
	assertContains(t, fe.args, "-t")
	assertContains(t, fe.args, "moomux-foo")
}

func TestWezTermArgsSpawnTab(t *testing.T) {
	args := weztermArgs("feat/bar", "moomux-foo")
	assertContains(t, args, "cli")
	assertContains(t, args, "spawn")
	assertContains(t, args, "--")
	assertContains(t, args, "tmux")
	assertContains(t, args, "moomux-foo")
}

func TestWezTermStartArgsFallback(t *testing.T) {
	args := weztermStartArgs("feat/bar", "moomux-foo")
	assertContains(t, args, "start")
	assertContains(t, args, "--")
	assertContains(t, args, "tmux")
	assertContains(t, args, "moomux-foo")
}

func TestKittyTabArgs(t *testing.T) {
	args := kittyTabArgs("feat/bar", "moomux-foo")
	assertContains(t, args, "@")
	assertContains(t, args, "launch")
	assertContains(t, args, "--type=tab")
	assertContains(t, args, "--tab-title=feat/bar")
	assertContains(t, args, "tmux")
	assertContains(t, args, "attach")
	assertContains(t, args, "-t")
	assertContains(t, args, "moomux-foo")
}

func TestAlacrittyMsgArgs(t *testing.T) {
	args := alacrittyMsgArgs("feat/bar", "moomux-foo")
	assertContains(t, args, "msg")
	assertContains(t, args, "create-window")
	assertContains(t, args, "-T")
	assertContains(t, args, "feat/bar")
	assertContains(t, args, "-e")
	assertContains(t, args, "tmux")
	assertContains(t, args, "moomux-foo")
}

func TestFootArgs(t *testing.T) {
	args := footArgs("feat/bar", "moomux-foo")
	assertContains(t, args, "--title=feat/bar")
	assertContains(t, args, "tmux")
	assertContains(t, args, "attach")
	assertContains(t, args, "-t")
	assertContains(t, args, "moomux-foo")
}

func TestRemoteOpenerUsesRemoteWhenItSucceeds(t *testing.T) {
	remote := &fakeExec{}
	fallback := &fakeExec{}
	r := &remoteOpener{
		binary:   "kitten",
		args:     kittyTabArgs,
		fallback: &windowOpener{binary: "kitty", args: kittyArgs, exec: fallback.Command},
		run:      func(b string, a ...string) error { remote.binary, remote.args = b, a; return nil },
	}
	hint, err := r.OpenSession("moomux-foo", "feat/bar")
	if err != nil {
		t.Fatal(err)
	}
	if hint != "" {
		t.Fatalf("unexpected hint: %q", hint)
	}
	if remote.binary != "kitten" {
		t.Fatalf("remote not invoked, got binary %q", remote.binary)
	}
	if fallback.binary != "" {
		t.Fatalf("fallback should not run on remote success, ran %q", fallback.binary)
	}
}

func TestRemoteOpenerFallsBackOnError(t *testing.T) {
	fallback := &fakeExec{}
	r := &remoteOpener{
		binary:   "kitten",
		args:     kittyTabArgs,
		fallback: &windowOpener{binary: "kitty", args: kittyArgs, exec: fallback.Command},
		run:      func(string, ...string) error { return errors.New("remote control disabled") },
	}
	if _, err := r.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if fallback.binary != "kitty" {
		t.Fatalf("expected fallback to kitty, got %q", fallback.binary)
	}
	assertContains(t, fallback.args, "moomux-foo")
}

func TestWindowOpenerAlacrittyArgs(t *testing.T) {
	fe := &fakeExec{}
	w := &windowOpener{binary: "alacritty", args: alacrittyArgs, exec: fe.Command}
	if _, err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	assertContains(t, fe.args, "--title")
	assertContains(t, fe.args, "feat/bar")
}

func TestWindowOpenerGnomeTerminalArgs(t *testing.T) {
	fe := &fakeExec{}
	w := &windowOpener{binary: "gnome-terminal", args: gnomeTerminalArgs, exec: fe.Command}
	if _, err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if fe.binary != "gnome-terminal" {
		t.Fatalf("wrong binary: %s", fe.binary)
	}
	assertContains(t, fe.args, "--tab")
	assertContains(t, fe.args, "--title")
	assertContains(t, fe.args, "feat/bar")
	assertContains(t, fe.args, "--")
	assertContains(t, fe.args, "tmux")
	assertContains(t, fe.args, "attach")
	assertContains(t, fe.args, "-t")
	assertContains(t, fe.args, "moomux-foo")
}

func TestWindowOpenerKonsoleArgs(t *testing.T) {
	fe := &fakeExec{}
	w := &windowOpener{binary: "konsole", args: konsoleArgs, exec: fe.Command}
	if _, err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if fe.binary != "konsole" {
		t.Fatalf("wrong binary: %s", fe.binary)
	}
	assertContains(t, fe.args, "--new-tab")
	assertContains(t, fe.args, "--title")
	assertContains(t, fe.args, "feat/bar")
	assertContains(t, fe.args, "-e")
	assertContains(t, fe.args, "tmux")
	assertContains(t, fe.args, "moomux-foo")
}

func TestWindowOpenerXtermArgs(t *testing.T) {
	fe := &fakeExec{}
	w := &windowOpener{binary: "xterm", args: xtermArgs, exec: fe.Command}
	if _, err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if fe.binary != "xterm" {
		t.Fatalf("wrong binary: %s", fe.binary)
	}
	assertContains(t, fe.args, "-title")
	assertContains(t, fe.args, "feat/bar")
	assertContains(t, fe.args, "-e")
	assertContains(t, fe.args, "tmux")
	assertContains(t, fe.args, "moomux-foo")
}

func TestWindowOpenerTilixArgs(t *testing.T) {
	fe := &fakeExec{}
	w := &windowOpener{binary: "tilix", args: tilixArgs, exec: fe.Command}
	if _, err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if fe.binary != "tilix" {
		t.Fatalf("wrong binary: %s", fe.binary)
	}
	assertContains(t, fe.args, "-e")
	assertContains(t, fe.args, "tmux")
	assertContains(t, fe.args, "moomux-foo")
}

func TestWindowOpenerCmuxArgs(t *testing.T) {
	fe := &fakeExec{}
	w := &windowOpener{binary: "cmux", args: cmuxArgs, exec: fe.Command}
	if _, err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if fe.binary != "cmux" {
		t.Fatalf("wrong binary: %s", fe.binary)
	}
	assertContains(t, fe.args, "new-workspace")
	assertContains(t, fe.args, "--name")
	assertContains(t, fe.args, "feat/bar")
	assertContains(t, fe.args, "--command")
	assertContains(t, fe.args, "tmux attach -t moomux-foo")
}

func assertContains(t *testing.T, haystack []string, needle string) {
	t.Helper()
	for _, s := range haystack {
		if s == needle {
			return
		}
	}
	t.Fatalf("args %v missing %q", haystack, needle)
}
