package tui

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
	Up         key.Binding
	Down       key.Binding
	Open       key.Binding
	New        key.Binding
	Delete     key.Binding
	Kill       key.Binding
	Refresh    key.Binding
	Tab        key.Binding
	Quit       key.Binding
	Cancel     key.Binding
	Confirm    key.Binding
	NewProject key.Binding
	DelProject key.Binding
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Open:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		New:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Delete:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Kill:    key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "kill tmux")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Tab:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "project")),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Cancel:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		Confirm:    key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "confirm")),
		NewProject: key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "add project")),
		DelProject: key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "remove project")),
	}
}
