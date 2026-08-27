// Package tmux wraps the tmux CLI behind an injectable runner.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// runTimeout bounds every tmux subprocess execRunner spawns. Client methods
// don't carry a caller context, so an unresponsive tmux server is bounded
// by a fixed timeout instead of hanging the whole app forever.
var runTimeout = 10 * time.Second

type Runner interface {
	Run(args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", args...)
	// A bare `tmux` command with no live server spawns one, and that server
	// inherits this process's cwd as its permanent launch directory — used
	// as tmux's silent fallback whenever a session's own -c doesn't stick
	// (see PaneCwd's doc comment). If moomux is ever run from inside one of
	// its own managed worktrees, that directory can later be deleted,
	// permanently poisoning every future session on the same server with a
	// dead fallback cwd. Home is stable for the tmux server's whole lifetime.
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	// Without WaitDelay, CombinedOutput can still block past ctx's
	// deadline: if tmux forked a child that inherited the output pipe,
	// killing tmux alone doesn't close it — Read() waits for every process
	// holding the write end to exit, not just the one we canceled.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func ExecRunner() Runner { return execRunner{} }

type Client struct {
	Runner Runner
}

func New() *Client { return &Client{Runner: ExecRunner()} }

// Exact returns a tmux target that matches the session name exactly. A bare
// name in -t falls back to *prefix* matching when no exact match exists, so
// e.g. `kill-session -t moomux-feat` kills moomux-feat-2 once moomux-feat is
// gone. The "=" sigil disables that. Only valid for commands taking a
// session target (has-session, kill-session, attach-session) — commands
// taking a window/pane target reject a bare "=name"; use exactWindow there.
func Exact(name string) string { return "=" + name }

// exactWindow returns an exact-match target for commands that take a
// window/pane target (set-option, split-window, list-panes, ...): the
// trailing ":" marks the "=name" part as a session, selecting its current
// window.
func exactWindow(name string) string { return "=" + name + ":" }

// HasSession reports whether tmux session `name` exists.
func (c *Client) HasSession(name string) (bool, error) {
	out, err := c.Runner.Run("has-session", "-t", Exact(name))
	if err == nil {
		return true, nil
	}
	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return false, err
	}
	diagnostic := strings.TrimSpace(out)
	lower := strings.ToLower(diagnostic)
	if diagnostic == "" || strings.Contains(lower, "can't find session") || strings.Contains(lower, "no server running") {
		return false, nil
	}
	return false, fmt.Errorf("%s: %w", diagnostic, err)
}

// LiveSessions returns the set of currently running tmux session names via a
// single list-sessions call — much cheaper than N HasSession calls.
func (c *Client) LiveSessions() map[string]bool {
	out, err := c.Runner.Run("list-sessions", "-F", "#{session_name}")
	result := map[string]bool{}
	if err != nil {
		// tmux exits non-zero when no sessions exist; that's fine
		return result
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			result[line] = true
		}
	}
	return result
}

// EnsureEnvRefresh keeps the tmux server's notion of MOSHI_CLIENT in sync
// with reality, so browser.Remote() doesn't false-positive from a stale
// value. Two separate tmux mechanisms need fixing:
//
//  1. update-environment only refreshes a *session's own* local environment
//     table, and only on that session's next attach. A var missing from
//     that option's list (as MOSHI_CLIENT is by default) is never refreshed
//     at all — captured once and stuck for that session's whole lifetime.
//  2. Even with that fixed, brand-new sessions/windows (which start with no
//     session-local override) fall back to the tmux *server's global*
//     environment table — captured once when the server first started and
//     never touched by update-environment, so it stays stuck forever
//     regardless of what any later client's own environment looks like.
//
// This appends MOSHI_CLIENT to update-environment (fixing case 1 for this
// session's future attaches) and pushes this process's own live
// MOSHI_CLIENT value into the global table (fixing case 2 for every new
// session/window from now on) — the value moomux itself was just launched
// with is the freshest signal available for what the global table should
// hold. No-op outside tmux.
func (c *Client) EnsureEnvRefresh() error {
	if os.Getenv("TMUX") == "" {
		return nil
	}
	out, err := c.Runner.Run("show-options", "-g", "update-environment")
	if err != nil {
		return err
	}
	if !strings.Contains(out, "MOSHI_CLIENT") {
		if _, err := c.Runner.Run("set-option", "-ga", "update-environment", "MOSHI_CLIENT"); err != nil {
			return err
		}
	}
	if v := os.Getenv("MOSHI_CLIENT"); v != "" {
		_, err = c.Runner.Run("set-environment", "-g", "MOSHI_CLIENT", v)
	} else {
		_, err = c.Runner.Run("set-environment", "-gu", "MOSHI_CLIENT")
	}
	return err
}

// newSessionBase creates a detached tmux session at cwd with the
// window-name/title/mouse setup shared by NewSession and NewSessionWithLayout.
// If windowName is non-empty it is set as the initial window name via -n so
// terminals that read the tmux title (iTerm2, kitty, etc.) display it immediately.
// automatic-rename is disabled so the name is not overwritten by the shell.
func (c *Client) newSessionBase(name, cwd, windowName string) error {
	args := []string{"new-session", "-d", "-s", name, "-c", cwd}
	if windowName != "" {
		args = append(args, "-n", windowName)
	}
	if _, err := c.Runner.Run(args...); err != nil {
		return err
	}
	if windowName != "" {
		// Keep the window name stable; without this tmux replaces it with the
		// running process name (e.g. "bash") as soon as the shell starts.
		_, _ = c.Runner.Run("set-window-option", "-t", exactWindow(name), "automatic-rename", "off")
		// Make tmux continuously push the window name as the terminal title so
		// the shell's own PROMPT_COMMAND/precmd title updates don't win the race.
		_, _ = c.Runner.Run("set-option", "-t", exactWindow(name), "set-titles", "on")
		_, _ = c.Runner.Run("set-option", "-t", exactWindow(name), "set-titles-string", "#{window_name}")
	}
	// Enable mouse support so users can click/scroll/resize panes without
	// memorizing tmux prefix keybindings.
	_, _ = c.Runner.Run("set-option", "-t", exactWindow(name), "mouse", "on")
	return nil
}

// NewSession creates a detached tmux session at cwd, split into two
// side-by-side panes: a left pane (~2/3 width) running `cmd`, and a right
// pane (~1/3 width) left as a plain interactive shell.
// If cmd is empty, no command is sent to the left pane.
func (c *Client) NewSession(name, cwd, cmd, windowName string) error {
	if err := c.newSessionBase(name, cwd, windowName); err != nil {
		return err
	}
	// Capture the original (left) pane's stable pane_id before splitting.
	// We can't assume its index is 0: a user's tmux.conf may set
	// pane-base-index to 1 (as this README itself recommends), which would
	// make a hardcoded ".0" target fail with "can't find pane".
	leftPane, err := c.Runner.Run("list-panes", "-t", exactWindow(name), "-F", "#{pane_id}")
	if err != nil {
		return err
	}
	leftPane = strings.TrimSpace(leftPane)
	// Split the window horizontally (side by side): the new pane takes 33% of
	// the width, leaving the original (left) pane at roughly 2/3. Uses -l
	// with a percentage rather than the older -p: -p sizes relative to an
	// attached client's last-known size, which doesn't exist yet for a
	// brand-new detached session (new-session -d) and fails with "size
	// missing"; -l sizes off the window's own current dimensions instead.
	if _, err := c.Runner.Run("split-window", "-h", "-t", exactWindow(name), "-c", cwd, "-l", "33%"); err != nil {
		return err
	}
	// split-window moves focus to the new (right) pane; return focus to the
	// left pane before sending the agent command into it.
	if _, err := c.Runner.Run("select-pane", "-t", leftPane); err != nil {
		return err
	}
	if cmd != "" {
		if _, err := c.Runner.Run("send-keys", "-t", leftPane, cmd, "Enter"); err != nil {
			return err
		}
	}
	return nil
}

// PressEnter sends a bare Enter keypress to session's active pane, with no
// text — must be its own step (see PasteText's doc comment for why bundling
// it with the text is unreliable).
func (c *Client) PressEnter(session string) error {
	_, err := c.Runner.Run("send-keys", "-t", exactWindow(session), "Enter")
	return err
}

// PasteText delivers text into session's active pane via tmux's paste
// buffer (load-buffer + paste-buffer) rather than send-keys, with no
// trailing Enter. Two problems with send-keys -l made this necessary:
//
//  1. send-keys -l submits text as a sequence of individual synthetic
//     keystrokes, not a paste — a multi-line prompt's embedded newlines each
//     arrive as their own Enter keypress, so the receiving CLI can submit
//     (and start acting on) an incomplete prefix of the prompt partway
//     through instead of receiving the whole thing as one entry.
//  2. Long text passed as a single argv argument can also just exceed
//     tmux/exec argument-length limits, silently truncating the prompt.
//
// paste-buffer instead hands the terminal one atomic block, which tmux (and
// any bracketed-paste-aware readline/TUI on the other end) delivers and
// renders as a single paste — embedded newlines can't be mistaken for
// separate Enter presses. Deliberately still not bundled with Enter: many
// terminal-raw-mode TUIs (Ink, readline) detect "paste" by how a chunk of
// input arrives, and a whole text+Enter burst delivered together commonly
// gets swallowed as pasted content instead of text-then-submit — the Enter
// never registers as a keypress. Sending text and Enter as separate steps
// (see PressEnter), with a short gap between them, is the pattern that
// actually submits.
func (c *Client) PasteText(session, text string) error {
	f, err := os.CreateTemp("", "moomux-paste-*")
	if err != nil {
		return fmt.Errorf("paste text: %w", err)
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)
	_, writeErr := f.WriteString(text)
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("paste text: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("paste text: %w", closeErr)
	}
	if _, err := c.Runner.Run("load-buffer", tmpPath); err != nil {
		return err
	}
	// -d deletes the buffer immediately after pasting, so it doesn't linger
	// in tmux's paste-buffer stack (visible to, and reusable by, anything
	// else in the session via prefix-]).
	_, err = c.Runner.Run("paste-buffer", "-d", "-t", exactWindow(session))
	return err
}

// SetWindowName renames session's window. ConfigureTitleTracking already
// turned on set-titles/set-titles-string for the window, so terminals whose
// tab title tracks it (iTerm2, kitty, etc.) pick up the new name automatically.
func (c *Client) SetWindowName(session, name string) error {
	_, err := c.Runner.Run("rename-window", "-t", exactWindow(session), name)
	return err
}

// WindowName returns session's current tmux window name, e.g. to preserve a
// user's manual rename when only a status prefix needs updating.
func (c *Client) WindowName(session string) (string, error) {
	out, err := c.Runner.Run("display-message", "-p", "-t", exactWindow(session), "#{window_name}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ConfigureTitleTracking ensures the tmux session keeps its window name stable
// and continuously emits it as the terminal title. Safe to call on existing
// sessions — idempotent tmux set-option calls never break anything.
func (c *Client) ConfigureTitleTracking(session, windowName string) {
	_, _ = c.Runner.Run("rename-window", "-t", exactWindow(session), windowName)
	_, _ = c.Runner.Run("set-window-option", "-t", exactWindow(session), "automatic-rename", "off")
	_, _ = c.Runner.Run("set-option", "-t", exactWindow(session), "set-titles", "on")
	_, _ = c.Runner.Run("set-option", "-t", exactWindow(session), "set-titles-string", "#{window_name}")
	_, _ = c.Runner.Run("set-option", "-t", exactWindow(session), "mouse", "on")
}

// PaneCwd returns the current working directory of session `name`'s first
// pane. tmux silently falls back to its own launch cwd when a requested
// -c directory doesn't exist (e.g. a worktree not yet created), so this is
// used to detect sessions that ended up in the wrong place.
func (c *Client) PaneCwd(name string) (string, error) {
	out, err := c.Runner.Run("list-panes", "-t", exactWindow(name), "-F", "#{pane_current_path}")
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return lines[0], nil
}

// CapturePane returns the visible text of session `name`'s active pane, used
// to detect when an agent CLI has finished its startup render and is idle
// waiting for input (see App.StartFirstPrompt).
func (c *Client) CapturePane(name string) (string, error) {
	return c.Runner.Run("capture-pane", "-p", "-t", exactWindow(name))
}

// RenameSession renames a live tmux session in place.
func (c *Client) RenameSession(old, new string) error {
	_, err := c.Runner.Run("rename-session", "-t", Exact(old), new)
	return err
}

func (c *Client) KillSession(name string) error {
	_, err := c.Runner.Run("kill-session", "-t", Exact(name))
	return err
}

// CurrentSessionName returns the tmux session name of the pane this process
// is running in (no -t target: tmux resolves it from $TMUX/$TMUX_PANE in the
// environment, which the child process inherits). Errors when not run
// inside a tmux client, e.g. $TMUX unset.
func (c *Client) CurrentSessionName() (string, error) {
	out, err := c.Runner.Run("display-message", "-p", "#S")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
