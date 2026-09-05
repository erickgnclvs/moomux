package updatecheck

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.5.3", "v0.5.4", true},
		{"0.5.3", "v0.5.3", false},
		{"0.5.4", "v0.5.3", false},
		{"1.0.0", "v0.9.9", false},
		{"0.5.9", "v0.6.0", true},
		{"dev", "v0.5.4", false},
		{"0.5.3", "not-a-version", false},
		{"", "v0.5.4", false},
	}
	for _, c := range cases {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}
