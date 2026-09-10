package terminal

import (
	"errors"
	"strings"
	"testing"
)

type fakeWeztermRunner struct {
	calls [][]string
	outs  []string // one entry per call, in order
	errs  []error  // one entry per call, in order
}

func (f *fakeWeztermRunner) run(args ...string) (string, error) {
	i := len(f.calls)
	f.calls = append(f.calls, args)
	var out string
	var err error
	if i < len(f.outs) {
		out = f.outs[i]
	}
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return out, err
}

// listWithTTYs is `wezterm cli list --format json` shaped like the real
// thing: pane 42 is the one on the tty tmux reports as attached.
const listWithTTYs = `[
	{"pane_id":7,"tty_name":"/dev/ttys001"},
	{"pane_id":42,"tty_name":"/dev/ttys011"}
]`

// The pane is found by matching tmux's #{client_tty} against the tty_name
// wezterm reports per pane, not by a remembered id — mux-server pane ids
// restart from 0, so a stale one can activate a stranger's pane.
func TestWeztermOpenFindsExistingPaneByTTY(t *testing.T) {
	fr := &fakeWeztermRunner{outs: []string{listWithTTYs, ""}}
	c := &weztermClient{run: fr.run, clients: func(string) []tmuxClient { return ttysOf("/dev/ttys011") }}
	hint, err := c.OpenSession("moomux-foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	if hint != "" {
		t.Fatalf("want no hint, got %q", hint)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("want list then activate-pane, got %d: %v", len(fr.calls), fr.calls)
	}
	if !strings.Contains(strings.Join(fr.calls[1], " "), "activate-pane --pane-id 42") {
		t.Fatalf("missing activate-pane call: %v", fr.calls[1])
	}
}

// With nothing attached there is no pane to activate, so one is spawned.
func TestWeztermOpenSpawnsWhenNothingAttached(t *testing.T) {
	fr := &fakeWeztermRunner{outs: []string{"7"}}
	c := &weztermClient{run: fr.run, clients: noTTYs}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("want a single spawn call, got %d: %v", len(fr.calls), fr.calls)
	}
}

// A wezterm too old to report tty_name matches nothing and spawns, which is
// the behaviour from before the lookup existed.
func TestWeztermOpenSpawnsWhenNoPaneReportsTheTTY(t *testing.T) {
	fr := &fakeWeztermRunner{outs: []string{`[{"pane_id":7}]`, "9"}}
	c := &weztermClient{run: fr.run, clients: func(string) []tmuxClient { return ttysOf("/dev/ttys011") }}
	if _, err := c.OpenSession("moomux-foo", "bar"); err != nil {
		t.Fatal(err)
	}
	second := strings.Join(fr.calls[1], " ")
	if !strings.Contains(second, "cli spawn") || !strings.Contains(second, "tmux attach -t =moomux-foo") {
		t.Fatalf("second call should spawn+attach: %s", second)
	}
}

func TestWeztermFindTabDoesNotActivate(t *testing.T) {
	fr := &fakeWeztermRunner{outs: []string{listWithTTYs}}
	c := &weztermClient{run: fr.run, clients: func(string) []tmuxClient { return ttysOf("/dev/ttys011") }}
	id, err := c.FindTab("moomux-foo")
	if err != nil {
		t.Fatal(err)
	}
	if id != "42" {
		t.Fatalf("want pane 42, got %q", id)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("want only a list call, got %v", fr.calls)
	}
}

func TestWeztermFindTabSkipsListWhenNothingAttached(t *testing.T) {
	fr := &fakeWeztermRunner{}
	c := &weztermClient{run: fr.run, clients: noTTYs}
	id, err := c.FindTab("moomux-foo")
	if err != nil || id != "" {
		t.Fatalf("want empty id and no error, got %q / %v", id, err)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("want no wezterm call at all, got %v", fr.calls)
	}
}

func TestWeztermCloseTabIsBestEffort(t *testing.T) {
	fr := &fakeWeztermRunner{errs: []error{errors.New("no such pane")}}
	c := &weztermClient{run: fr.run}
	if err := c.CloseTab("42"); err != nil {
		t.Fatalf("an already-gone pane is not an error: %v", err)
	}
	if !strings.Contains(strings.Join(fr.calls[0], " "), "kill-pane --pane-id 42") {
		t.Fatalf("missing kill-pane call: %v", fr.calls[0])
	}
}

func TestWeztermOpenFallsBackWhenSpawnFails(t *testing.T) {
	fr := &fakeWeztermRunner{errs: []error{errors.New("mux unreachable")}}
	fb := &fakeOpener{hint: "fallback hint"}
	c := &weztermClient{run: fr.run, clients: noTTYs, fallback: fb}
	hint, err := c.OpenSession("moomux-foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	if hint != "fallback hint" {
		t.Fatalf("want fallback hint, got %q", hint)
	}
	if fb.tmuxSession != "moomux-foo" || fb.title != "bar" {
		t.Fatalf("fallback called with wrong args: %+v", fb)
	}
}

type fakeOpener struct {
	hint        string
	err         error
	tmuxSession string
	title       string
}

func (f *fakeOpener) OpenSession(tmuxSession, title string) (string, error) {
	f.tmuxSession = tmuxSession
	f.title = title
	return f.hint, f.err
}
