package tui

import (
	"time"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/sessionview"
)

// StatusTickMsg carries one snapshot of derived per-session state from the
// core. It replaces the model's views wholesale — a Snapshot is absolute
// state, not a delta.
type StatusTickMsg struct{ Snap sessionview.Snapshot }

// GitStatusMsg carries the delete dialog's on-demand git-status check for
// one session, computed by fetchGitStatusCmd. Routine git status arrives on
// StatusTickMsg instead.
type GitStatusMsg struct {
	ID     string
	Status gitStatusInfo
}

// ChangeSummaryMsg carries the file/commit-count detail for one session's
// delete-confirmation warning, computed by fetchChangeSummaryCmd. ID guards
// against a stale result landing after the dialog moved on to another
// session (or closed).
type ChangeSummaryMsg struct {
	ID      string
	Summary changeSummary
}

// StatusChannelClosedMsg is delivered when the status watcher channel is
// closed. It is terminal: no attempt is made to restart the watcher.
type StatusChannelClosedMsg struct{}

type ErrorMsg struct{ Err error }

// CreateFailedMsg reports a failed session creation. Unlike ErrorMsg it
// reopens the new-session form with everything still typed in it — the first
// prompt can be several paragraphs of work, and dropping back to the list
// with only a flash message threw it away.
type CreateFailedMsg struct {
	Err error
	// Cfg, when set, is the backend's post-mutation config snapshot for
	// Update() to apply via *m.cfg = *Cfg — set only when the failed create
	// still persisted a new auto-submit default before failing; nil
	// otherwise.
	Cfg *config.Config
}

// UpdateAvailableMsg is delivered by checkUpdateCmd when GitHub Releases
// reports a version newer than the one currently running.
type UpdateAvailableMsg struct{ Version string }

// UpdateCheckTickMsg fires every updateCheckInterval to re-poll GitHub
// Releases, so long-running sessions still notice new versions.
type UpdateCheckTickMsg struct{}

// UpdateAppliedMsg is delivered by runUpdateCmd once `brew upgrade moomux`
// finishes. Err is nil on success, in which case the caller quits the
// program so main() can exec the newly-installed binary in place.
type UpdateAppliedMsg struct{ Err error }

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
	// Cfg, when set, is the backend's post-mutation config snapshot for
	// Update() to apply via *m.cfg = *Cfg — set only when creating this
	// session also persisted a new auto-submit default; nil otherwise.
	Cfg *config.Config
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
// flow reacts to errors differently in Update(). Cfg, like every other
// mutation Msg's Cfg field below, is the backend's post-mutation config
// snapshot for Update() to apply via *m.cfg = *Cfg; nil means the mutation
// failed and m.cfg should be left alone.
type ProjectAddedMsg struct {
	Kind    string
	Name    string
	Project config.Project
	Err     error
	// Warning accompanies a gitwt.ErrNotGitRepo Err: the core's note about
	// the path, shown in the init-choice dialog. Empty otherwise.
	Warning string
	Cfg     *config.Config
}

type ProjectRemovedMsg struct {
	Name string
	Err  error
	Cfg  *config.Config
}

// ProjectMovedMsg is the result of an async reorder (MoveProject) call.
// Update() re-syncs m.projects and re-anchors activeProj on Name once this
// arrives; Err is set if the persisted reorder failed.
type ProjectMovedMsg struct {
	Name string
	Err  error
	Cfg  *config.Config
}

type ProjectUpdatedMsg struct {
	Name string
	Err  error
	Cfg  *config.Config
}

// ThemeSetMsg is the result of an async SetTheme persist call.
type ThemeSetMsg struct {
	Theme      string
	Appearance string
	Cfg        *config.Config
}
