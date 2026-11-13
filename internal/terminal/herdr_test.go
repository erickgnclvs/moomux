package terminal

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestMain clears TERM_PROGRAM, HERDR_ENV and TMUX: several Detect tests
// only set the vars of the terminal they're about, so the developer's own
// terminal (herdr, iTerm, ...) would otherwise win the switch and fail
// them — and inside tmux, Detect would ask the real tmux server.
func TestMain(m *testing.M) {
	os.Unsetenv("TERM_PROGRAM")
	os.Unsetenv("HERDR_ENV")
	os.Unsetenv("TMUX")
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

// withClientEnv stubs the most recently active tmux client and its env;
// a nil env means it can't be read (as on macOS, without /proc).
func withClientEnv(t *testing.T, env map[string]string) {
	t.Helper()
	withAttachedClients(t, map[int][]string{7: {"zsh"}}, 7)
	orig := processEnv
	t.Cleanup(func() { processEnv = orig })
	processEnv = func(int) (map[string]string, error) {
		if env == nil {
			return nil, errors.New("no /proc")
		}
		return env, nil
	}
}

// moomux inside tmux inside herdr: tmux overwrites TERM_PROGRAM, but
// herdr's own vars survive into the pane. The client's env is unreadable
// here, so moomux's own is all there is.
func TestDetectReturnsHerdrClientInsideTmux(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "tmux")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("HERDR_ENV", "1")
	withClientEnv(t, nil)
	if got := Detect(); !isHerdr(got) {
		t.Fatalf("expected *herdrClient, got %T", got)
	}
}

// The tmux server was started from herdr, so moomux's env says herdr even
// when the session is attached from Windows Terminal; the attached
// client's env is the one that's true, and opens there.
func TestDetectPrefersAttachedClientEnvOverStaleTmuxEnv(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("HERDR_ENV", "1")
	withClientEnv(t, map[string]string{"WT_SESSION": "abc"})
	wo, ok := Detect().(*windowOpener)
	if !ok || wo.binary != "wt.exe" {
		t.Fatalf("expected wt.exe windowOpener, got %#v", Detect())
	}
}

// A readable client env that names no terminal (an ssh client, say) must
// not fall back to the stale env either.
func TestDetectIgnoresStaleEnvWhenClientEnvHasNoTerminal(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("HERDR_ENV", "1")
	withClientEnv(t, map[string]string{"SSH_TTY": "/dev/pts/3"})
	if got := Detect(); isHerdr(got) {
		t.Fatalf("stale HERDR_ENV won over the attached client: %T", got)
	}
}

// Attached from a herdr pane, the tab opens in *that* herdr workspace, not
// the one the tmux server happened to start in.
func TestDetectHerdrUsesClientWorkspace(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("HERDR_WORKSPACE_ID", "stale")
	withClientEnv(t, map[string]string{"TERM_PROGRAM": "herdr", "HERDR_WORKSPACE_ID": "w9"})
	h, ok := Detect().(*herdrClient)
	if !ok || h.workspace != "w9" {
		t.Fatalf("expected herdr client in w9, got %#v", Detect())
	}
}

func TestParseEnviron(t *testing.T) {
	env := parseEnviron([]byte("A=1\x00B=x=y\x00junk\x00\x00"))
	if len(env) != 2 || env["A"] != "1" || env["B"] != "x=y" {
		t.Fatalf("got %v", env)
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
