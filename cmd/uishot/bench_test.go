package main

import "testing"

// BenchmarkRenderScreen drives the real tui.Model against the fake backend, so
// it can be profiled (go test -cpuprofile, or Instruments on the test binary)
// without any real projects, git repos or tmux sessions.
func BenchmarkRenderScreen(b *testing.B) {
	for _, screen := range []string{"list", "long-list", "new-session"} {
		b.Run(screen, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := renderScreen(screen, 100, 32, "", ""); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
