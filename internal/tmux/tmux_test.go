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
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.NewSession("moomux-foo", "/tmp/wt", "claude", "foo"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-foo", "-c", "/tmp/wt", "-n", "foo"},
		{"set-window-option", "-t", "moomux-foo", "automatic-rename", "off"},
		{"set-option", "-t", "moomux-foo", "set-titles", "on"},
		{"set-option", "-t", "moomux-foo", "set-titles-string", "#{window_name}"},
		{"send-keys", "-t", "moomux-foo", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestNewSessionNoWindowName(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.NewSession("moomux-foo", "/tmp/wt", "claude", ""); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-foo", "-c", "/tmp/wt"},
		{"send-keys", "-t", "moomux-foo", "claude", "Enter"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestNewSessionNoCmd(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.NewSession("moomux-foo", "/tmp/wt", "", "foo"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"new-session", "-d", "-s", "moomux-foo", "-c", "/tmp/wt", "-n", "foo"},
		{"set-window-option", "-t", "moomux-foo", "automatic-rename", "off"},
		{"set-option", "-t", "moomux-foo", "set-titles", "on"},
		{"set-option", "-t", "moomux-foo", "set-titles-string", "#{window_name}"},
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

func TestKillWindow(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.KillWindow("moomux-mosaic"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"kill-window", "-t", "moomux-mosaic"}) {
		t.Fatalf("got %v", got)
	}
}

func TestNewWindow(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.NewWindow("moomux-mosaic"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"new-window", "-d", "-n", "moomux-mosaic"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSplitWindow(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SplitWindow("moomux-mosaic"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"split-window", "-t", "moomux-mosaic"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSendKeys(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SendKeys("moomux-mosaic", "tmux attach -t moomux-foo"); err != nil {
		t.Fatal(err)
	}
	want := []string{"send-keys", "-t", "moomux-mosaic", "tmux attach -t moomux-foo", "Enter"}
	if got := fr.calls[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestSelectLayout(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SelectLayout("moomux-mosaic", "tiled"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"select-layout", "-t", "moomux-mosaic", "tiled"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSetPaneBorderStatus(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SetPaneBorderStatus("moomux-mosaic", "top"); err != nil {
		t.Fatal(err)
	}
	want := []string{"set-option", "-t", "moomux-mosaic", "pane-border-status", "top"}
	if got := fr.calls[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestSelectPane(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SelectPane("moomux-mosaic.0", "auth-refactor"); err != nil {
		t.Fatal(err)
	}
	want := []string{"select-pane", "-t", "moomux-mosaic.0", "-T", "auth-refactor"}
	if got := fr.calls[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestSelectWindow(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.SelectWindow("moomux-mosaic"); err != nil {
		t.Fatal(err)
	}
	if got := fr.calls[0]; !reflect.DeepEqual(got, []string{"select-window", "-t", "moomux-mosaic"}) {
		t.Fatalf("got %v", got)
	}
}
