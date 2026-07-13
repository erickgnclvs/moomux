package tui

import (
	"time"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

type StatusTickMsg struct{ Snap watcher.Snapshot }

// StatusRefreshedMsg carries the results of an off-goroutine tmux-alive and
// prompt scan, computed by refreshStatusCmd. Update() merges these into the
// model; the computation itself must not touch model state.
type StatusRefreshedMsg struct {
	TmuxAlive map[string]bool
	Prompts   map[string]string
}

// StatusChannelClosedMsg is delivered when the status watcher channel is
// closed. It is terminal: no attempt is made to restart the watcher.
type StatusChannelClosedMsg struct{}

type SessionsRefreshedMsg struct{ Sessions []session.Session }

type ErrorMsg struct{ Err error }

type InfoMsg struct {
	Text string
	When time.Time
}

type SessionOpenedMsg struct {
	ID   string
	Hint string
}
type LinkOpenedMsg struct{ URL string }
type SessionCreatedMsg struct {
	Session session.Session
	Hint    string
}
type SessionDeletedMsg struct{ ID string }

// SessionArchivedMsg is the result of an async archive/restore toggle.
// Archived reflects the state applied (true = archived, false = restored).
type SessionArchivedMsg struct {
	ID       string
	Archived bool
	Err      error
}
type SessionTaggedMsg struct{ Session session.Session }
type TmuxKilledMsg struct{ ID string }

// SessionMovedMsg is the result of an async reorder (MoveSession) call.
// Update() re-syncs m.sessions and re-anchors the cursor on ID once this
// arrives; Err is set if the persisted reorder failed.
type SessionMovedMsg struct {
	ID  string
	Err error
}

// ProjectAddedMsg is the result of an async project-add flow. Kind
// distinguishes which backend call produced it ("add" for AddProject,
// "init" for InitProjectAndAdd, "plain" for AddPlainProject) since each
// flow reacts to errors differently in Update().
type ProjectAddedMsg struct {
	Kind    string
	Name    string
	Project config.Project
	Err     error
}

type ProjectRemovedMsg struct {
	Name string
	Err  error
}

// ProjectMovedMsg is the result of an async reorder (MoveProject) call.
// Update() re-syncs m.projects and re-anchors activeProj on Name once this
// arrives; Err is set if the persisted reorder failed.
type ProjectMovedMsg struct {
	Name string
	Err  error
}
