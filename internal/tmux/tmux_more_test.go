package tmux

import (
	"errors"
	"reflect"
	"testing"
)

func TestLiveSessions(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		"list-sessions -F #{session_name}": "moomux-a\nmoomux-b\n\n",
	}}
	c := &Client{Runner: fr}
	got := c.LiveSessions()
	want := map[string]bool{"moomux-a": true, "moomux-b": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestLiveSessionsNoServer(t *testing.T) {
	// tmux exits non-zero when no server is running; that means "no sessions".
	fr := &fakeRunner{failOn: map[string]bool{"list-sessions -F #{session_name}": true}}
	c := &Client{Runner: fr}
	if got := c.LiveSessions(); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestConfigureTitleTracking(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	c.ConfigureTitleTracking("moomux-a", "a")
	want := [][]string{
		{"rename-window", "-t", "moomux-a", "a"},
		{"set-window-option", "-t", "moomux-a", "automatic-rename", "off"},
		{"set-option", "-t", "moomux-a", "set-titles", "on"},
		{"set-option", "-t", "moomux-a", "set-titles-string", "#{window_name}"},
		{"set-option", "-t", "moomux-a", "mouse", "on"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestNewSessionErrors(t *testing.T) {
	// new-session itself fails
	fr := &fakeRunner{failOn: map[string]bool{"new-session -d -s s -c /wt -n w": true}}
	c := &Client{Runner: fr}
	if err := c.NewSession("s", "/wt", "cmd", "w"); err == nil {
		t.Fatal("expected error from new-session")
	}

	// list-panes fails
	fr = &fakeRunner{failOn: map[string]bool{"list-panes -t s -F #{pane_id}": true}}
	c = &Client{Runner: fr}
	if err := c.NewSession("s", "/wt", "cmd", "w"); err == nil {
		t.Fatal("expected error from list-panes")
	}

	// split-window fails
	fr = &fakeRunner{
		out:    map[string]string{"list-panes -t s -F #{pane_id}": "%0\n"},
		failOn: map[string]bool{"split-window -h -t s -c /wt -l 33%": true},
	}
	c = &Client{Runner: fr}
	if err := c.NewSession("s", "/wt", "cmd", "w"); err == nil {
		t.Fatal("expected error from split-window")
	}

	// select-pane fails
	fr = &fakeRunner{
		out:    map[string]string{"list-panes -t s -F #{pane_id}": "%0\n"},
		failOn: map[string]bool{"select-pane -t %0": true},
	}
	c = &Client{Runner: fr}
	if err := c.NewSession("s", "/wt", "cmd", "w"); err == nil {
		t.Fatal("expected error from select-pane")
	}

	// send-keys fails
	fr = &fakeRunner{
		out:    map[string]string{"list-panes -t s -F #{pane_id}": "%0\n"},
		failOn: map[string]bool{"send-keys -t %0 cmd Enter": true},
	}
	c = &Client{Runner: fr}
	if err := c.NewSession("s", "/wt", "cmd", "w"); err == nil {
		t.Fatal("expected error from send-keys")
	}
}

type plainErrRunner struct{}

func (plainErrRunner) Run(args ...string) (string, error) {
	return "", errors.New("tmux exploded") // no ExitCode(): a real failure, not "absent"
}

func TestHasSessionNonExitError(t *testing.T) {
	c := &Client{Runner: plainErrRunner{}}
	if _, err := c.HasSession("s"); err == nil {
		t.Fatal("expected non-exit errors to propagate")
	}
}

func TestPaneCwdError(t *testing.T) {
	fr := &fakeRunner{failOn: map[string]bool{"list-panes -t s -F #{pane_current_path}": true}}
	c := &Client{Runner: fr}
	if _, err := c.PaneCwd("s"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewUsesExecRunner(t *testing.T) {
	if New().Runner == nil {
		t.Fatal("nil runner")
	}
}
