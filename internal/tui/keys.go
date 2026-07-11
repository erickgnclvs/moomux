package tui

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
	Up           key.Binding
	Down         key.Binding
	MoveUp       key.Binding
	MoveDown     key.Binding
	Open         key.Binding
	New          key.Binding
	Delete       key.Binding
	Archive      key.Binding
	ShowArchived key.Binding
	Kill         key.Binding
	Refresh      key.Binding
	Tab          key.Binding
	ShiftTab     key.Binding
	Quit         key.Binding
	Cancel       key.Binding
	Confirm      key.Binding
	NewProject   key.Binding
	DelProject   key.Binding
	Tag          key.Binding
	Enter        key.Binding
	Left         key.Binding
	Right        key.Binding
	No           key.Binding
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:           key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:         key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		MoveUp:       key.NewBinding(key.WithKeys("shift+up"), key.WithHelp("shift+↑", "move up")),
		MoveDown:     key.NewBinding(key.WithKeys("shift+down"), key.WithHelp("shift+↓", "move down")),
		Open:         key.NewBinding(key.WithKeys("enter", "o"), key.WithHelp("enter/o", "open")),
		New:          key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Delete:       key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Archive:      key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "archive")),
		ShowArchived: key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "archived")),
		Kill:         key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "park")),
		Refresh:      key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Tab:          key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next project")),
		ShiftTab:     key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev project")),
		Quit:         key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Cancel:       key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		Confirm:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "confirm")),
		NewProject:   key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "add project")),
		DelProject:   key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "remove project")),
		Tag:          key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "tag")),
		Enter:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit")),
		Left:         key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "left")),
		Right:        key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "right")),
		No:           key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "no")),
	}
}
