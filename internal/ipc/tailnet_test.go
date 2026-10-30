package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFindTailscale(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	noexec := filepath.Join(dir, "noexec")
	bin := filepath.Join(dir, "tailscale")
	if err := os.WriteFile(noexec, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := findTailscale([]string{filepath.Join(dir, "missing"), noexec, bin}); got != bin {
		t.Errorf("fallback: got %q, want %q", got, bin)
	}
	if got := findTailscale([]string{noexec}); got != "tailscale" {
		t.Errorf("none found: got %q, want bare name", got)
	}

	t.Setenv("PATH", dir)
	if got := findTailscale(nil); got != bin {
		t.Errorf("PATH first: got %q, want %q", got, bin)
	}
}

// fakeTailscale answers the three queries tailnetSelf makes, for a node with
// the given address, owner login and host name.
func fakeTailscale(ip, login, host string) func(args ...string) (string, error) {
	return func(args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "ip -4":
			return ip + "\n", nil
		case "whois --json " + ip:
			return fmt.Sprintf(`{"UserProfile":{"LoginName":%q}}`, login), nil
		case "status --json":
			return fmt.Sprintf(`{"Self":{"HostName":%q}}`, host), nil
		}
		return "", fmt.Errorf("unexpected tailscale %v", args)
	}
}

func TestPairLink(t *testing.T) {
	got := PairLink("100.71.5.0", "ericks-macbook-pro")
	want := "moomux://connect?host=100.71.5.0&name=ericks-macbook-pro"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := PairLink("100.1.2.3", "Erick's Mac"); strings.Contains(got, "+") || !strings.Contains(got, "Erick%27s%20Mac") {
		t.Fatalf("space must be %%20, not +: %q", got)
	}
}

func TestTailnetSelfWording(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(args ...string) (string, error)
		want error
	}{
		{"no cli", func(...string) (string, error) { return "", exec.ErrNotFound }, errNoTailscale},
		{"stopped", func(a ...string) (string, error) { return "", errors.New("exit status 1") }, errTailscaleOff},
		{"tagged", fakeTailscale("100.1.2.3", "tagged-devices", "box"), errTaggedNode},
	} {
		old := tailscaleRunFn
		tailscaleRunFn = tc.run
		_, err := tailnetSelf()
		tailscaleRunFn = old
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
		// The text crosses the wire and is shown verbatim, so no wrapping.
		if err != tc.want {
			t.Errorf("%s: error must be returned bare, got %v", tc.name, err)
		}
	}
	old := tailscaleRunFn
	defer func() { tailscaleRunFn = old }()
	tailscaleRunFn = fakeTailscale("100.1.2.3", "me@example.com", "ericks-macbook-pro")
	self, err := tailnetSelf()
	if err != nil || self != (TailnetSelf{"100.1.2.3", "me@example.com", "ericks-macbook-pro"}) {
		t.Fatalf("got %+v, %v", self, err)
	}
}

// withSelf answers tailnetSelfFn with self and err for the length of the
// test, and puts pairBindWait back afterwards for the tests that shorten it.
func withSelf(t *testing.T, self TailnetSelf, err error) {
	t.Helper()
	oldSelf, oldWait := tailnetSelfFn, pairBindWait
	tailnetSelfFn = func() (TailnetSelf, error) { return self, err }
	t.Cleanup(func() { tailnetSelfFn, pairBindWait = oldSelf, oldWait })
}

// boundOn is a Tailnet listen func that binds at once: a spare port on ip,
// admitting owner — a tailnet that is up, without being on one. Bound here
// rather than inside the func, which runs on Start's goroutine where a
// t.Fatal can't stop the test.
func boundOn(t *testing.T, ip, owner string) func(onErr func(error)) net.Listener {
	t.Helper()
	inner, err := net.Listen("tcp", ip+":0")
	if err != nil {
		t.Fatal(err)
	}
	ln := newTailnetListener(inner, owner)
	t.Cleanup(func() { ln.Close() })
	return func(func(error)) net.Listener { return ln }
}

func TestPairSavesNothingWhenTailscaleIsOff(t *testing.T) {
	withSelf(t, TailnetSelf{}, errTailscaleOff)
	called := false
	_, err := (&Tailnet{Srv: &Server{}, listen: boundOn(t, "127.0.0.1", "me@example.com")}).
		Pair(func() (bool, error) { called = true; return true, nil })
	if !errors.Is(err, errTailscaleOff) || called {
		t.Fatalf("err %v, enable called %v", err, called)
	}
}

func TestPairTurnsOnOnceAndIsIdempotent(t *testing.T) {
	withSelf(t, TailnetSelf{"127.0.0.1", "me@example.com", "box"}, nil)
	tn := &Tailnet{Srv: &Server{}, listen: boundOn(t, "127.0.0.1", "me@example.com")}
	first, err := tn.Pair(func() (bool, error) { return true, nil })
	if err != nil || !first.TurnedOn || first.Link != PairLink("127.0.0.1", "box") || first.Owner != "me@example.com" {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := tn.Pair(func() (bool, error) { return false, nil })
	if err != nil || second.TurnedOn {
		t.Fatalf("second: %+v %v", second, err)
	}
}

func TestPairReportsAStaleListener(t *testing.T) {
	withSelf(t, TailnetSelf{"127.0.0.1", "other@example.com", "box"}, nil)
	called := false
	_, err := (&Tailnet{Srv: &Server{}, listen: boundOn(t, "127.0.0.1", "me@example.com")}).
		Pair(func() (bool, error) { called = true; return false, nil })
	if !errors.Is(err, errTailnetChanged) || called {
		t.Fatalf("err %v, enable called %v", err, called)
	}
}

// An address that moved under a running listener is as stale as a changed
// account: the listener is still bound to the old one, which no phone will
// be told about.
func TestPairReportsAMovedAddress(t *testing.T) {
	withSelf(t, TailnetSelf{"127.0.0.2", "me@example.com", "box"}, nil)
	called := false
	_, err := (&Tailnet{Srv: &Server{}, listen: boundOn(t, "127.0.0.1", "me@example.com")}).
		Pair(func() (bool, error) { called = true; return false, nil })
	if !errors.Is(err, errTailnetChanged) || called {
		t.Fatalf("err %v, enable called %v", err, called)
	}
}

func TestPairGivesUpWhenTheListenerNeverBinds(t *testing.T) {
	withSelf(t, TailnetSelf{"127.0.0.1", "me@example.com", "box"}, nil)
	pairBindWait = 50 * time.Millisecond
	never := func(onErr func(error)) net.Listener { onErr(errors.New("address already in use")); select {} }
	called := false
	_, err := (&Tailnet{Srv: &Server{}, listen: never}).Pair(func() (bool, error) { called = true; return true, nil })
	if called {
		t.Fatal("enable called on a failed Pair")
	}
	if err == nil || !strings.Contains(err.Error(), "couldn't listen on Tailscale yet (address already in use)") {
		t.Fatalf("got %v", err)
	}
}

// A listener that hasn't failed yet either — a `tailscale` subprocess still
// inside its timeout — has no bind error to show, so the wait is what's
// reported instead of an empty "()".
func TestPairGivesUpWithNoBindErrorHeard(t *testing.T) {
	withSelf(t, TailnetSelf{"127.0.0.1", "me@example.com", "box"}, nil)
	pairBindWait = 50 * time.Millisecond
	silent := func(func(error)) net.Listener { select {} }
	called := false
	_, err := (&Tailnet{Srv: &Server{}, listen: silent}).Pair(func() (bool, error) { called = true; return true, nil })
	const want = "moomux couldn't listen on Tailscale yet (no answer after 50ms). Try again in a moment."
	if called || err == nil || err.Error() != want {
		t.Fatalf("err %v, enable called %v; want %q", err, called, want)
	}
}

// Two front ends may pair at once, and a core started with the flag on has
// already called Start. Every call must get its answer from the one
// listener: a second bind would fail on the port the first one holds.
func TestPairConcurrentlyStartsOneListener(t *testing.T) {
	withSelf(t, TailnetSelf{"127.0.0.1", "me@example.com", "box"}, nil)
	bound := boundOn(t, "127.0.0.1", "me@example.com")
	var listens atomic.Int32
	tn := &Tailnet{Srv: &Server{}, listen: func(onErr func(error)) net.Listener {
		listens.Add(1)
		return bound(onErr)
	}}
	var wg sync.WaitGroup
	wg.Go(tn.Start)
	for range 8 {
		wg.Go(func() {
			if _, err := tn.Pair(func() (bool, error) { return false, nil }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := listens.Load(); n != 1 {
		t.Fatalf("listen called %d times, want 1", n)
	}
}
