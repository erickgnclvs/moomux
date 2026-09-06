package config

// Color is one entry in a Theme: a light/dark pair, plus an optional name of
// a platform semantic color a native front end should prefer over the pair.
//
// Light/Dark are "#rrggbb" hex, except on a Theme with ANSI set, where they
// are terminal palette indices ("11") instead.
type Color struct {
	Light string `json:"light"`
	Dark  string `json:"dark"`
	// System names a platform semantic color — "accent", "green", "orange",
	// "secondary" — for a front end that has real ones (SwiftUI's
	// .accentColor, .green, ...). It is set only on the "default" theme:
	// the other three are deliberate designer palettes and should render as
	// themselves everywhere. Light/Dark always carry a usable fallback, so
	// ignoring this field is a valid implementation — that is what the TUI
	// does, since a terminal has no system accent to follow.
	System string `json:"system,omitempty"`
}

// Theme is the full set of colors a front end renders moomux with. Served
// over internal/ipc so neither the TUI nor a native app keeps its own copy —
// the same reason AgentOption is served rather than duplicated.
//
// Working/Done/NeedsInput/Parked are the agent-state colors (the TUI's ⬤
// dots, the Mac app's state icon). Every theme uses the same *meaning* for
// them — working is blue, done is green, needs-input is orange — expressed in
// that theme's own palette. That consistency is the point: green previously
// meant "working" in the TUI and "done" in the Mac app.
type Theme struct {
	Name string `json:"name"`
	// ANSI marks Light/Dark as terminal palette indices rather than hex, so
	// a front end with no terminal colorscheme to delegate to (a GUI) can
	// detect that in one branch instead of sniffing the string, and fall
	// back to the "default" theme.
	ANSI bool `json:"ansi,omitempty"`

	Fg     Color `json:"fg"`
	Mute   Color `json:"mute"`
	Accent Color `json:"accent"`

	Working    Color `json:"working"`
	Done       Color `json:"done"`
	NeedsInput Color `json:"needs_input"`
	Parked     Color `json:"parked"`

	// Warn colors non-fatal warnings: the form's "choose a project" nudges,
	// the delete-confirm's uncommitted-changes line, and the session list's
	// ± / ↑ git badges. It used to be Done — which worked only while Done
	// was amber; now that done is green, "you have unpushed commits" in
	// green would read as success. Splitting them keeps warnings amber in
	// every theme while the state dots move.
	Warn   Color `json:"warn"`
	Danger Color `json:"danger"`
	Border Color `json:"border"`
	SelBg  Color `json:"sel_bg"`
}

// themes is the built-in palette table, in display/cycle order — "default"
// first, since it is what an unset or unrecognized config.Theme falls back
// to.
//
// "default"'s four state colors are the macOS app's, backported: they are
// AppKit's controlAccentColor / systemGreen / systemOrange /
// secondaryLabelColor, read off a Mac (secondaryLabelColor's 50%/55% alpha
// flattened over the window background, since a terminal cell has nothing to
// blend with). controlAccentColor is whatever accent the *user* has picked
// system-wide, so #007aff is only the default-blue reading of it — that is
// exactly why Working also carries System: "accent", and why a front end
// that can follow the live accent should.
var themes = []Theme{
	{
		Name:       "default",
		Fg:         Color{Light: "#1a1a1a", Dark: "#e6e6e6"},
		Mute:       Color{Light: "#5b5b66", Dark: "#7a7a85"},
		Accent:     Color{Light: "#2952cc", Dark: "#7aa2f7"},
		Working:    Color{Light: "#007aff", Dark: "#007aff", System: "accent"},
		Done:       Color{Light: "#34c759", Dark: "#30d158", System: "green"},
		NeedsInput: Color{Light: "#ff8d28", Dark: "#ff9230", System: "orange"},
		Parked:     Color{Light: "#808080", Dark: "#99999a", System: "secondary"},
		Warn:       Color{Light: "#946f1a", Dark: "#e0af68"},
		Danger:     Color{Light: "#c0293f", Dark: "#f7768e"},
		Border:     Color{Light: "#9a9aa5", Dark: "#2d2f3a"},
		SelBg:      Color{Light: "#a9bdf0", Dark: "#2f395e"},
	},
	{
		// "terminal" delegates to the user's own colorscheme via ANSI
		// indices, which is why its Light/Dark halves match — the terminal
		// resolves those per its own appearance already.
		Name:       "terminal",
		ANSI:       true,
		Fg:         Color{Light: "15", Dark: "15"},
		Mute:       Color{Light: "8", Dark: "8"},
		Accent:     Color{Light: "12", Dark: "12"},
		Working:    Color{Light: "12", Dark: "12"},
		Done:       Color{Light: "10", Dark: "10"},
		NeedsInput: Color{Light: "11", Dark: "11"}, // no orange in ANSI 16; bright yellow is the nearest
		Parked:     Color{Light: "7", Dark: "7"},
		Warn:       Color{Light: "11", Dark: "11"},
		Danger:     Color{Light: "9", Dark: "9"},
		Border:     Color{Light: "8", Dark: "8"},
		SelBg:      Color{Light: "4", Dark: "4"},
	},
	{
		Name:       "gruvbox",
		Fg:         Color{Light: "#3c3836", Dark: "#ebdbb2"},
		Mute:       Color{Light: "#665c54", Dark: "#a89984"},
		Accent:     Color{Light: "#076678", Dark: "#83a598"},
		Working:    Color{Light: "#076678", Dark: "#83a598"},
		Done:       Color{Light: "#79740e", Dark: "#b8bb26"},
		NeedsInput: Color{Light: "#af3a03", Dark: "#fe8019"},
		Parked:     Color{Light: "#a89984", Dark: "#665c54"},
		Warn:       Color{Light: "#b57614", Dark: "#fabd2f"},
		Danger:     Color{Light: "#9d0006", Dark: "#fb4934"},
		Border:     Color{Light: "#d5c4a1", Dark: "#3c3836"},
		SelBg:      Color{Light: "#d5c4a1", Dark: "#504945"},
	},
	{
		Name:       "catppuccin",
		Fg:         Color{Light: "#4c4f69", Dark: "#cdd6f4"},
		Mute:       Color{Light: "#6c6f85", Dark: "#a6adc8"},
		Accent:     Color{Light: "#1e66f5", Dark: "#89b4fa"},
		Working:    Color{Light: "#1e66f5", Dark: "#89b4fa"},
		Done:       Color{Light: "#40a02b", Dark: "#a6e3a1"},
		NeedsInput: Color{Light: "#fe640b", Dark: "#fab387"},
		Parked:     Color{Light: "#9ca0b0", Dark: "#6c7086"},
		Warn:       Color{Light: "#df8e1d", Dark: "#f9e2af"},
		Danger:     Color{Light: "#d20f39", Dark: "#f38ba8"},
		Border:     Color{Light: "#acb0be", Dark: "#585b70"},
		SelBg:      Color{Light: "#ccd0da", Dark: "#313244"},
	},
}

// Themes returns the built-in palettes in display order. The slice is copied
// so a caller (or a JSON round trip through a front end) can't mutate the
// table.
func Themes() []Theme { return append([]Theme(nil), themes...) }

// ThemeByName returns the named palette, falling back to the first
// ("default") for an empty or unrecognized name — Theme is a free-text field
// in a hand-editable config.toml, so this never errors.
func ThemeByName(name string) Theme {
	for _, t := range themes {
		if t.Name == name {
			return t
		}
	}
	return themes[0]
}
