package ipc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
