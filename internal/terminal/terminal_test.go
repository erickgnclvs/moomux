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
	got := Detect()
	if _, ok := got.(*fallbackOpener); !ok {
		t.Fatalf("expected *fallbackOpener, got %T", got)
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
