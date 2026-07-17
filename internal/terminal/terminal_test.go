package terminal

import (
	"os"
	"path/filepath"
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

func TestDetectReturnsRemoteOpenerForKittyWithSocket(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty-sock")
	t.Setenv("WEZTERM_PANE", "")
	got := Detect()
	ro, ok := got.(*remoteOpener)
	if !ok {
		t.Fatalf("expected *remoteOpener, got %T", got)
	}
	if ro.binary != "kitten" {
		t.Fatalf("expected kitten binary, got %s", ro.binary)
	}
	fb, ok := ro.fallback.(*windowOpener)
	if !ok || fb.binary != "kitty" {
		t.Fatalf("expected kitty fallback, got %#v", ro.fallback)
	}
}

func TestDetectReturnsWindowOpenerForGhostty(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "ghostty" {
		t.Fatalf("expected ghostty binary, got %s", wo.binary)
	}
}

func TestDetectReturnsRemoteOpenerForWezTerm(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "1")
	got := Detect()
	ro, ok := got.(*remoteOpener)
	if !ok {
		t.Fatalf("expected *remoteOpener, got %T", got)
	}
	if ro.binary != "wezterm" {
		t.Fatalf("expected wezterm binary, got %s", ro.binary)
	}
	fb, ok := ro.fallback.(*windowOpener)
	if !ok || fb.binary != "wezterm" {
		t.Fatalf("expected wezterm fallback, got %#v", ro.fallback)
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
	got := Detect()
	if _, ok := got.(*fallbackOpener); !ok {
		t.Fatalf("expected *fallbackOpener, got %T", got)
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
		"GNOME_TERMINAL_SCREEN", "GNOME_TERMINAL_SERVICE", "VTE_VERSION",
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
	got := Detect()
	if _, ok := got.(*fallbackOpener); !ok {
		t.Fatalf("expected *fallbackOpener, got %T", got)
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
