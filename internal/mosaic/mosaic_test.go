package mosaic

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tmux"
)

type fakeRunner struct {
	calls  [][]string
	failOn map[string]bool
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.failOn[key] {
		return "", errors.New("injected failure")
	}
	return "", nil
}

func makeSessions(names ...string) []session.Session {
	out := make([]session.Session, len(names))
	for i, n := range names {
		out[i] = session.Session{Name: n, TmuxSession: "moomux-" + n}
	}
	return out
}

func assertContains(t *testing.T, calls [][]string, want []string) {
	t.Helper()
	for _, call := range calls {
		if reflect.DeepEqual(call, want) {
			return
		}
	}
	t.Fatalf("expected call %v not found in %v", want, calls)
}

func TestOpenEmpty(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Tmux: &tmux.Client{Runner: fr}}
	if err := c.Open(nil); err == nil {
		t.Fatal("expected error for empty sessions")
	}
}

func TestOpenSingleSession(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Tmux: &tmux.Client{Runner: fr}}

	if err := c.Open(makeSessions("auth")); err != nil {
		t.Fatal(err)
	}

	assertContains(t, fr.calls, []string{"kill-window", "-t", "moomux-mosaic"})
	assertContains(t, fr.calls, []string{"new-window", "-d", "-n", "moomux-mosaic"})
	assertContains(t, fr.calls, []string{"send-keys", "-t", "moomux-mosaic", "tmux attach -t moomux-auth", "Enter"})
	assertContains(t, fr.calls, []string{"select-layout", "-t", "moomux-mosaic", "tiled"})
	assertContains(t, fr.calls, []string{"select-window", "-t", "moomux-mosaic"})
}

func TestOpenTwoSessions(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Tmux: &tmux.Client{Runner: fr}}

	if err := c.Open(makeSessions("auth", "payment")); err != nil {
		t.Fatal(err)
	}

	assertContains(t, fr.calls, []string{"send-keys", "-t", "moomux-mosaic", "tmux attach -t moomux-auth", "Enter"})
	assertContains(t, fr.calls, []string{"split-window", "-t", "moomux-mosaic"})
	assertContains(t, fr.calls, []string{"send-keys", "-t", "moomux-mosaic", "tmux attach -t moomux-payment", "Enter"})
}

func TestOpenThreeSessions_PaneTitles(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Tmux: &tmux.Client{Runner: fr}}

	if err := c.Open(makeSessions("auth", "payment", "ui")); err != nil {
		t.Fatal(err)
	}

	assertContains(t, fr.calls, []string{"select-pane", "-t", "moomux-mosaic.0", "-T", "auth"})
	assertContains(t, fr.calls, []string{"select-pane", "-t", "moomux-mosaic.1", "-T", "payment"})
	assertContains(t, fr.calls, []string{"select-pane", "-t", "moomux-mosaic.2", "-T", "ui"})
}

func TestOpenNewWindowFails(t *testing.T) {
	fr := &fakeRunner{failOn: map[string]bool{"new-window -d -n moomux-mosaic": true}}
	c := &Client{Tmux: &tmux.Client{Runner: fr}}

	if err := c.Open(makeSessions("auth")); err == nil {
		t.Fatal("expected error when new-window fails")
	}
}
