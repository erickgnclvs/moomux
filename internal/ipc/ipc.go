// Package ipc puts a unix socket in the middle of tui.Backend: Server
// exposes a real backend, Client re-implements the same interface over the
// wire. The TUI can't tell them apart, which is the point — a second front
// end (a native macOS app, say) speaks the same JSON and gets the whole
// orchestration core without linking any Go.
//
// Wire format is one JSON request line in, one JSON response line out, then
// the connection closes. The exception is "Watch", which streams snapshot
// lines until the client hangs up.
package ipc

import (
	"errors"
	"time"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
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
	ID          string        `json:"id,omitempty"`
	Name        string        `json:"name,omitempty"`
	Project     string        `json:"project,omitempty"`
	Agent       string        `json:"agent,omitempty"`
	Branch      string        `json:"branch,omitempty"`
	BaseBranch  string        `json:"base_branch,omitempty"`
	Ticket      string        `json:"ticket,omitempty"`
	PR          string        `json:"pr,omitempty"`
	Prompt      string        `json:"prompt,omitempty"`
	Model       string        `json:"model,omitempty"`
	Thinking    string        `json:"thinking,omitempty"`
	Theme       string        `json:"theme,omitempty"`
	Appearance  string        `json:"appearance,omitempty"`
	TmuxSession string        `json:"tmux_session,omitempty"`
	Delta       int           `json:"delta,omitempty"`
	State       watcher.State `json:"state,omitempty"`
	// Dangerous is shared by CreateSession and SetSessionAgent. It's a
	// pointer so CreateSession's caller can leave it unset (nil, simply
	// omitted on the wire) to mean "use the project's own default" — a plain
	// bool can't express that, since an explicit false and an absent field
	// would be indistinguishable. SetSessionAgent's dangerous is always an
	// explicit choice, so its server-side handler treats a nil Dangerous
	// (which it never sends) the same as false.
	Dangerous    *bool          `json:"dangerous,omitempty"`
	OpenTerminal bool           `json:"open_terminal,omitempty"`
	AutoSubmit   bool           `json:"auto_submit,omitempty"`
	On           bool           `json:"on,omitempty"` // archived / recentFirst / compact / autoTmux
	Proj         config.Project `json:"proj,omitempty"`
}

// Result is the matching union of every return shape. Same trade as Args.
type Result struct {
	Session  *session.Session     `json:"session,omitempty"`
	Sessions []session.Session    `json:"sessions,omitempty"`
	Strings  []string             `json:"strings,omitempty"`
	Alive    map[string]bool      `json:"alive,omitempty"`
	PR       *prstatus.Info       `json:"pr,omitempty"`
	Cfg      *config.Config       `json:"cfg,omitempty"`
	Agents   []config.AgentOption `json:"agents,omitempty"`
	Hint     string               `json:"hint,omitempty"`
	Dirty    bool                 `json:"dirty,omitempty"`
	Unpushed bool                 `json:"unpushed,omitempty"`
	OK       bool                 `json:"ok,omitempty"`
	Files    int                  `json:"files,omitempty"`
	Commits  int                  `json:"commits,omitempty"`
}

// snapshotWire carries a watcher.Snapshot over JSON; Snapshot.Err is an
// error value and doesn't survive a round trip on its own.
type snapshotWire struct {
	States   map[string]watcher.State `json:"states"`
	PollTime time.Time                `json:"poll_time"`
	Err      string                   `json:"err,omitempty"`
}

// wireErr carries the server's error message while still unwrapping to the
// sentinel it came from, so errors.Is works on the client side.
type wireErr struct {
	msg      string
	sentinel error // nil for ordinary errors
}

func (e wireErr) Error() string { return e.msg }
func (e wireErr) Unwrap() error { return e.sentinel }
