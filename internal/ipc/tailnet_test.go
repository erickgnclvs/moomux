package ipc

import (
	"os"
	"path/filepath"
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
