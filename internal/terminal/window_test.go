package terminal

import (
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
	if err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
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

func TestWindowOpenerWezTermArgs(t *testing.T) {
	fe := &fakeExec{}
	w := &windowOpener{binary: "wezterm", args: weztermArgs, exec: fe.Command}
	if err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	if fe.binary != "wezterm" {
		t.Fatalf("wrong binary: %s", fe.binary)
	}
	assertContains(t, fe.args, "start")
	assertContains(t, fe.args, "--")
	assertContains(t, fe.args, "tmux")
}

func TestWindowOpenerAlacrittyArgs(t *testing.T) {
	fe := &fakeExec{}
	w := &windowOpener{binary: "alacritty", args: alacrittyArgs, exec: fe.Command}
	if err := w.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	assertContains(t, fe.args, "--title")
	assertContains(t, fe.args, "feat/bar")
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
