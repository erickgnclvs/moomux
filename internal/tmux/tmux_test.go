package tmux

import (
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls  [][]string
	out    map[string]string
	failOn map[string]bool
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.failOn[key] {
		return "", exitErr{code: 1}
	}
	return f.out[key], nil
}

type exitErr struct{ code int }

func (e exitErr) Error() string { return "exit" }
func (e exitErr) ExitCode() int { return e.code }

func TestNewSession(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{"list-panes -t moomux-foo -F #{pane_id}": "%3\n"}}
	c := &Client{Runner: fr}
	if err := c.NewSession("moomux-foo", "/tmp/wt", "claude", "foo"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-foo", "-c", "/tmp/wt", "-n", "foo"},
		{"set-window-option", "-t", "moomux-foo", "automatic-rename", "off"},
		{"set-option", "-t", "moomux-foo", "set-titles", "on"},
		{"set-option", "-t", "moomux-foo", "set-titles-string", "#{window_name}"},
		{"set-option", "-t", "moomux-foo", "mouse", "on"},
		{"list-panes", "-t", "moomux-foo", "-F", "#{pane_id}"},
		{"split-window", "-h", "-t", "moomux-foo", "-c", "/tmp/wt", "-l", "33%"},
		{"select-pane", "-t", "%3"},
		{"send-keys", "-t", "%3", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestNewSessionNoWindowName(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{"list-panes -t moomux-foo -F #{pane_id}": "%3\n"}}
	c := &Client{Runner: fr}
	if err := c.NewSession("moomux-foo", "/tmp/wt", "claude", ""); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-foo", "-c", "/tmp/wt"},
		{"set-option", "-t", "moomux-foo", "mouse", "on"},
		{"list-panes", "-t", "moomux-foo", "-F", "#{pane_id}"},
		{"split-window", "-h", "-t", "moomux-foo", "-c", "/tmp/wt", "-l", "33%"},
		{"select-pane", "-t", "%3"},
		{"send-keys", "-t", "%3", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestNewSessionNoCmd(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{"list-panes -t moomux-foo -F #{pane_id}": "%3\n"}}
	c := &Client{Runner: fr}
	if err := c.NewSession("moomux-foo", "/tmp/wt", "", "foo"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-foo", "-c", "/tmp/wt", "-n", "foo"},
		{"set-window-option", "-t", "moomux-foo", "automatic-rename", "off"},
		{"set-option", "-t", "moomux-foo", "set-titles", "on"},
		{"set-option", "-t", "moomux-foo", "set-titles-string", "#{window_name}"},
		{"set-option", "-t", "moomux-foo", "mouse", "on"},
		{"list-panes", "-t", "moomux-foo", "-F", "#{pane_id}"},
		{"split-window", "-h", "-t", "moomux-foo", "-c", "/tmp/wt", "-l", "33%"},
		{"select-pane", "-t", "%3"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestHasSessionPresent(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	ok, err := c.HasSession("moomux-foo")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestHasSessionAbsent(t *testing.T) {
	fr := &fakeRunner{failOn: map[string]bool{"has-session -t moomux-foo": true}}
	c := &Client{Runner: fr}
	ok, err := c.HasSession("moomux-foo")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestPaneCwd(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{"list-panes -t moomux-foo -F #{pane_current_path}": "/tmp/wt\n"}}
	c := &Client{Runner: fr}
	got, err := c.PaneCwd("moomux-foo")
	if err != nil || got != "/tmp/wt" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestKillSession(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.KillSession("moomux-foo"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"kill-session", "-t", "moomux-foo"}) {
		t.Fatalf("got %v", got)
	}
}
