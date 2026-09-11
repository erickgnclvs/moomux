package terminal

import (
	"errors"
	"strings"
	"testing"
)

type fakeKittyRunner struct {
	calls [][]string
	outs  []string // out returned per call, in order
	errs  []error  // err returned per call, in order
}

func (f *fakeKittyRunner) Run(args ...string) (string, error) {
	n := len(f.calls)
	f.calls = append(f.calls, args)
	var out string
	var err error
	if n < len(f.outs) {
		out = f.outs[n]
	}
	if n < len(f.errs) {
		err = f.errs[n]
	}
	return out, err
}

// lsWithForegroundPIDs is `kitten @ ls` output shaped like the real thing:
// tab 8 holds the window whose foreground process is the tmux client.
const lsWithForegroundPIDs = `[{"tabs":[
	{"id":7,"windows":[{"foreground_processes":[{"pid":100}]}]},
	{"id":8,"windows":[{"foreground_processes":[{"pid":222}]}]}
]}]`

// pidsOf builds the attached-client list from pids alone, for kitty, which
// identifies a tab by the pids of its foreground processes.
func pidsOf(pids ...string) []tmuxClient {
	var cs []tmuxClient
	for _, p := range pids {
		cs = append(cs, tmuxClient{TTY: "/dev/ttys0", PID: p})
	}
	return cs
}

// The tab is found by matching tmux's #{client_pid} against the foreground
// processes `kitten @ ls` reports — kitty exposes no tty, and its tab ids
// restart from 1 with the process, so a remembered handle can point at a
// stranger's tab after a kitty restart.
func TestKittyOpenFindsExistingTabByClientPID(t *testing.T) {
	fr := &fakeKittyRunner{outs: []string{lsWithForegroundPIDs, ""}}
	c := &kittyClient{runner: fr, clients: func(string) []tmuxClient { return pidsOf("222") }}
	hint, err := c.OpenSession("moomux-foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	if hint != "" {
		t.Fatalf("want no hint, got %q", hint)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("want ls then focus-tab, got %d: %v", len(fr.calls), fr.calls)
	}
	assertContains(t, fr.calls[0], "ls")
	assertContains(t, fr.calls[1], "focus-tab")
	assertContains(t, fr.calls[1], "id:8")
}

// With nothing attached there is no tab to focus, so one is launched — and
// only launched: kitty used to make a second `@ ls` call just to learn the
// new tab's id, which nothing stores any more.
func TestKittyOpenLaunchesWhenNothingAttached(t *testing.T) {
	fr := &fakeKittyRunner{}
	c := &kittyClient{runner: fr, clients: noTTYs}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("want a single launch call, got %d: %v", len(fr.calls), fr.calls)
	}
	assertContains(t, fr.calls[0], "launch")
}

// tmux's attached client can be some other terminal, so a pid kitty doesn't
// hold is a miss, not an error.
func TestKittyOpenLaunchesWhenNoTabHoldsTheClient(t *testing.T) {
	fr := &fakeKittyRunner{outs: []string{lsWithForegroundPIDs, ""}}
	c := &kittyClient{runner: fr, clients: func(string) []tmuxClient { return pidsOf("999") }}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	assertContains(t, fr.calls[1], "launch")
}

func TestKittyFindTabDoesNotFocus(t *testing.T) {
	fr := &fakeKittyRunner{outs: []string{lsWithForegroundPIDs}}
	c := &kittyClient{runner: fr, clients: func(string) []tmuxClient { return pidsOf("222") }}
	id, err := c.FindTab("moomux-foo")
	if err != nil {
		t.Fatal(err)
	}
	if id != "8" {
		t.Fatalf("want tab 8, got %q", id)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("want only an ls call, got %v", fr.calls)
	}
}

func TestKittyFindTabSkipsLSWhenNothingAttached(t *testing.T) {
	fr := &fakeKittyRunner{}
	c := &kittyClient{runner: fr, clients: noTTYs}
	id, err := c.FindTab("moomux-foo")
	if err != nil || id != "" {
		t.Fatalf("want empty id and no error, got %q / %v", id, err)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("want no remote call at all, got %v", fr.calls)
	}
}

func TestKittyCloseTabIsBestEffort(t *testing.T) {
	fr := &fakeKittyRunner{errs: []error{errors.New("no matching tabs")}}
	c := &kittyClient{runner: fr}
	if err := c.CloseTab("8"); err != nil {
		t.Fatalf("an already-gone tab is not an error: %v", err)
	}
	assertContains(t, fr.calls[0], "close-tab")
	assertContains(t, fr.calls[0], "id:8")
}

func TestKittyOpenFallsBackWhenLaunchFails(t *testing.T) {
	fallback := &fakeExec{}
	fr := &fakeKittyRunner{errs: []error{errors.New("remote control disabled")}}
	c := &kittyClient{runner: fr, clients: noTTYs, fallback: &windowOpener{binary: "kitty", args: kittyArgs, exec: fallback.Command}}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if fallback.binary != "kitty" {
		t.Fatalf("expected fallback to kitty, got %q", fallback.binary)
	}
}

func TestKittyOpenTitleAndSessionEscaping(t *testing.T) {
	fr := &fakeKittyRunner{}
	c := &kittyClient{runner: fr, clients: noTTYs}
	if _, err := c.OpenSession("moomux-foo", "feat/bar"); err != nil {
		t.Fatal(err)
	}
	assertContains(t, fr.calls[0], "--tab-title=feat/bar")
	if !strings.Contains(strings.Join(fr.calls[0], " "), "=moomux-foo") {
		t.Fatalf("missing exact-match session target: %v", fr.calls[0])
	}
}
