package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var update = flag.Bool("update", false, "rewrite the testdata golden files")

// TestRenderScreenCoversAllScenarios drives every registered screen scenario
// through renderScreen, the deterministic Go half of scripts/screenshot.sh's
// pipeline (the pty/HTML/Chromium half needs a real display and isn't
// practical to unit-test). A bad key sequence or an unknown screen name
// (e.g. a typo introduced when adding a new scenario) fails here instead of
// only showing up as a blank or broken screenshot.
func TestRenderScreenCoversAllScenarios(t *testing.T) {
	for name := range screens {
		t.Run(name, func(t *testing.T) {
			out, err := renderScreen(name, 100, 32, "", "")
			if err != nil {
				t.Fatalf("renderScreen(%q): %v", name, err)
			}
			if strings.TrimSpace(out) == "" {
				t.Fatalf("renderScreen(%q): empty view", name)
			}
		})
	}
}

func TestRenderScreenUnknownScreen(t *testing.T) {
	if _, err := renderScreen("does-not-exist", 100, 32, "", ""); err == nil {
		t.Fatal("expected error for unknown screen, got nil")
	}
}

// sizes are the terminal geometries every screen is checked at: the
// narrowest real-world client (see narrowWidthBreak in internal/tui) and the
// default screenshot size.
var sizes = [][2]int{{40, 20}, {100, 32}}

// TestScreens is the automated half of the two UI rules in AGENTS.md, run
// over every registered scenario at both geometries in one render pass:
//
//   - Nothing overflows its terminal. On a ~40-column mobile SSH client an
//     unwrapped footer, header or hint hard-clips mid-word instead of
//     degrading, and the only guard used to be a human looking at a PNG.
//     lipgloss.Width is used rather than len so ANSI styling and wide runes
//     (emoji project icons) measure as they render.
//
//   - The plain-text layout matches its golden file, so an unrelated change
//     that quietly reflows a screen shows up as a reviewable text diff
//     instead of only as a PNG nobody re-rendered. Styling is stripped:
//     colour is what the screenshots are for, layout is what breaks
//     silently. Update with `go test ./cmd/uishot -update` and read the diff.
func TestScreens(t *testing.T) {
	for name := range screens {
		t.Run(name, func(t *testing.T) {
			var b strings.Builder
			for _, s := range sizes {
				cols, rows := s[0], s[1]
				out, err := renderScreen(name, cols, rows, "", "")
				if err != nil {
					t.Fatalf("%dx%d: %v", cols, rows, err)
				}
				lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
				if len(lines) > rows {
					t.Errorf("%dx%d: view is %d lines, more than %d rows", cols, rows, len(lines), rows)
				}
				for i, line := range lines {
					if w := lipgloss.Width(line); w > cols {
						t.Errorf("%dx%d: line %d is %d cells wide, more than %d columns:\n%s", cols, rows, i+1, w, cols, line)
					}
				}
				fmt.Fprintf(&b, "=== %dx%d ===\n%s\n", cols, rows, strings.TrimRight(ansi.Strip(out), "\n"))
			}

			path := filepath.Join("testdata", name+".txt")
			got := b.String()
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run `go test ./cmd/uishot -update` to create it)", err)
			}
			if got != string(want) {
				t.Errorf("screen %q no longer matches %s — inspect the change and re-run with -update if it is intended\n--- got ---\n%s", name, path, got)
			}
		})
	}
}
