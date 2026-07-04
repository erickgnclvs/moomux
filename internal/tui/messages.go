package tui

import (
	"time"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

type StatusTickMsg struct{ Snap watcher.Snapshot }

type SessionsRefreshedMsg struct{ Sessions []session.Session }

type ErrorMsg struct{ Err error }

type InfoMsg struct {
	Text string
	When time.Time
}

type SessionOpenedMsg struct{ ID string }
type SessionCreatedMsg struct{ Session session.Session }
type SessionDeletedMsg struct{ ID string }
type SessionTaggedMsg struct{ Session session.Session }
type TmuxKilledMsg struct{ ID string }
