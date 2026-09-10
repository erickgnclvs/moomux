package terminal

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFallbackReturnsAttachHint(t *testing.T) {
	f := &fallbackOpener{}
	hint, err := f.OpenSession("moomux-foo", "feat/bar")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hint, `tmux attach -t '=moomux-foo'`) {
		t.Fatalf("expected attach command in hint, got: %s", hint)
	}
}

func TestFallbackUsesProcessTreeInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	withAttachedClient(t, []string{"zsh", "login", "iTerm2"})

	got := fallback()
	if _, ok := got.(*itermClient); !ok {
		t.Fatalf("expected *itermClient, got %T", got)
	}
}

func TestFallbackReturnsFallbackOpenerWhenProcessTreeUnknownInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	withAttachedClient(t, []string{"zsh", "sshd"})

	if got, want := fmt.Sprintf("%T", fallback()), fmt.Sprintf("%T", platformFallback()); got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestFallbackFuncReturnsFallbackOutsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	if got, want := fmt.Sprintf("%T", fallback()), fmt.Sprintf("%T", platformFallback()); got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

// The macOS fallback has to actually launch something: a launchd-started
// core has none of the env vars Detect probes, so before this every open
// from a socket client (`moomux ui -socket`) returned a hint and no window.
func TestDetectDaemonEnvOnDarwinOpensSomething(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only fallback")
	}
	clearLinuxTerminalEnv(t)
	if _, ok := Detect().(*commandFileOpener); !ok {
		t.Fatalf("expected *commandFileOpener with no terminal env, got %T", Detect())
	}
}

// The generated script must attach to the exact session, quoted, and clean
// itself up — it's the whole payload, and `open` gives no way to see it fail.
func TestCommandFileScript(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only fallback")
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	fakeOpen(t, dir)

	if hint, err := new(commandFileOpener).OpenSession("moomux-x-1", "x"); err != nil || hint != "" {
		t.Fatalf("OpenSession = %q, %v", hint, err)
	}
	entries, _ := os.ReadDir(dir)
	var script string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".command") {
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			script = string(b)
		}
	}
	if !strings.Contains(script, "exec tmux attach -t '=moomux-x-1'") {
		t.Fatalf("script does not attach to the exact session:\n%s", script)
	}
	if !strings.Contains(script, "rm -f ") {
		t.Fatalf("script does not remove itself:\n%s", script)
	}
}

// fakeOpen puts a no-op `open` first on PATH so the test never launches a
// real terminal, and leaves the script behind for inspection.
func fakeOpen(t *testing.T, dir string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "open"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}
