// Package ipc puts a unix socket in the middle of tui.Backend: Server
// exposes a real backend, Client re-implements the same interface over the
// wire. The TUI can't tell them apart, which is the point — a second front
// end (a native macOS app, say) speaks the same JSON and gets the whole
// orchestration core without linking any Go.
//
// Wire format is one JSON request line in, one JSON response line out, then
// the connection closes. The exception is "Watch", which streams
// sessionview.Snapshot lines until the client hangs up, and accepts nudge
// lines back on the same connection.
package ipc

import (
	"errors"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/session"
)

// DefaultSocket is where `moomux serve` listens unless told otherwise.
func DefaultSocket(home string) string {
	return home + "/.local/share/moomux/moomux.sock"
}

type request struct {
	Method string `json:"method"`
	Args   Args   `json:"args,omitempty"`
}

type response struct {
	Result Result `json:"result,omitempty"`
	Err    string `json:"err,omitempty"`
	// Code names a sentinel error the caller branches on, since errors.Is
	// can't survive a string round trip. Only sentinels the TUI actually
	// tests for need one — see sentinels.
	Code string `json:"code,omitempty"`
}

// sentinels maps wire codes to the sentinel errors the TUI branches on.
// tui/update.go's errors.Is(err, gitwt.ErrNotGitRepo) drives the "git init
// it / add as plain" choice; without this the remote TUI would show a dead
// error string and the user could never reach that dialog.
var sentinels = map[string]error{"not_git_repo": gitwt.ErrNotGitRepo}

// codeFor returns the wire code for err, or "" if it isn't a sentinel.
func codeFor(err error) string {
	for code, sentinel := range sentinels {
		if errors.Is(err, sentinel) {
			return code
		}
	}
	return ""
}

// Args is a union of every parameter any Backend method takes; each method
// fills the subset it needs.
//
// ponytail: one union beats 29 per-method arg structs at this size. Split it
// if the surface doubles or two methods ever want the same field to mean
// different things.
type Args struct {
	ID   string   `json:"id,omitempty"`
	IDs  []string `json:"ids,omitempty"` // ReorderSessions
	Name string   `json:"name,omitempty"`
	// NewName is RenameFolder's target name; Name carries its oldName, same
	// as every other folder method's single Name/folder-name parameter.
	NewName    string `json:"new_name,omitempty"`
	Project    string `json:"project,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Ticket     string `json:"ticket,omitempty"`
	PR         string `json:"pr,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	Theme      string `json:"theme,omitempty"`
	Appearance string `json:"appearance,omitempty"`
	Delta      int    `json:"delta,omitempty"`
	// Req is CreateSession's whole request. One field rather than a dozen
	// flat ones, because creating a session is a transaction the core runs
	// end to end — see session.CreateRequest.
	Req *session.CreateRequest `json:"req,omitempty"`
	// Dangerous is SetSessionAgent's, always an explicit choice — the
	// tri-state "use the project's default" lives on Req.Dangerous instead.
	// Kept a pointer so a nil (which SetSessionAgent never sends) reads as
	// false rather than as something meaningful.
	Dangerous *bool          `json:"dangerous,omitempty"`
	On        bool           `json:"on,omitempty"` // archived / recentFirst / compact / autoTmux
	Proj      config.Project `json:"proj"`
}

// Result is the matching union of every return shape. Same trade as Args.
type Result struct {
	Session  *session.Session     `json:"session,omitempty"`
	Sessions []session.Session    `json:"sessions,omitempty"`
	Cfg      *config.Config       `json:"cfg,omitempty"`
	Agents   []config.AgentOption `json:"agents,omitempty"`
	Themes   []config.Theme       `json:"themes,omitempty"`
	Name     string               `json:"name,omitempty"`
	Hint     string               `json:"hint,omitempty"`
	Dirty    bool                 `json:"dirty,omitempty"`
	Unpushed bool                 `json:"unpushed,omitempty"`
	OK       bool                 `json:"ok,omitempty"`
	Files    int                  `json:"files,omitempty"`
	Commits  int                  `json:"commits,omitempty"`
}

// nudgeRequest is the only thing a client sends on a live "Watch"
// connection after the initial request: "give me a snapshot now". Snapshots
// themselves go the other way as sessionview.Snapshot, encoded as-is — the
// core has already done the deriving, so there's nothing left for a wire
// type to translate.
type nudgeRequest struct {
	Nudge bool `json:"nudge"`
}

// wireErr carries the server's error message while still unwrapping to the
// sentinel it came from, so errors.Is works on the client side.
type wireErr struct {
	msg      string
	sentinel error // nil for ordinary errors
}

func (e wireErr) Error() string { return e.msg }
func (e wireErr) Unwrap() error { return e.sentinel }
