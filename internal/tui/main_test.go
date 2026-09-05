package tui

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestMain pins lipgloss's colour profile, and rebuilds the styles derived
// from it, for the whole package. The profile is process-global state that
// lipgloss otherwise detects from the ambient terminal, and styles.go
// pre-renders a handful of strings (iconTicket, the status dots) from
// whatever it was at applyTheme time — so tests asserting on rendered output
// used to depend on the developer's terminal and on which other test had run
// first. Ascii is the baseline because it is what a non-tty run detects;
// tests that need real escape codes set TrueColor themselves.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	applyTheme("default")
	os.Exit(m.Run())
}
