package terminal

import (
	"testing"
)

func TestDetectReturnsITermForITermApp(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	got := Detect()
	if _, ok := got.(*itermClient); !ok {
		t.Fatalf("expected *itermClient, got %T", got)
	}
}

func TestDetectReturnsWindowOpenerForKitty(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "1")
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

func TestDetectReturnsWindowOpenerForWezTerm(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "1")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "wezterm" {
		t.Fatalf("expected wezterm binary, got %s", wo.binary)
	}
}

func TestDetectReturnsFallbackForUnknown(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
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

func TestDetectReturnsWindowOpenerForGnomeTerminal(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WEZTERM_PANE", "")
	t.Setenv("TERM", "")
	t.Setenv("TILIX_ID", "")
	t.Setenv("KONSOLE_VERSION", "")
	t.Setenv("XTERM_VERSION", "")
	t.Setenv("VTE_VERSION", "6800")
	got := Detect()
	wo, ok := got.(*windowOpener)
	if !ok {
		t.Fatalf("expected *windowOpener, got %T", got)
	}
	if wo.binary != "gnome-terminal" {
		t.Fatalf("expected gnome-terminal binary, got %s", wo.binary)
	}
}

func TestDetectReturnsWindowOpenerForAppleTerminal(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
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
