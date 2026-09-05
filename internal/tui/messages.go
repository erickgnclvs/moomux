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

// GitStatusMsg carries git status for sessions that just transitioned into
// watcher.Parked, computed by fetchGitStatusCmd. Update() merges these into
// m.gitStatus rather than replacing it wholesale.
type GitStatusMsg struct {
	Status map[string]gitStatusInfo
}

// ChangeSummaryMsg carries the file/commit-count detail for one session's
// delete-confirmation warning, computed by fetchChangeSummaryCmd. ID guards
// against a stale result landing after the dialog moved on to another
// session (or closed).
type ChangeSummaryMsg struct {
	ID      string
	Summary changeSummary
}

// PRStatusMsg carries PR status for sessions with a PR attached, computed by
// fetchPRStatusCmd. Update() merges these into m.prStatus rather than
// replacing it wholesale, mirroring GitStatusMsg.
type PRStatusMsg struct {
	Status map[string]prStatusInfo
}

// StatusChannelClosedMsg is delivered when the status watcher channel is
// closed. It is terminal: no attempt is made to restart the watcher.
type StatusChannelClosedMsg struct{}

type ErrorMsg struct{ Err error }

// CreateFailedMsg reports a failed session creation. Unlike ErrorMsg it
// reopens the new-session form with everything still typed in it — the first
// prompt can be several paragraphs of work, and dropping back to the list
// with only a flash message threw it away.
type CreateFailedMsg struct{ Err error }

// UpdateAvailableMsg is delivered by checkUpdateCmd when GitHub Releases
// reports a version newer than the one currently running.
type UpdateAvailableMsg struct{ Version string }

// UpdateCheckTickMsg fires every updateCheckInterval to re-poll GitHub
// Releases, so long-running sessions still notice new versions.
type UpdateCheckTickMsg struct{}

type InfoMsg struct {
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

// SessionDeletedMsg is the result of an async delete. NextID is whichever
// session should take over the selection (the row below ID's, or above if it
// was last), pinned from m.sessions at the moment the delete was confirmed —
// before this arrives, killing ID's tmux pane can flip its tmux-alive state
// and resort the list on its own, so recomputing "the neighbor" only once
// this message lands would inherit that resort instead of the order the user
// actually saw when they confirmed the delete.
type SessionDeletedMsg struct {
	ID     string
	NextID string
	Hint   string
}

// SessionArchivedMsg is the result of an async archive/restore toggle.
// Archived reflects the state applied (true = archived, false = restored).
// NextID mirrors SessionDeletedMsg's — pinned before the toggle fires, for
// the same reason.
type SessionArchivedMsg struct {
	ID       string
	Archived bool
	NextID   string
	Err      error
}
type SessionTaggedMsg struct{ Session session.Session }
type SessionAgentUpdatedMsg struct {
	Session session.Session
	Err     error
}
type TmuxKilledMsg struct{ ID string }

// SessionsReorderedMsg is the result of an async ReorderSessions call fired
// by dispatchReorder. Err is set if the persist failed, in which case
// Update() reverts the optimistic local reorder by re-syncing m.sessions
// from the (unchanged) backend.
type SessionsReorderedMsg struct {
	Err error
}

// SessionFolderSetMsg is the result of an async SetSessionFolder call.
type SessionFolderSetMsg struct{ Session session.Session }

// FolderCreatedMsg is the result of an async CreateFolder call.
type FolderCreatedMsg struct {
	Project, Name string
	Err           error
}

// FolderRenamedMsg is the result of an async RenameFolder call.
type FolderRenamedMsg struct {
	OldName, NewName string
	Err              error
}

// FolderCollapsedSetMsg is the result of an async SetFolderCollapsed call.
type FolderCollapsedSetMsg struct {
	Project, Name string
	Collapsed     bool
	Err           error
}

// FolderDeletedMsg is the result of an async DeleteFolder call.
type FolderDeletedMsg struct {
	Project, Name string
	Err           error
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

type ProjectUpdatedMsg struct {
	Name string
	Err  error
}

// ThemeSetMsg is the result of an async SetTheme persist call.
type ThemeSetMsg struct {
	Theme      string
	Appearance string
}
