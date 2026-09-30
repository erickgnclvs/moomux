//go:build e2e

package e2e

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erickgnclvs/moomux/internal/app"
	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/ipc"
	"github.com/erickgnclvs/moomux/internal/session"
)

// newCoreSession registers a real session against a real, live tmux session
// whose pane is a plain shell.
//
// Deliberately not CreateSession: that launches the project's agent in the
// pane, and an agent CLI owns the keyboard — its first-run trust prompt
// swallows anything typed at it, which is exactly what these tests are
// trying to observe arriving.
func newCoreSession(t *testing.T, name string) (*app.App, session.Session) {
	t.Helper()
	repo := initRepo(t, "main")
	a := newTestApp(t)
	if _, err := a.AddProject("demo", config.Project{Repo: repo, BaseBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	s := session.Session{
		ID: "demo:" + name, Project: "demo", Name: name,
		Branch: "main", WorktreePath: repo,
		TmuxSession: app.TmuxSessionName("demo:"+name, name),
	}
	if err := a.Tmux.NewSession(s.TmuxSession, repo, "", name); err != nil {
		t.Fatalf("tmux NewSession: %v", err)
	}
	if err := a.Store.Put(s); err != nil {
		t.Fatalf("store put: %v", err)
	}
	return a, s
}

// serveOnSocket runs the real ipc.Server over a real unix socket and hands
// back a client. The socket lives under a short path of its own: a unix
// socket path is capped around 100 bytes, and t.TempDir() names it after the
// test, which on macOS already spends most of the budget.
func serveOnSocket(t *testing.T, a *app.App) *ipc.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "mx")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s.sock")
	ln, err := ipc.Listen(sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close(); os.RemoveAll(dir) })
	go func() { _ = (&ipc.Server{Backend: a, Config: a.ConfigSnapshot}).Serve(ln) }()
	return &ipc.Client{Socket: sock}
}

// Capture is one of the three things the Mac app used to do by shelling out
// to tmux itself, and the one a phone cannot do at all. It has to come back
// keyed by session id, with the pane's real text in it.
func TestCaptureOverTheSocket(t *testing.T) {
	a, s := newCoreSession(t, "cap")
	c := serveOnSocket(t, a)

	marker := "moomux-e2e-capture-marker"
	if err := a.Tmux.PasteText(s.TmuxSession, "echo "+marker); err != nil {
		t.Fatalf("paste: %v", err)
	}
	if err := a.Tmux.PressEnter(s.TmuxSession); err != nil {
		t.Fatalf("enter: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		screens := c.Capture([]string{s.ID})
		if strings.Contains(screens[s.ID], marker) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("marker never appeared in the captured pane: %q", screens[s.ID])
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Review opens a tmux window named "review" and reuses it on a second call
// rather than stacking a second window nothing can tell from the first.
func TestReviewOverTheSocketReusesItsWindow(t *testing.T) {
	a, s := newCoreSession(t, "rev")
	c := serveOnSocket(t, a)

	for i := range 2 {
		if _, err := c.Review(s.ID); err != nil {
			t.Fatalf("Review %d: %v", i, err)
		}
		if got := tmuxWindowNames(t, s.TmuxSession); strings.Count(got, "review") != 1 {
			t.Fatalf("after %d review(s), windows = %q", i+1, got)
		}
	}
}

// Attach is the real work: after the request line the connection *is* the
// pty. So the test is exactly that — read the screen tmux draws, write a
// command into it, and see the result come back as bytes.
func TestAttachIsAPtyOverTheWire(t *testing.T) {
	a, s := newCoreSession(t, "att")
	c := serveOnSocket(t, a)

	// A settled baseline, taken before anything attaches: a detached pane
	// may not have drawn its prompt yet, and this has to be measured on the
	// far side of that first paint to mean anything.
	before := settledPaneLines(t, a, s.TmuxSession)

	att, err := c.Attach(s.ID, 100, 40)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer att.Close()

	screen := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, att)
		screen <- buf.Bytes()
	}()

	// Nothing may have been typed on our behalf. The request line's
	// terminating newline must not reach the pty: a shell answers a bare
	// Enter with a fresh prompt, so a stray one shows up as an extra line —
	// and in an agent pane it would submit whatever was sitting there.
	if after := settledPaneLines(t, a, s.TmuxSession); after != before {
		t.Fatalf("attaching typed something into the pane: %d lines before, %d after", before, after)
	}

	marker := "moomux-e2e-attach-marker"
	// Straight into the pty, as a keyboard would: this is the half that
	// proves the connection is bidirectional.
	if _, err := att.Write([]byte("echo " + marker + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		// The pane itself is the durable record of what the pty received,
		// and it is readable without racing the stream of draw bytes.
		out, _ := a.Tmux.CapturePane(s.TmuxSession)
		if strings.Contains(out, marker) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing typed over the attach reached the pane:\n%s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// The window took the attached client's size, which is how cols/rows on
	// the request reach pty.Setsize. (One client, so this holds under
	// tmux's default window-size too.)
	if got := tmuxWindowSize(t, s.TmuxSession); !strings.HasPrefix(got, "100x") {
		t.Fatalf("window size = %q, want the attached client's 100 columns", got)
	}

	// A resize lands on the live pty by token, without a reattach.
	if att.Token == "" {
		t.Fatal("Attach returned no resize token")
	}
	if err := c.ResizeAttach(att.Token, 70, 30); err != nil {
		t.Fatalf("ResizeAttach: %v", err)
	}
	waitFor(t, "window to take the resized 70 columns", func() bool {
		return strings.HasPrefix(tmuxWindowSize(t, s.TmuxSession), "70x")
	})

	// Closing the socket is the detach: the copy above must end, and tmux
	// must lose the client without losing the session.
	att.Close()
	select {
	case drawn := <-screen:
		if len(drawn) == 0 {
			t.Fatal("the attach never drew anything")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("closing the connection did not end the attach")
	}
	if !tmuxHasSession(s.TmuxSession) {
		t.Fatal("detaching killed the session")
	}
	// The token dies with its connection — once the core has noticed the
	// close, which the client's own read ending doesn't wait for.
	waitFor(t, "the closed attach's token to be forgotten", func() bool {
		err := c.ResizeAttach(att.Token, 80, 24)
		return err != nil && err.Error() == "unknown attach"
	})
}

// A second attach is a second client, not a replacement: the phone attaching
// must not detach the Mac app's pane (or anyone else's).
func TestAttachDoesNotDetachOtherClients(t *testing.T) {
	a, s := newCoreSession(t, "two")
	c := serveOnSocket(t, a)
	for i := range 2 {
		att, err := c.Attach(s.ID, 100, 40)
		if err != nil {
			t.Fatalf("Attach %d: %v", i, err)
		}
		defer att.Close()
		go func() { _, _ = io.Copy(io.Discard, att) }()
	}
	waitFor(t, "both attaches to be tmux clients", func() bool {
		out, _ := exec.Command("tmux", "list-clients", "-t", s.TmuxSession).Output()
		return strings.Count(string(out), "\n") == 2
	})
	// ...and still both a moment later, rather than the first being kicked
	// once the second finished attaching.
	time.Sleep(500 * time.Millisecond)
	out, _ := exec.Command("tmux", "list-clients", "-t", s.TmuxSession).Output()
	if n := strings.Count(string(out), "\n"); n != 2 {
		t.Fatalf("%d tmux clients after two attaches, want 2:\n%s", n, out)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// settledPaneLines waits for the pane's line count to stop changing, then
// returns it — a pane repaints on its own after creation and after a client
// attaches, and neither is something being typed.
func settledPaneLines(t *testing.T, a *app.App, sess string) int {
	t.Helper()
	last, stable := -1, 0
	for range 60 {
		time.Sleep(250 * time.Millisecond)
		n := paneLines(t, a, sess)
		if n == last && n > 0 {
			if stable++; stable == 3 {
				return n
			}
			continue
		}
		last, stable = n, 0
	}
	t.Fatalf("pane never settled (last count %d)", last)
	return 0
}

// paneLines counts the non-blank rows of a session's active pane.
func paneLines(t *testing.T, a *app.App, sess string) int {
	t.Helper()
	out, err := a.Tmux.CapturePane(sess)
	if err != nil {
		t.Fatalf("capture-pane: %v", err)
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func tmuxWindowNames(t *testing.T, sess string) string {
	t.Helper()
	out, err := exec.Command("tmux", "list-windows", "-t", sess, "-F", "#{window_name}").Output()
	if err != nil {
		t.Fatalf("tmux list-windows: %v", err)
	}
	return string(out)
}

func tmuxWindowSize(t *testing.T, sess string) string {
	t.Helper()
	out, err := exec.Command("tmux", "list-windows", "-t", sess, "-F", "#{window_width}x#{window_height}").Output()
	if err != nil {
		t.Fatalf("tmux list-windows: %v", err)
	}
	return strings.TrimSpace(strings.Split(strings.TrimSpace(string(out)), "\n")[0])
}
