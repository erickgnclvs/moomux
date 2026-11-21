package ipc

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erickgnclvs/moomux/internal/sessionview"
)

// whois admits this node's own address as the owner, so a local process that
// dials the tailnet port must be refused by address, not by login.
func TestTailnetListenerRefusesItsOwnAddress(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	restore := tailscaleLoginFn
	tailscaleLoginFn = func(string) (string, error) { return "me@example.com", nil }
	defer func() { tailscaleLoginFn = restore }()

	ln := newTailnetListener(inner, "me@example.com")
	ln.self = "127.0.0.1"
	go func() {
		for {
			if _, err := ln.Accept(); err != nil {
				return
			}
			t.Error("own address was admitted")
		}
	}()
	defer ln.Close()

	c, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read = %v, want the connection closed (EOF)", err)
	}
}

// A peer that connects and says nothing must not hold its goroutine and
// descriptor forever.
func TestHandleDropsAPeerThatSendsNothing(t *testing.T) {
	srv, cli := net.Pipe()
	defer cli.Close()
	s := &Server{ioTimeout: 100 * time.Millisecond}
	done := make(chan struct{})
	go func() { s.handle(srv); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handle still waiting on a silent peer")
	}
}

// A symlink or loose directory planted at /tmp/moomux-images must not receive
// an upload.
func TestSaveFileRefusesAPlantedDirectory(t *testing.T) {
	tmp := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(tmp, "moomux-images")); err != nil {
		t.Fatal(err)
	}
	if _, err := saveFile(tmp, "a.png", []byte("x")); err == nil {
		t.Fatal("saveFile wrote through a symlinked directory")
	}
	if ents, _ := os.ReadDir(elsewhere); len(ents) != 0 {
		t.Fatalf("file landed in the symlink target: %v", ents)
	}
}

func TestSaveFileTightensItsOwnLooseDirectory(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "moomux-images")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := saveFile(tmp, "a.png", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v, want 0700", fi.Mode().Perm())
	}
}

// The limit is on silence: a request that trickles in over longer than the
// timeout, but never stalls for that long, must still be served.
func TestHandleAllowsASlowButSteadyRequest(t *testing.T) {
	srv, cli := net.Pipe()
	defer cli.Close()
	s := &Server{ioTimeout: 300 * time.Millisecond}
	go s.handle(srv)
	req := `{"method":"Nope","args":{}}`
	for i := 0; i < len(req); i += 5 {
		end := min(i+5, len(req))
		if _, err := io.WriteString(cli, req[i:end]); err != nil {
			t.Fatalf("write cut off at byte %d: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond) // total ~600ms, over the 300ms timeout
	}
	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	b, err := io.ReadAll(cli)
	if err != nil || !strings.Contains(string(b), "unknown") {
		t.Fatalf("got %q, %v; want an unknown-method answer", b, err)
	}
}

// A Watch client that connects and never reads would block stream's write
// forever, holding its goroutine and descriptor. The write deadline ends it.
func TestStreamDropsAClientThatStopsReading(t *testing.T) {
	srv, cli := net.Pipe() // unbuffered: a write blocks until cli reads
	defer cli.Close()
	snap := sessionview.Snapshot{Views: map[string]sessionview.View{"moomux:a": {ID: "moomux:a"}}, PollTime: time.Now()}
	s := &Server{Source: &blockingWatcher{snap: snap}, ioTimeout: 200 * time.Millisecond}
	pr, pw := io.Pipe() // the client's nudge half: silent, but open
	defer pw.Close()
	done := make(chan struct{})
	go func() { s.stream(srv, pr); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream still blocked writing to a client that never reads")
	}
}
