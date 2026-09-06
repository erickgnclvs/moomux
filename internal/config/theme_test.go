package config

import (
	"reflect"
	"testing"
)

// TestThemesComplete guards the invariants a front end relies on when it
// stops keeping its own color table: every theme defines every color, and
// the state colors stay distinguishable from each other.
func TestThemesComplete(t *testing.T) {
	all := Themes()
	if len(all) == 0 {
		t.Fatal("Themes() is empty")
	}
	for _, th := range all {
		v := reflect.ValueOf(th)
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.Type != reflect.TypeOf(Color{}) {
				continue
			}
			c := v.Field(i).Interface().(Color)
			if c.Light == "" || c.Dark == "" {
				t.Errorf("%s.%s: %+v has an empty half", th.Name, f.Name, c)
			}
			// Only "default" maps onto platform semantic colors; the
			// others are designer palettes that must render as themselves.
			if th.Name != "default" && c.System != "" {
				t.Errorf("%s.%s: System %q set on a non-default theme", th.Name, f.Name, c.System)
			}
		}
		// The whole point of serving this: the same color must not mean two
		// different agent states, in any theme or in any front end.
		states := map[string]Color{
			"working": th.Working, "done": th.Done,
			"needsInput": th.NeedsInput, "parked": th.Parked,
		}
		seen := map[string]string{}
		for name, c := range states {
			if prev, dup := seen[c.Dark]; dup {
				t.Errorf("%s: %s and %s are both %q", th.Name, prev, name, c.Dark)
			}
			seen[c.Dark] = name
		}
		// Warn used to be Done. Now that Done is green, a warning drawn in
		// it would read as success — they must stay separate.
		if th.Warn == th.Done {
			t.Errorf("%s: Warn and Done are the same color %+v", th.Name, th.Done)
		}
	}
}

// TestDefaultThemeCarriesSystemColors pins the backport: the default theme's
// state colors are the macOS app's semantic ones, so a native front end can
// keep following the user's live system accent instead of a frozen hex.
func TestDefaultThemeCarriesSystemColors(t *testing.T) {
	d := ThemeByName("")
	if d.Name != "default" {
		t.Fatalf("ThemeByName(\"\") = %q, want default", d.Name)
	}
	want := map[string]string{
		"working": "accent", "done": "green",
		"needsInput": "orange", "parked": "secondary",
	}
	got := map[string]string{
		"working": d.Working.System, "done": d.Done.System,
		"needsInput": d.NeedsInput.System, "parked": d.Parked.System,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default state System names = %v, want %v", got, want)
	}
}

// TestANSIThemeFlagged makes sure a GUI front end can tell that "terminal"'s
// colors are palette indices it can't render, in one branch.
func TestANSIThemeFlagged(t *testing.T) {
	for _, th := range Themes() {
		if (th.Name == "terminal") != th.ANSI {
			t.Errorf("%s: ANSI = %v", th.Name, th.ANSI)
		}
	}
}

func TestThemesReturnsACopy(t *testing.T) {
	Themes()[0].Working.Dark = "#000000"
	if got := ThemeByName("default").Working.Dark; got == "#000000" {
		t.Fatal("Themes() hands out the table itself")
	}
}

func TestThemeByNameFallsBack(t *testing.T) {
	if got := ThemeByName("dracula").Name; got != "default" {
		t.Errorf("ThemeByName(\"dracula\") = %q, want default", got)
	}
}
