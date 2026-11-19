package tmux

import (
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/erickgnclvs/moomux/internal/watcher"
)

func TestTerminalReportsDisplayedPrompts(t *testing.T) {
	osc := func(s string) string { return "\x1b]7501;" + s + "\x1b\\" }
	stream := strings.Join([]string{
		osc("state=clear"), osc("state=working:app=codex"),
		"Should I continue?", "\x1b]0;approval requested\x07",
		osc("state=blocked:app=other:kind=permission"), osc("state=working:app=codex"),
		osc("state=blocked:app=codex:kind=permission"), osc("state=working:app=codex"),
		osc("state=blocked:app=codex:kind=question"), osc("state=idle:app=codex"),
		osc("state=blocked:app=codex:kind=auth"), osc("state=clear"),
		osc("state=done:app=codex"),
	}, "")
	var states []watcher.State
	err := readCodexTerminal(iotest.OneByteReader(strings.NewReader(stream)), func(st watcher.State) error {
		states = append(states, st)
		return nil
	})
	want := []watcher.State{watcher.Working, watcher.NeedsInput, watcher.Working, watcher.NeedsInput, watcher.Done, watcher.NeedsInput, watcher.Done}
	if err != nil || !slices.Equal(states, want) {
		t.Fatalf("read terminal: %v, %v; want %v", states, err, want)
	}
}

func TestSessionStatesDistinguishesFailedReadFromNoServer(t *testing.T) {
	const cmd = "-u list-panes -a -F #{session_name}\t#{pane_pipe_pid}\t#{?pane_pipe,#{@moomux_codex_state},}"
	// When the server has gone away, callers must drop parked sessions.
	// Losing a single poll of a live server should preserve their state.
	c := &Client{Runner: &fakeRunner{out: map[string]string{cmd: "no server running on /tmp/test"}, failOn: map[string]bool{cmd: true}}}
	if got := c.SessionStates(); got == nil || len(got) != 0 {
		t.Fatalf("no server: got %v, want an empty snapshot", got)
	}
	c.Runner = plainErrRunner{}
	if got := c.SessionStates(); got != nil {
		t.Fatalf("transient failure: got %v, want nil", got)
	}
}
