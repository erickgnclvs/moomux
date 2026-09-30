package tmux

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// batchKey is the single tmux invocation CapturePanes makes for names.
func batchKey(names ...string) string {
	var args []string
	for i, n := range names {
		args = append(args, "display-message", "-p", captureMarker+string(rune('0'+i)), ";",
			"capture-pane", "-p", "-t", firstWindow(n), ";")
	}
	return strings.Join(append(args, "display-message", "-p", captureMarker+"end"), " ")
}

func TestCapturePanesBatchesOneInvocation(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		batchKey("a", "b"): captureMarker + "0\nscreen a\nrow 2\n" +
			captureMarker + "1\nscreen b\n" + captureMarker + "end\n",
	}}
	c := &Client{Runner: fr}
	got := c.CapturePanes([]string{"a", "b"})
	want := map[string]string{"a": "screen a\nrow 2", "b": "screen b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	if n := capturingCalls(fr); n != 1 {
		t.Fatalf("want one capture invocation for the whole set, got %d: %v", n, fr.calls)
	}
}

// tmux abandons the rest of a command sequence at the first error — and
// exits 0 doing it — so everything after a session that died a moment ago
// comes back missing. Without the per-session fallback those tiles would
// freeze; without the closing marker the half-written last section would be
// accepted as a screen.
func TestCapturePanesFallsBackAfterAnAbandonedSequence(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		// "b" died: its capture failed, so tmux printed nothing further and
		// the "c" section never opened.
		batchKey("a", "b", "c"): captureMarker + "0\nscreen a\n" +
			captureMarker + "1\ncan't find pane: b\n",
		"capture-pane -p -t =c:^": "screen c\n",
	}, failOn: map[string]bool{"capture-pane -p -t =b:^": true}}
	c := &Client{Runner: fr}
	got := c.CapturePanes([]string{"a", "b", "c"})
	// "c" came back through the per-session fallback and "a" through the
	// batch, and the two must agree down to the trailing newline — a tile
	// that gains or loses a blank row depending on which path served it
	// flickers whenever an earlier session momentarily fails.
	want := map[string]string{"a": "screen a", "c": "screen c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v (a dead session must not freeze the ones after it)", got, want)
	}
}

func TestCapturePanesEmpty(t *testing.T) {
	fr := &fakeRunner{}
	if got := (&Client{Runner: fr}).CapturePanes(nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("nothing to capture should run no tmux: %v", fr.calls)
	}
}

// reviewLookup is the query ReviewWindow makes to find its own window.
const reviewLookup = "list-windows -t =moomux-a -F #{window_id} #{@moomux_review}"

func TestReviewWindowReusesAnExistingWindow(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		reviewLookup: "@1 \n@4 1\n",
	}}
	c := &Client{Runner: fr}
	if err := c.ReviewWindow("moomux-a", "/wt", "diff"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"list-windows", "-t", "=moomux-a", "-F", "#{window_id} #{@moomux_review}"},
		{"respawn-window", "-k", "-t", "@4", "-c", "/wt", "diff"},
		{"select-window", "-t", "@4"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

// respawn-window and not kill-then-create: killing the last window of a
// session kills the session. new-window is only for the first review.
func TestReviewWindowCreatesWhenThereIsNothingToReuse(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		"new-window -P -F #{window_id} -t =moomux-a -c /wt -n review diff": "@7\n",
	}}
	c := &Client{Runner: fr}
	if err := c.ReviewWindow("moomux-a", "/wt", "diff"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"list-windows", "-t", "=moomux-a", "-F", "#{window_id} #{@moomux_review}"},
		{"new-window", "-P", "-F", "#{window_id}", "-t", "=moomux-a", "-c", "/wt", "-n", "review", "diff"},
		// Marked, so the next Review reuses this window rather than
		// stacking a second one beside it.
		{"set-window-option", "-t", "@7", "@moomux_review", "1"},
	}
	if !reflect.DeepEqual(fr.calls, want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

// respawn-window -k kills whatever the target window is running. A window a
// user's own layout called "review" is not moomux's to blow away, and the
// name is plausible enough that addressing it by name was a live hazard:
// only the marker makes a window reusable.
func TestReviewWindowLeavesAnUnmarkedReviewWindowAlone(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		reviewLookup: "@1 \n@2 \n", // a window named "review", but not ours
	}}
	c := &Client{Runner: fr}
	if err := c.ReviewWindow("moomux-a", "/wt", "diff"); err != nil {
		t.Fatal(err)
	}
	for _, call := range fr.calls {
		if call[0] == "respawn-window" {
			t.Fatalf("respawned a window moomux does not own: %v", call)
		}
	}
}

// A pane's unused rows are blank, and capture-pane prints them like any
// other row. The batch path used to drop the last one as framing, so a tile
// gained or lost a row purely from which path served it — the flicker the
// fallback's TrimSuffix exists to prevent.
func TestCapturePanesKeepsATrailingBlankRow(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		batchKey("a", "b"): captureMarker + "0\nhello\n\n\n" +
			captureMarker + "1\ncan't find pane: b\n",
		"capture-pane -p -t =b:^": "hello\n\n\n",
	}}
	c := &Client{Runner: fr}
	got := c.CapturePanes([]string{"a", "b"})
	if got["a"] != got["b"] {
		t.Fatalf("batch %q and fallback %q disagree on the blank rows", got["a"], got["b"])
	}
	if want := "hello\n\n"; got["a"] != want {
		t.Fatalf("got %q, want %q", got["a"], want)
	}
}

// Every other command here targets the session's *first* window, not its
// current one: ReviewWindow selects a second window, and a capture or a
// status-title rename that follows the user into it reads and renames the
// wrong pane.
func TestWindowCommandsTargetTheAgentWindowNotTheCurrentOne(t *testing.T) {
	if got := firstWindow("moomux-a"); got != "=moomux-a:^" {
		t.Fatalf("firstWindow = %q, want the session's first window", got)
	}
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	_ = c.SetWindowName("moomux-a", "● a")
	_, _ = c.CapturePane("moomux-a")
	if err := c.ReviewWindow("moomux-a", "/wt", "diff"); err != nil {
		t.Fatal(err)
	}
	for _, call := range fr.calls {
		for _, arg := range call {
			// The review window is addressed by name; nothing else may be
			// addressed by "the current window".
			if arg == "=moomux-a:" {
				t.Fatalf("%v targets the current window", call)
			}
		}
	}
}

// capturingCalls counts the tmux invocations that actually captured a pane,
// as opposed to the one-off window lookup agentWindow caches per session.
func capturingCalls(fr *fakeRunner) int {
	n := 0
	for _, call := range fr.calls {
		for _, arg := range call {
			if arg == "capture-pane" {
				n++
				break
			}
		}
	}
	return n
}

// A layout can put the agent leaf in a window other than the first, and the
// window it lands in is marked at creation. Without reading that mark back,
// every capture, rename and cwd check here targets window one — for
// PaneCwd, whose answer decides whether App.EnsureTmux kills a live session
// and recreates it.
func TestAgentWindowFollowsTheMarkedWindow(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		"list-windows -t =moomux-a -F #{window_id} #{@moomux_agent}": "@0 \n@1 1\n",
	}}
	c := &Client{Runner: fr}
	if _, err := c.CapturePane("moomux-a"); err != nil {
		t.Fatal(err)
	}
	want := []string{"capture-pane", "-p", "-t", "@1"}
	if !reflect.DeepEqual(fr.calls[len(fr.calls)-1], want) {
		t.Fatalf("captured %v, want the agent's window %v", fr.calls[len(fr.calls)-1], want)
	}
	// Resolved once: these run on every poll tick.
	_, _ = c.CapturePane("moomux-a")
	for _, call := range fr.calls[1:] {
		if call[0] == "list-windows" {
			t.Fatalf("agent window looked up twice: %v", fr.calls)
		}
	}
}

// tmux exits non-zero when a command in the sequence fails, and used to be
// believed not to. Discarding the whole batch on that error puts every
// session on the per-session fallback — N+1 forks a tick — and a parked
// session keeps it there forever.
func TestCapturePanesKeepsTheGoodSectionsWhenTheBatchErrors(t *testing.T) {
	key := batchKey("a", "b")
	fr := &fakeRunner{
		out: map[string]string{
			key: captureMarker + "0\nscreen a\n" + captureMarker + "1\ncan't find pane: b\n",
		},
		failOn: map[string]bool{key: true, "capture-pane -p -t =b:^": true},
	}
	c := &Client{Runner: fr}
	got := c.CapturePanes([]string{"a", "b"})
	if want := (map[string]string{"a": "screen a"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if n := capturingCalls(fr); n != 2 {
		t.Fatalf("want the batch plus one fallback, got %d: %v", n, fr.calls)
	}
}

// A dead session early in the batch used to send every session behind it to
// the per-session path — every tick, for as long as it stayed parked, which
// is the N+1 forks the batching exists to avoid.
func TestCapturePanesRebatchesWhatTheFirstPassMissed(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{
		// "dead" is first, so tmux abandons the sequence before "b" and "c"
		// ever open a section.
		batchKey("dead", "b", "c"): captureMarker + "0\ncan't find pane: dead\n",
		batchKey("b", "c"): captureMarker + "0\nscreen b\n" +
			captureMarker + "1\nscreen c\n" + captureMarker + "end\n",
	}, failOn: map[string]bool{"capture-pane -p -t =dead:^": true}}
	c := &Client{Runner: fr}
	got := c.CapturePanes([]string{"dead", "b", "c"})
	want := map[string]string{"b": "screen b", "c": "screen c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	// The two batches plus one fallback fork for "dead" itself.
	if n := capturingCalls(fr); n != 3 {
		t.Fatalf("want the live sessions recovered by one re-batch, got %d invocations: %v", n, fr.calls)
	}
}

// tmux resolves its server socket from $TMUX when -L/-S aren't given.
// AttachCmd has to strip $TMUX (tmux refuses to nest), so without -S an
// attach from a core running inside `tmux -L foo` goes to the *default*
// server and reports "no sessions" — while every Runner call in this
// package, which does inherit $TMUX, still talks to foo.
func TestAttachCmdKeepsTheServerSocket(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/foo,4321,0")
	args := AttachCmd("moomux-a").Args
	want := []string{"tmux", "-u", "-S", "/private/tmp/tmux-501/foo", "attach", "-t", "=moomux-a"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for _, kv := range AttachCmd("moomux-a").Env {
		if strings.HasPrefix(kv, "TMUX=") {
			t.Fatalf("TMUX must not reach the child: %q", kv)
		}
	}
}

// -u: a core run by launchd has no LANG, so without it tmux decides the
// client isn't UTF-8 and draws every non-ASCII glyph (❯, ⏵, ·, the Claude
// logo) as "_" on the phone. The -u in both wants above is that regression.
func TestAttachCmdOutsideTmuxTargetsTheDefaultServer(t *testing.T) {
	t.Setenv("TMUX", "")
	args := AttachCmd("moomux-a").Args
	want := []string{"tmux", "-u", "attach", "-t", "=moomux-a"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

// Nothing evicts the cache entry for a session that died outside
// KillSession, and CapturePanes inserts one per name it is asked about, so
// an unbounded map is a leak in a core that runs for weeks.
func TestAgentWindowCacheIsBounded(t *testing.T) {
	c := &Client{Runner: &fakeRunner{}}
	c.mu.Lock()
	for i := range agentWinsMax * 3 {
		c.cacheAgentWindowLocked(fmt.Sprintf("moomux-%d", i), "@1")
	}
	n := len(c.agentWins)
	c.mu.Unlock()
	if n > agentWinsMax {
		t.Fatalf("cache grew to %d entries, want at most %d", n, agentWinsMax)
	}
}
