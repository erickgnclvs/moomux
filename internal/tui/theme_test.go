package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestApplyThemeRebuildsPrerenderedDots guards the one thing easy to forget
// when adding a new derived style: dotWorking/dotDone/dotNeedsInput/
// dotParked (and iconTicket/iconPR) are pre-rendered strings baked at
// buildStyles() time, not read live — a theme swap that only reassigns
// colWorking etc. without rebuilding these would leave the status dots
// showing the previous theme's colors forever.
func TestApplyThemeRebuildsPrerenderedDots(t *testing.T) {
	// Rendering is color-profile dependent — outside a real terminal lipgloss
	// downsamples to no color, which would make every theme's dots render as
	// identical plain text regardless of this bug. Force a color profile
	// that actually emits escape codes, like list_test.go does.
	origProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor) // avoids ANSI256/ANSI quantization coincidentally mapping two distinct hex colors to the same code
	// Order matters: the profile has to go back *before* the styles are
	// rebuilt, or applyTheme bakes truecolor escapes into the pre-rendered
	// globals (iconTicket and friends) and leaves them there for every later
	// test in this binary, which then renders under a different profile —
	// order-dependent failures that only show up under -shuffle.
	t.Cleanup(func() {
		lipgloss.SetColorProfile(origProfile)
		applyAppearance("")
		applyTheme("default")
	})
	// Appearance fixed so only the theme changes between the two renders.
	applyAppearance("dark")

	applyTheme("default")
	beforeDot, beforeTicket := dotWorking, iconTicket

	applyTheme("gruvbox")
	afterDot, afterTicket := dotWorking, iconTicket

	if beforeDot == afterDot {
		t.Fatal("applyTheme did not rebuild dotWorking — status dots would keep the previous theme's color")
	}
	if beforeTicket == afterTicket {
		t.Fatal("applyTheme did not rebuild iconTicket — the ticket icon would keep the previous theme's color")
	}
}

// TestApplyThemeChangesColors is a broader sanity check that every palette
// actually differs from default, not just working/done.
func TestApplyThemeChangesColors(t *testing.T) {
	defer applyTheme("default")
	for _, name := range themeNames {
		if name == "default" {
			continue
		}
		applyTheme("default")
		before := colAccent
		applyTheme(name)
		if colAccent == before {
			t.Errorf("theme %q left colAccent unchanged from default", name)
		}
	}
}

func TestApplyThemeUnknownFallsBackToDefault(t *testing.T) {
	defer applyTheme("default")
	applyTheme("default")
	want := colFg
	applyTheme("this-theme-does-not-exist")
	if colFg != want {
		t.Fatalf("unknown theme name should fall back to default, colFg = %v, want %v", colFg, want)
	}
}

// TestApplyAppearanceOverridesAndRestoresAuto checks light/dark force the
// renderer's background flag, and that "auto" restores whatever autoDark()
// resolved to — not necessarily "the real terminal", since test binaries
// aren't attached to one, but consistently the same cached value every time.
func TestApplyAppearanceOverridesAndRestoresAuto(t *testing.T) {
	origDark := autoDark()
	defer applyAppearance("")

	applyAppearance("light")
	if lipgloss.HasDarkBackground() {
		t.Fatal("appearance=light should force HasDarkBackground()=false")
	}
	applyAppearance("dark")
	if !lipgloss.HasDarkBackground() {
		t.Fatal("appearance=dark should force HasDarkBackground()=true")
	}
	applyAppearance("")
	if got := lipgloss.HasDarkBackground(); got != origDark {
		t.Fatalf("appearance=\"\" (auto) should restore %v, got %v", origDark, got)
	}
}

func TestNextAppearanceCyclesAutoLightDark(t *testing.T) {
	seq := []string{"", "light", "dark", ""}
	cur := ""
	for i := 1; i < len(seq); i++ {
		cur = nextAppearance(cur)
		if cur != seq[i] {
			t.Fatalf("step %d: nextAppearance = %q, want %q", i, cur, seq[i])
		}
	}
}

func aKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")} }

// openThemePickerViaSettings opens the settings screen and drills into the
// theme picker from its "theme & appearance" row (index 1) — the only way
// to reach ModeThemePicker now that T no longer opens it directly.
func openThemePickerViaSettings(m *Model) {
	m.Update(runeKey('s'))
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // sort mode -> theme & appearance
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func TestThemePickerLivePreviewAndEscReverts(t *testing.T) {
	defer applyTheme("default")
	defer applyAppearance("")
	applyTheme("default")
	applyAppearance("")

	be := &fakeBackend{}
	m := newTestModel(be)

	openThemePickerViaSettings(m)
	if m.mode != ModeThemePicker {
		t.Fatalf("expected ModeThemePicker, got %v", m.mode)
	}
	if m.themeCursor != 0 {
		t.Fatalf("expected cursor to start at default (0), got %d", m.themeCursor)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.themeCursor != 1 {
		t.Fatalf("expected cursor 1 after down, got %d", m.themeCursor)
	}
	if colFg != themes[themeNames[1]].fg {
		t.Fatalf("expected down to live-preview theme %q immediately", themeNames[1])
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != ModeSettings {
		t.Fatalf("expected esc to return to ModeSettings, got %v", m.mode)
	}
	if colFg != themes["default"].fg {
		t.Fatal("expected esc to revert the live preview back to the persisted theme")
	}
	if len(be.setThemeCalls) != 0 {
		t.Fatalf("expected no SetTheme call on cancel, got %v", be.setThemeCalls)
	}
}

func TestThemePickerEnterPersistsSelection(t *testing.T) {
	defer applyTheme("default")
	defer applyAppearance("")
	applyTheme("default")
	applyAppearance("")

	be := &fakeBackend{}
	m := newTestModel(be)

	openThemePickerViaSettings(m)
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // -> themeNames[1]
	m.Update(aKey())                        // auto -> light
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drainCmd(m, cmd)

	if m.mode != ModeSettings {
		t.Fatalf("expected enter to return to ModeSettings, got %v", m.mode)
	}
	if len(be.setThemeCalls) != 1 {
		t.Fatalf("expected 1 SetTheme call, got %d: %v", len(be.setThemeCalls), be.setThemeCalls)
	}
	want := setThemeCall{theme: themeNames[1], appearance: "light"}
	if be.setThemeCalls[0] != want {
		t.Fatalf("SetTheme called with %+v, want %+v", be.setThemeCalls[0], want)
	}
}

// TestSettingsEscClosesAfterThemePicker guards against openThemePicker
// clobbering sessionDialogReturn, the same field ModeSettings itself relies
// on for Esc: drilling into the theme picker and backing out of it must not
// leave Settings unable to close.
func TestSettingsEscClosesAfterThemePicker(t *testing.T) {
	defer applyTheme("default")
	defer applyAppearance("")

	be := &fakeBackend{}
	m := newTestModel(be)

	openThemePickerViaSettings(m)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // back out of the theme picker
	if m.mode != ModeSettings {
		t.Fatalf("expected esc from theme picker to return to ModeSettings, got %v", m.mode)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // should close settings now
	if m.mode != ModeList {
		t.Fatalf("expected esc from settings to return to ModeList, got %v", m.mode)
	}
}

// TestThemePickerFooterFitsNarrowWidths mirrors
// TestProjectPickerFooterFitsNarrowWidths: the footer must never be wider
// than the overlay, or it gets hard-clipped mid-word on mobile widths.
func TestThemePickerFooterFitsNarrowWidths(t *testing.T) {
	be := &fakeBackend{}
	m := newTestModel(be)
	for _, width := range []int{200, 100, 72, 60, 50, 40} {
		m.width = width
		footer := m.themePickerFooter()
		avail := m.overlayWidth(formHintWidth)
		if w := lipgloss.Width(footer); w > avail {
			t.Errorf("width=%d: footer %q is %d cells wide, want <= %d", width, footer, w, avail)
		}
	}
}
