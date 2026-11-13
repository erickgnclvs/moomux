package terminal

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestMain clears TERM_PROGRAM and HERDR_ENV: several Detect tests only set
// the vars of the terminal they're about, so the developer's own terminal
// (herdr, iTerm, ...) would otherwise win the switch and fail them.
func TestMain(m *testing.M) {
	os.Unsetenv("TERM_PROGRAM")
	os.Unsetenv("HERDR_ENV")
	os.Exit(m.Run())
}

const herdrTabCreated = `{"id":"cli:tab:create","result":{"root_pane":{"pane_id":"w2C:p2","tab_id":"w2C:t2","workspace_id":"w2C"},"tab":{"label":"bar","tab_id":"w2C:t2"},"type":"tab_created"}}`

func TestDetectReturnsHerdrClient(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "herdr")
	// herdr runs inside another emulator whose vars can leak through;
	// herdr, the innermost, must still win.
	t.Setenv("WT_SESSION", "x")
	t.Setenv("KITTY_WINDOW_ID", "1")
	if got := Detect(); !isHerdr(got) {
		t.Fatalf("expected *herdrClient, got %T", got)
	}
}

// moomux inside tmux inside herdr: tmux overwrites TERM_PROGRAM, but
// herdr's own vars survive into the pane.
func TestDetectReturnsHerdrClientInsideTmux(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "tmux")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("HERDR_ENV", "1")
	if got := Detect(); !isHerdr(got) {
		t.Fatalf("expected *herdrClient, got %T", got)
	}
}

func isHerdr(o TerminalOpener) bool { _, ok := o.(*herdrClient); return ok }

func TestHerdrOpenCreatesTabAndAttaches(t *testing.T) {
	fr := &fakeWeztermRunner{outs: []string{herdrTabCreated, ""}}
	c := &herdrClient{run: fr.run, workspace: "w2C", fallback: &fallbackOpener{}}
	hint, err := c.OpenSession("moomux-foo", "bar")
	if err != nil || hint != "" {
		t.Fatalf("want clean open, got hint=%q err=%v", hint, err)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("want tab create then pane run, got %v", fr.calls)
	}
	create := strings.Join(fr.calls[0], " ")
	for _, want := range []string{"tab create", "--workspace w2C", "--label bar", "--focus"} {
		if !strings.Contains(create, want) {
			t.Fatalf("tab create %q missing %q", create, want)
		}
	}
	run := fr.calls[1]
	if len(run) != 4 || run[0] != "pane" || run[1] != "run" || run[2] != "w2C:p2" || run[3] != "tmux attach -t '=moomux-foo'" {
		t.Fatalf("unexpected pane run: %q", run)
	}
}

// A failed or unparseable create must not report success: that was the
// bug — "opened" with nothing on screen.
func TestHerdrOpenFallsBackWhenCreateFails(t *testing.T) {
	for name, fr := range map[string]*fakeWeztermRunner{
		"error":   {errs: []error{errors.New("no server")}},
		"garbage": {outs: []string{"not json"}},
	} {
		t.Run(name, func(t *testing.T) {
			c := &herdrClient{run: fr.run, fallback: &fallbackOpener{}}
			hint, err := c.OpenSession("moomux-foo", "bar")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(hint, "tmux attach") {
				t.Fatalf("want attach hint, got %q", hint)
			}
		})
	}
}
