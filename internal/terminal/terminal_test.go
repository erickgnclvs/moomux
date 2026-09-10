package terminal

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectReturnsITermForITermApp(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	got := Detect()
	if _, ok := got.(*itermClient); !ok {
		t.Fatalf("expected *itermClient, got %T", got)
	}
}

func TestDetectReturnsWindowOpenerForCmux(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("__CFBundleIdentifier", "com.cmuxterm.app")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "cmux" {
		t.Fatalf("expected cmux binary, got %s", wo.binary)
	}
}

func TestDetectReturnsWindowOpenerForKitty(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("KITTY_LISTEN_ON", "")
	t.Setenv("WEZTERM_PANE", "")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "kitty" {
		t.Fatalf("expected kitty binary, got %s", wo.binary)
	}
}

func TestDetectReturnsKittyClientForKittyWithSocket(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty-sock")
	t.Setenv("WEZTERM_PANE", "")
	got := Detect()
	kc, ok := got.(*kittyClient)
	if !ok {
		t.Fatalf("expected *kittyClient, got %T", got)
	}
	fb, ok := kc.fallback.(*windowOpener)
	if !ok || fb.binary != "kitty" {
		t.Fatalf("expected kitty fallback, got %#v", kc.fallback)
	}
}

func TestDetectReturnsGhosttyOpener(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	got := Detect()
	// On macOS Ghostty's AppleScript bridge can open a tab in the current
	// window; elsewhere the binary is all there is, and it only makes
	// windows.
	if runtime.GOOS == "darwin" {
		gc, ok := got.(*ghosttyClient)
		if !ok {
			t.Fatalf("expected *ghosttyClient, got %T", got)
		}
		fb, ok := gc.fallback.(*windowOpener)
		if !ok || fb.binary != "ghostty" {
			t.Fatalf("expected ghostty fallback, got %#v", gc.fallback)
		}
		return
	}
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "ghostty" {
		t.Fatalf("expected ghostty binary, got %s", wo.binary)
	}
}

func TestDetectReturnsWeztermClientForWezTerm(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "1")
	got := Detect()
	wc, ok := got.(*weztermClient)
	if !ok {
		t.Fatalf("expected *weztermClient, got %T", got)
	}
	fb, ok := wc.fallback.(*windowOpener)
	if !ok || fb.binary != "wezterm" {
		t.Fatalf("expected wezterm fallback, got %#v", wc.fallback)
	}
}

func TestDetectReturnsFallbackForUnknown(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	t.Setenv("TERM", "")
	t.Setenv("TILIX_ID", "")
	t.Setenv("KONSOLE_VERSION", "")
	t.Setenv("XTERM_VERSION", "")
	t.Setenv("VTE_VERSION", "")
	t.Setenv("GHOSTTY_RESOURCES_DIR", "")
	t.Setenv("TMUX", "")
	// macOS has a fallback that can still open something (see
	// commandFileOpener); everywhere else there is nothing to open.
	want := fmt.Sprintf("%T", platformFallback())
	if got := fmt.Sprintf("%T", Detect()); got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestDetectReturnsWindowOpenerForTilix(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	t.Setenv("TERM", "")
	t.Setenv("TILIX_ID", "some-id")
	t.Setenv("VTE_VERSION", "6800")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "tilix" {
		t.Fatalf("expected tilix binary, got %s", wo.binary)
	}
}

func TestDetectReturnsWindowOpenerForKonsole(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	t.Setenv("TERM", "")
	t.Setenv("TILIX_ID", "")
	t.Setenv("KONSOLE_VERSION", "210401")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "konsole" {
		t.Fatalf("expected konsole binary, got %s", wo.binary)
	}
}

func TestDetectReturnsWindowOpenerForXterm(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	t.Setenv("TERM", "")
	t.Setenv("TILIX_ID", "")
	t.Setenv("KONSOLE_VERSION", "")
	t.Setenv("XTERM_VERSION", "XTerm(379)")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "xterm" {
		t.Fatalf("expected xterm binary, got %s", wo.binary)
	}
}

func clearLinuxTerminalEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"TERM_PROGRAM", "__CFBundleIdentifier", "KITTY_WINDOW_ID",
		"KITTY_LISTEN_ON", "WEZTERM_PANE", "TERM", "ALACRITTY_WINDOW_ID",
		"ALACRITTY_SOCKET", "TILIX_ID", "KONSOLE_VERSION", "XTERM_VERSION",
		"GNOME_TERMINAL_SCREEN", "GNOME_TERMINAL_SERVICE", "VTE_VERSION", "TMUX",
	} {
		t.Setenv(v, "")
	}
}

func TestDetectReturnsWindowOpenerForGnomeTerminal(t *testing.T) {
	clearLinuxTerminalEnv(t)
	t.Setenv("VTE_VERSION", "6800")
	t.Setenv("GNOME_TERMINAL_SCREEN", "/org/gnome/Terminal/screen/abc")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "gnome-terminal" {
		t.Fatalf("expected gnome-terminal binary, got %s", wo.binary)
	}
}

// A VTE-based terminal that isn't GNOME Terminal (Ptyxis, GNOME Console,
// Xfce Terminal, ...) sets VTE_VERSION but may not ship the gnome-terminal
// binary; Detect must not return an opener doomed to exec a missing binary.
func TestDetectVTEWithoutGnomeTerminalFallsBack(t *testing.T) {
	clearLinuxTerminalEnv(t)
	t.Setenv("VTE_VERSION", "7800")
	t.Setenv("PATH", t.TempDir()) // no gnome-terminal on PATH
	// Either platform fallback will do; what matters is that Detect didn't
	// hand back a windowOpener doomed to exec a gnome-terminal that isn't there.
	if got, want := fmt.Sprintf("%T", Detect()), fmt.Sprintf("%T", platformFallback()); got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestDetectVTEWithGnomeTerminalInstalled(t *testing.T) {
	clearLinuxTerminalEnv(t)
	t.Setenv("VTE_VERSION", "7800")
	dir := t.TempDir()
	fake := filepath.Join(dir, "gnome-terminal")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "gnome-terminal" {
		t.Fatalf("expected gnome-terminal binary, got %s", wo.binary)
	}
}

func TestDetectReturnsWindowOpenerForFoot(t *testing.T) {
	clearLinuxTerminalEnv(t)
	t.Setenv("TERM", "foot")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "foot" {
		t.Fatalf("expected foot binary, got %s", wo.binary)
	}
}

func TestDetectReturnsRemoteOpenerForAlacrittyWithSocket(t *testing.T) {
	clearLinuxTerminalEnv(t)
	t.Setenv("ALACRITTY_WINDOW_ID", "42")
	t.Setenv("ALACRITTY_SOCKET", "/tmp/alacritty-sock")
	got := Detect()
	ro, ok := got.(*remoteOpener)
	if !ok {
		t.Fatalf("expected *remoteOpener, got %T", got)
	}
	if ro.binary != "alacritty" {
		t.Fatalf("expected alacritty binary, got %s", ro.binary)
	}
}

// ALACRITTY_WINDOW_ID survives inside tmux, where TERM is no longer
// "alacritty" — detection must still work there.
func TestDetectReturnsWindowOpenerForAlacrittyViaWindowID(t *testing.T) {
	clearLinuxTerminalEnv(t)
	t.Setenv("TERM", "tmux-256color")
	t.Setenv("ALACRITTY_WINDOW_ID", "42")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "alacritty" {
		t.Fatalf("expected alacritty binary, got %s", wo.binary)
	}
}

func TestDetectReturnsWindowOpenerForWindowsTerminal(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("GHOSTTY_RESOURCES_DIR", "")
	t.Setenv("WEZTERM_PANE", "")
	t.Setenv("TERM", "")
	t.Setenv("WT_SESSION", "1")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "wt.exe" {
		t.Fatalf("expected wt.exe binary, got %s", wo.binary)
	}
}

func TestDetectReturnsWindowOpenerForAppleTerminal(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	t.Setenv("TERM", "")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "open" {
		t.Fatalf("expected open binary, got %s", wo.binary)
	}
}

func TestDetectReturnsWindowOpenerForAlacritty(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	t.Setenv("TERM", "alacritty")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "alacritty" {
		t.Fatalf("expected alacritty binary, got %s", wo.binary)
	}
}
