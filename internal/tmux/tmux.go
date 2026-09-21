// Package tmux wraps the tmux CLI behind an injectable runner.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
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

	mu        sync.Mutex
	agentWins map[string]string // tmux session name -> target for the agent's window
}

func New() *Client { return &Client{Runner: ExecRunner()} }

// Exact returns a tmux target that matches the session name exactly. A bare
// name in -t falls back to *prefix* matching when no exact match exists, so
// e.g. `kill-session -t moomux-feat` kills moomux-feat-2 once moomux-feat is
// gone. The "=" sigil disables that. Only valid for commands taking a
// session target (has-session, kill-session, attach-session) — commands
// taking a window/pane target reject a bare "=name"; use firstWindow/agentWindow there.
func Exact(name string) string { return "=" + name }

// firstWindow returns an exact-match target for the session's *first*
// window, for commands that take a window/pane target (set-option,
// split-window, list-panes, ...): the trailing ":" marks the "=name" part as
// a session, and "^" picks the first window.
//
// Not ":" (the current window): ReviewWindow adds a second window and
// selects it, and the user can switch tabs anyway, so every call here —
// capture-pane, rename-window for the status title, send-keys, paste-buffer
// — would follow them and read or type into the wrong pane. Callers that
// mean the agent's window want agentWindow, which is the first one only
// when a layout hasn't put the agent somewhere else.
func firstWindow(name string) string { return "=" + name + ":^" }

// agentWindowOption marks the agent's window so it can be found again by a
// process that didn't create the session — a `moomux serve` core restarted
// under a still-running tmux server. A tmux user option, so tmux itself
// stores it for the window's lifetime and moomux needs no state on disk.
const agentWindowOption = "@moomux_agent"

// agentWindow returns a window/pane target for the window the agent runs in.
//
// Which is the first window for a plain session, but layouts can put the
// agent leaf in any window (see NewSessionWithLayout), and typing a first
// prompt into some other window's pane — or reading its cwd, which decides
// whether App.EnsureTmux kills and recreates a live session — is how that
// goes wrong. Resolved once per session and cached: the lookup costs a tmux
// fork and these calls run on every poll tick.
func (c *Client) agentWindow(session string) string {
	c.mu.Lock()
	t, cached := c.agentWins[session]
	c.mu.Unlock()
	if cached {
		return t
	}
	// Deliberately outside c.mu: this forks tmux, and c.mu is on the path of
	// every call below. Holding it here serialized a cold Capture of N
	// sessions into N subprocesses back to back, with the watcher and any
	// incoming Attach queued behind them. Two racing lookups cost one extra
	// fork and agree on the answer.
	target := firstWindow(session)
	out, err := c.Runner.Run("list-windows", "-t", Exact(session), "-F", "#{window_id} #{"+agentWindowOption+"}")
	if err != nil {
		// Uncached: one transient failure would otherwise pin a layout
		// session to the wrong window for the life of the process, and
		// runAgent's forget-on-error can't undo it — commands aimed at
		// the first window succeed, they just target the wrong pane.
		return target
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if id, mark, ok := strings.Cut(line, " "); ok && mark == "1" {
			target = id // a window id is a complete target on its own
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cacheAgentWindowLocked(session, target)
	return target
}

// agentTarget is the placeholder runAgent replaces with the session's
// agent-window target. Not a real tmux target, so a call site that forgets
// to go through runAgent fails loudly rather than targeting something else.
const agentTarget = "\x00moomux-agent-window"

// runAgent runs a tmux command against session's agent window.
//
// A failure drops the cached target: for a layout session that target is a
// concrete window id, and the agent exiting closes its only pane and with it
// that window, while the tmux session lives on. Without this every later
// call would keep aiming at the dead id — title updates silently lost,
// captures and first prompts failing forever — until the session was killed
// or the core restarted. The failed call still fails; the next one resolves
// again.
func (c *Client) runAgent(session string, args ...string) (string, error) {
	target := c.agentWindow(session)
	argv := slices.Clone(args)
	for i, a := range argv {
		if a == agentTarget {
			argv[i] = target
		}
	}
	out, err := c.Runner.Run(argv...)
	if err != nil {
		c.forgetAgentWindow(session)
	}
	return out, err
}

// markAgentWindow records pane's window as the agent's, both in tmux (for
// another process) and in this client's cache (so the common path never
// needs the lookup).
func (c *Client) markAgentWindow(session, pane string) {
	_, _ = c.Runner.Run("set-window-option", "-t", pane, agentWindowOption, "1")
	out, err := c.Runner.Run("display-message", "-p", "-t", pane, "#{window_id}")
	id := strings.TrimSpace(out)
	if err != nil || id == "" {
		// Drop the cached target first: newSessionBase already seeded it
		// with the session's first window, so leaving it there means
		// agentWindow answers from cache and never runs the lookup that
		// would find the option we just set.
		c.forgetAgentWindow(session)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cacheAgentWindowLocked(session, id)
}

// agentWinsMax bounds the cache. Nothing evicts the entry for a session
// that died outside KillSession/RenameSession — a tmux server restart, a
// user's own kill-session — and CapturePanes inserts one for every name it
// is ever asked about, so a long-lived `moomux serve` would otherwise grow
// this forever. Drop the lot rather than tracking ages: the cost is one
// list-windows per live session, once.
const agentWinsMax = 256

func (c *Client) cacheAgentWindowLocked(session, target string) {
	if c.agentWins == nil {
		c.agentWins = map[string]string{}
	}
	if len(c.agentWins) >= agentWinsMax {
		clear(c.agentWins)
	}
	c.agentWins[session] = target
}

func (c *Client) forgetAgentWindow(session string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.agentWins, session)
}

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
		_, _ = c.Runner.Run("set-window-option", "-t", firstWindow(name), "automatic-rename", "off")
		// Make tmux continuously push the window name as the terminal title so
		// the shell's own PROMPT_COMMAND/precmd title updates don't win the race.
		_, _ = c.Runner.Run("set-option", "-t", firstWindow(name), "set-titles", "on")
		_, _ = c.Runner.Run("set-option", "-t", firstWindow(name), "set-titles-string", "#{window_name}")
	}
	// Enable mouse support so users can click/scroll/resize panes without
	// memorizing tmux prefix keybindings.
	_, _ = c.Runner.Run("set-option", "-t", firstWindow(name), "mouse", "on")
	// One window so far, and the agent's: seeding the cache keeps every
	// later call off agentWindow's lookup (NewSessionWithLayout overwrites
	// this once it knows where the agent leaf landed).
	c.mu.Lock()
	c.cacheAgentWindowLocked(name, firstWindow(name))
	c.mu.Unlock()
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
	leftPane, err := c.runAgent(name, "list-panes", "-t", agentTarget, "-F", "#{pane_id}")
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
	if _, err := c.runAgent(name, "split-window", "-h", "-t", agentTarget, "-c", cwd, "-l", "33%"); err != nil {
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
	_, err := c.runAgent(session, "send-keys", "-t", agentTarget, "Enter")
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
// paste-buffer instead hands the terminal one atomic block, delimited with
// bracketed-paste markers when the other end asked for them, which tmux (and
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
	// -p wraps the text in bracketed-paste control codes (ESC[200~ / ESC[201~)
	// when the receiving application has asked for them, which is what makes
	// the paste unambiguous: without it the agent CLI only sees a burst of
	// ordinary input and has to guess it was a paste from arrival timing —
	// guess wrong and a multi-line prompt's newlines each submit, so only the
	// first line lands as the prompt. It's a no-op against an application that
	// hasn't requested bracketed paste, so it's always safe to pass.
	// -d deletes the buffer immediately after pasting, so it doesn't linger
	// in tmux's paste-buffer stack (visible to, and reusable by, anything
	// else in the session via prefix-]).
	_, err = c.runAgent(session, "paste-buffer", "-p", "-d", "-t", agentTarget)
	return err
}

// BracketedPaste reports whether session's active pane currently has
// bracketed-paste mode enabled, i.e. whether the program on the other end
// has told the terminal it wants pastes delimited. It's the closest thing
// tmux exposes to "a TUI is up and reading input": an agent CLI's startup
// (before its input layer is installed) has it off, and the shell turns it
// off for the duration of a command it runs, so it flips back on only once
// the agent itself is taking keystrokes. See App.StartFirstPrompt.
func (c *Client) BracketedPaste(session string) (bool, error) {
	out, err := c.runAgent(session, "display-message", "-p", "-t", agentTarget, "#{bracket_paste_flag}")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "1", nil
}

// SetWindowName renames session's window. ConfigureTitleTracking already
// turned on set-titles/set-titles-string for the window, so terminals whose
// tab title tracks it (iTerm2, kitty, etc.) pick up the new name automatically.
func (c *Client) SetWindowName(session, name string) error {
	_, err := c.runAgent(session, "rename-window", "-t", agentTarget, name)
	return err
}

// WindowName returns session's current tmux window name, e.g. to preserve a
// user's manual rename when only a status prefix needs updating.
func (c *Client) WindowName(session string) (string, error) {
	out, err := c.runAgent(session, "display-message", "-p", "-t", agentTarget, "#{window_name}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ConfigureTitleTracking ensures the tmux session keeps its window name stable
// and continuously emits it as the terminal title. Safe to call on existing
// sessions — idempotent tmux set-option calls never break anything.
func (c *Client) ConfigureTitleTracking(session, windowName string) {
	_, _ = c.runAgent(session, "rename-window", "-t", agentTarget, windowName)
	_, _ = c.runAgent(session, "set-window-option", "-t", agentTarget, "automatic-rename", "off")
	_, _ = c.runAgent(session, "set-option", "-t", agentTarget, "set-titles", "on")
	_, _ = c.runAgent(session, "set-option", "-t", agentTarget, "set-titles-string", "#{window_name}")
	_, _ = c.runAgent(session, "set-option", "-t", agentTarget, "mouse", "on")
}

// PaneCwd returns the current working directory of session `name`'s first
// pane. tmux silently falls back to its own launch cwd when a requested
// -c directory doesn't exist (e.g. a worktree not yet created), so this is
// used to detect sessions that ended up in the wrong place.
func (c *Client) PaneCwd(name string) (string, error) {
	out, err := c.runAgent(name, "list-panes", "-t", agentTarget, "-F", "#{pane_current_path}")
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
	return c.runAgent(name, "capture-pane", "-p", "-t", agentTarget)
}

// RenameSession renames a live tmux session in place.
func (c *Client) RenameSession(old, new string) error {
	c.forgetAgentWindow(old)
	_, err := c.Runner.Run("rename-session", "-t", Exact(old), new)
	return err
}

func (c *Client) KillSession(name string) error {
	c.forgetAgentWindow(name)
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

// captureMarker delimits one session's screen in CapturePanes' batched
// output. It carries the session's *index*, not its name: tmux expands
// #{...} in a display-message, and an index can't contain one.
//
// The random half is there because splitCaptures reads any captured line
// that starts with this as a section boundary, and a pane's visible text is
// captured verbatim — someone with this file or a capture dump on screen
// would otherwise truncate their own tile at that line. Once per process,
// not per call: it only has to be unguessable from outside, and a stable
// value keeps the runner's command string predictable for the tests.
var captureMarker = fmt.Sprintf("@@moomux-capture-%d@@", rand.Uint64())

// CapturePanes returns the visible text of each named session's active pane,
// keyed by session name, in one tmux invocation rather than one process per
// session — the session grid polls every live session every few seconds, and
// the process churn was the whole expense of that design.
//
// A session missing from the result is one that couldn't be captured (it
// died between the poll and the capture, say); callers keep whatever they
// last drew rather than blanking the tile.
func (c *Client) CapturePanes(names []string) map[string]string {
	out := make(map[string]string, len(names))
	if len(names) == 0 {
		return out
	}
	// tmux abandons the rest of a command sequence at the first error, so a
	// session that died takes every section after it down with it. Retry
	// what the first pass missed as one more batch: the sessions a caller
	// hands over are long-lived and a dead one is usually the same dead one
	// every tick, so the second pass covers everything behind it for one
	// extra fork rather than one per session, forever.
	//
	// Two passes, so a third dead session still reaches the per-session
	// path below; looping until no progress would fix that too, at N forks
	// when everything is dead. Not worth it.
	pending := names
	for range 2 {
		c.captureBatch(pending, out)
		pending = nil
		for _, name := range names {
			if _, ok := out[name]; !ok {
				pending = append(pending, name)
			}
		}
		if len(pending) == 0 {
			break
		}
		// The first session still missing is the one that broke that pass:
		// everything before it was captured and everything after it was
		// abandoned. Retrying it would fail the same way, so it goes to the
		// per-session fallback and the rest get their batch.
		pending = pending[1:]
		if len(pending) == 0 {
			break
		}
	}
	// Whatever the batch skipped gets one process of its own, which is at
	// worst what this used to cost.
	for _, name := range names {
		if _, ok := out[name]; ok {
			continue
		}
		if screen, err := c.CapturePane(name); err == nil {
			// Match the batch path, which drops the newline tmux writes
			// before the next marker. Otherwise a tile gains or loses a
			// blank row purely from whether its session made this tick's
			// batch — which flips whenever an earlier one momentarily fails.
			out[name] = strings.TrimSuffix(screen, "\n")
		}
	}
	return out
}

// captureBatch captures names in one tmux invocation, adding whatever came
// back whole to out.
func (c *Client) captureBatch(names []string, out map[string]string) {
	var args []string
	for i, name := range names {
		args = append(args, "display-message", "-p", fmt.Sprintf("%s%d", captureMarker, i), ";",
			"capture-pane", "-p", "-t", c.agentWindow(name), ";")
	}
	// A closing marker, so every section is delimited on both sides: an
	// unterminated last section is how a capture that failed midway is told
	// apart from one that worked.
	args = append(args, "display-message", "-p", captureMarker+"end")
	// The error is deliberately ignored: tmux exits non-zero for the one bad
	// session, and the sections it did print before giving up are good (the
	// closing marker is what makes a truncated one unusable, not the exit
	// status).
	text, _ := c.Runner.Run(args...)
	for i, screen := range splitCaptures(text) {
		if i >= 0 && i < len(names) {
			out[names[i]] = screen
		}
	}
}

// splitCaptures pulls the marked sections out of CapturePanes' batched
// output, keyed by the index each marker carries. Only sections closed by a
// following marker are returned — see CapturePanes on why.
func splitCaptures(text string) map[int]string {
	out := map[int]string{}
	idx, have := -1, false
	var cur []string
	for _, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(line, captureMarker)
		if !ok {
			if have {
				cur = append(cur, line)
			}
			continue
		}
		if have {
			// No trailing row is dropped here: capture-pane terminates
			// every row it prints, including the pane's blank ones, and
			// the Split above already consumed the last of those newlines
			// as the separator before this marker.
			out[idx] = strings.Join(cur, "\n")
		}
		n, err := strconv.Atoi(rest)
		idx, have, cur = n, err == nil, nil
	}
	return out
}

// ReviewWindow runs script in a window named "review" in session, reusing an
// existing one.
//
// respawn-window -k and not kill-then-create: killing the last window of a
// session kills the session. It doesn't select the window, hence the second
// command; new-window does.
//
// "=" on both halves of the target: a bare window name prefix-matches like a
// session name does, so ":review" would respawn a window called "reviewers"
// or "review-notes" — replacing whatever it was running — instead of failing
// through to new-window.
//
// Both paths leave the review window selected, and that is the point: the
// caller is remote (nothing in the TUI calls this) and attaches afterwards,
// which lands on whatever window the session has current. The cost is that
// a desktop watching the same session gets moved too — if a caller ever
// wants the window without the jump, that's a flag, not a default.
func (c *Client) ReviewWindow(session, cwd, script string) error {
	if target := c.reviewWindow(session); target != "" {
		if _, err := c.Runner.Run("respawn-window", "-k", "-t", target, "-c", cwd, script); err == nil {
			_, err := c.Runner.Run("select-window", "-t", target)
			return err
		}
	}
	// -n also turns automatic-rename off for the window, so the tab keeps
	// saying "review" and not "zsh". -P -F prints the new window's id, which
	// is what gets marked; new-window selects it, so there is no
	// select-window on this path.
	out, err := c.Runner.Run("new-window", "-P", "-F", "#{window_id}", "-t", Exact(session), "-c", cwd, "-n", "review", script)
	if err != nil {
		return err
	}
	if id := strings.TrimSpace(out); id != "" {
		_, _ = c.Runner.Run("set-window-option", "-t", id, reviewWindowOption, "1")
	}
	return nil
}

// reviewWindowOption marks a window as one ReviewWindow created, the same
// way agentWindowOption marks the agent's. Not cosmetic: respawn-window -k
// kills whatever the target is running, and addressing it by name alone
// would blow away a window a user's own layout happened to call "review".
const reviewWindowOption = "@moomux_review"

// reviewWindow returns the id of session's marked review window, or "" if
// it has none. Uncached, unlike agentWindow: this runs once per Review,
// which is a keypress, not a poll tick.
func (c *Client) reviewWindow(session string) string {
	out, err := c.Runner.Run("list-windows", "-t", Exact(session), "-F", "#{window_id} #{"+reviewWindowOption+"}")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if id, mark, ok := strings.Cut(line, " "); ok && mark == "1" {
			return id
		}
	}
	return ""
}

// AttachCmd is the command that attaches one client to session, for a caller
// that owns a pty to run it on (see internal/ipc's Attach). Not a Runner
// call: attaching is an interactive byte stream, not a captured one-shot.
func AttachCmd(session string) *exec.Cmd {
	// -d detaches whoever else is attached. Without it this is a second
	// client on the same session, and tmux's default window-size latest
	// reflows the shared window to the newest client — a phone attaching
	// would shrink the desktop's window to 40 columns and leave it there.
	// $TMUX is "<socket path>,<pid>,<session index>", and it is also how
	// tmux finds its server when -L/-S aren't given. Stripping it below is
	// required (tmux refuses to nest) but takes the server with it, so a
	// core started inside a `tmux -L foo` session would attach to the
	// *default* server while every Runner call here still talks to foo.
	var args []string
	if sock, _, _ := strings.Cut(os.Getenv("TMUX"), ","); sock != "" {
		args = append(args, "-S", sock)
	}
	args = append(args, "attach", "-d", "-t", Exact(session))
	cmd := exec.Command("tmux", args...)
	// A `moomux serve` started from inside tmux has $TMUX set and tmux
	// refuses to nest. The pty here is not a tmux pane, so the inherited
	// value is simply wrong — as is TERM, which describes whatever terminal
	// launched the core rather than the one now on the other end.
	env := os.Environ()
	env = slices.DeleteFunc(env, func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return name == "TMUX" || name == "TMUX_PANE" || name == "TERM"
	})
	// ponytail: one fixed TERM. Take it from the client if a real terminal
	// ever needs something else — the terminfo entry has to exist on this
	// machine, which is the reason not to trust whatever the phone says.
	cmd.Env = append(env, "TERM=xterm-256color")
	return cmd
}
