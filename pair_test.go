package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/ipc"
)

func TestRenderQRHalfBlocks(t *testing.T) {
	// 2×2 code, top-left and bottom-right black: one text row of "▀▄" inside the quiet zone.
	var b strings.Builder
	renderQR(&b, 2, func(x, y int) bool { return x == y })
	out := b.String()
	if !strings.Contains(out, "▀▄") {
		t.Fatalf("modules: %q", out)
	}
	if !strings.HasPrefix(out, "\x1b[30;107m") {
		t.Fatalf("explicit colors: %q", out)
	}
	if strings.Contains(out, "█") {
		t.Fatalf("no full block in a diagonal: %q", out)
	}
}

// TestRenderQRQuietZoneOnAllSides pins the border the scan was checked
// against: every text row is the same width, and each side carries
// qrQuietZone white modules — never a module of the code itself.
func TestRenderQRQuietZoneOnAllSides(t *testing.T) {
	var b strings.Builder
	renderQR(&b, 2, func(x, y int) bool { return true })
	rows := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	want := (2 + 2*qrQuietZone + 1) / 2
	if len(rows) != want {
		t.Fatalf("%d text rows, want %d:\n%s", len(rows), want, b.String())
	}
	blank := strings.Repeat(" ", 2+2*qrQuietZone)
	for i, row := range rows {
		cells := strings.TrimSuffix(strings.TrimPrefix(row, "\x1b[30;107m"), "\x1b[0m")
		if cells == row {
			t.Fatalf("row %d not wrapped in explicit colors: %q", i, row)
		}
		if got := []rune(cells); len(got) != 2+2*qrQuietZone {
			t.Fatalf("row %d is %d cells wide, want %d: %q", i, len(got), 2+2*qrQuietZone, cells)
		}
		inCode := i*2+1 >= qrQuietZone && i*2 < qrQuietZone+2
		if !inCode && cells != blank {
			t.Fatalf("row %d is quiet zone but has modules: %q", i, cells)
		}
		pad := strings.Repeat(" ", qrQuietZone)
		if !strings.HasPrefix(cells, pad) || !strings.HasSuffix(cells, pad) {
			t.Fatalf("row %d has no side quiet zone: %q", i, cells)
		}
	}
}

func TestPrintPairingSecurityLineOnlyWhenTurnedOn(t *testing.T) {
	p := ipc.Pairing{Link: "moomux://connect?host=100.1.2.3&name=box", Name: "box", Owner: "me@example.com"}
	var off, on strings.Builder
	printPairing(&off, p)
	p.TurnedOn = true
	printPairing(&on, p)
	for _, out := range []string{off.String(), on.String()} {
		if !strings.Contains(out, p.Link) || !strings.Contains(out, "sign in to Tailscale as me@example.com.") {
			t.Fatalf("missing link or owner line:\n%s", out)
		}
	}
	const sec = "Devices on your tailnet signed in as me@example.com can now reach moomux on this computer."
	if strings.Contains(off.String(), sec) || !strings.Contains(on.String(), sec) {
		t.Fatal("security line")
	}
}

// TestPrintPairingLayout pins the order below the code: a blank line, the
// link on a line of its own (so it can be copied whole), then the owner line.
func TestPrintPairingLayout(t *testing.T) {
	p := ipc.Pairing{Link: "moomux://connect?host=100.1.2.3&name=box", Name: "box", Owner: "me@example.com"}
	var b strings.Builder
	if err := printPairing(&b, p); err != nil {
		t.Fatal(err)
	}
	want := "\x1b[0m\n\n" + p.Link + "\n" +
		"Scan with your iPhone's Camera. On the iPhone, sign in to Tailscale as me@example.com.\n"
	if !strings.HasSuffix(b.String(), want) {
		t.Fatalf("tail of output:\n%q\nwant suffix:\n%q", b.String(), want)
	}
}

// A dial failure is the one error the core can't word, because there is no
// core: runPair says so itself, in the contract's copy.
func TestRunPairWithNoCoreSaysSo(t *testing.T) {
	err := runPair([]string{"-socket", filepath.Join(t.TempDir(), "none.sock")})
	const want = "No moomux core is running. Start it (`brew services start moomux`, or `moomux serve`), then try again."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

// shortSock is a unix socket path for a test core. Short deliberately: the
// path is capped around 104 bytes, which t.TempDir() can run past.
func shortSock(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

// Every other error is the core's own sentence and goes out as it came in:
// unwrapped, unprefixed, unreworded.
func TestRunPairShowsTheCoresErrorVerbatim(t *testing.T) {
	const copy = "Tailscale is off on this computer. Open it, sign in, and try again."
	sock := shortSock(t)
	ln, err := ipc.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := &ipc.Server{Pair: func() (ipc.Pairing, error) { return ipc.Pairing{}, errors.New(copy) }}
	go srv.Serve(ln)

	err = runPair([]string{"-socket", sock})
	if err == nil || err.Error() != copy {
		t.Fatalf("err = %v, want %q", err, copy)
	}
}

// A core from before Pair — `brew upgrade` without a service restart —
// answers what its dispatch answers for any method it doesn't know. It
// can't word anything about pairing, so runPair does, in the contract's
// CLI-only copy. Faked rather than an ipc.Server, which knows Pair.
func TestRunPairOnAnOlderCoreSaysRestartIt(t *testing.T) {
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = bufio.NewReader(c).ReadString('\n')
		_, _ = io.WriteString(c, `{"err":"unknown method \"Pair\""}`+"\n")
	}()

	err = runPair([]string{"-socket", sock})
	const want = "The running moomux core is older than this command. Restart it (`brew services restart moomux`), then try again."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}
