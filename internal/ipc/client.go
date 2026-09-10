package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/sessionview"
	"github.com/erickgnclvs/moomux/internal/tui"
)

// Client speaks to a Server over a unix socket. It implements tui.Backend,
// so the TUI runs against a remote core with no other changes, and
// sessionview.Source, so the derived per-session state streams from the same
// place — already joined, labelled and status-checked by the core.
type Client struct {
	Socket string

	nudgeOnce sync.Once
	nudgeCh   chan struct{}

	// mu guards the last-good cache below. tui.Backend's Sessions has no
	// error return — it's called from rendering paths, which have nowhere
	// to put one — so a failed call would
	// otherwise return nil and read as "everything was deleted" — the list
	// would empty out mid-render on one failed call. Returning the last
	// known-good answer keeps the UI stale-but-true; Run is what tells the
	// user the connection is down.
	mu           sync.Mutex
	lastSessions []session.Session
	lastCfg      config.Config
}

var (
	_ tui.Backend        = (*Client)(nil)
	_ sessionview.Source = (*Client)(nil)
)

// call dials, sends one request, reads one response, closes. No pooling and
// no multiplexing: a unix connect is tens of microseconds, and independent
// connections mean a slow CreateSession never queues behind a status poll.
//
// ponytail: connection-per-call. Pool it if profiling ever shows the dial
// cost mattering.
func (c *Client) call(method string, a Args) (Result, error) {
	conn, err := net.Dial("unix", c.Socket)
	if err != nil {
		return Result{}, err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(request{Method: method, Args: a}); err != nil {
		return Result{}, err
	}
	var res response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&res); err != nil {
		return Result{}, err
	}
	if res.Err != "" {
		return res.Result, wireErr{msg: res.Err, sentinel: sentinels[res.Code]}
	}
	return res.Result, nil
}

// mut wraps the calls that change server-side config. The server attaches
// its post-mutation config snapshot to the same response (see
// Server.mutResult), which mut caches into lastCfg for ConfigSnapshot to
// hand back — that's the TUI's own job to apply, on its own Update()
// goroutine (see tui.Backend.ConfigSnapshot's doc comment), so this must
// never refresh a shared *config.Config pointer in place itself.
func (c *Client) mut(method string, a Args) error {
	_, err := c.mutResult(method, a)
	return err
}

// mutResult is mut, keeping the response so a caller that needs more than
// "did it work" (AddProject's path warning) can read it.
func (c *Client) mutResult(method string, a Args) (Result, error) {
	r, err := c.call(method, a)
	if r.Cfg != nil {
		c.mu.Lock()
		c.lastCfg = *r.Cfg
		c.mu.Unlock()
	}
	return r, err
}

// err0 is for the many methods whose only return is an error.
func (c *Client) err0(method string, a Args) error {
	_, err := c.call(method, a)
	return err
}

// sess is for the methods returning (session.Session, error).
func (c *Client) sess(method string, a Args) (session.Session, error) {
	r, err := c.call(method, a)
	if r.Session == nil {
		return session.Session{}, err
	}
	return *r.Session, err
}

// Config fetches the server's config so a client can render without reading
// the config file itself. Not part of tui.Backend — the TUI takes *Config
// separately at construction.
func (c *Client) Config() (*config.Config, error) {
	r, err := c.call("Config", Args{})
	if err != nil {
		return nil, err
	}
	if r.Cfg == nil {
		// Result.Cfg is omitempty, so a server with no config (or any other
		// implementation of this protocol) would otherwise hand back a nil
		// the TUI immediately dereferences.
		return nil, errors.New("server returned no config")
	}
	// Cached here (not just in ConfigSnapshot) so main.go's initial fetch
	// already seeds lastCfg — otherwise a transient failure on the very
	// first post-mutation ConfigSnapshot call would fall back to a
	// still-zero-value config and wipe every project/setting from the TUI.
	c.mu.Lock()
	c.lastCfg = *r.Cfg
	c.mu.Unlock()
	return r.Cfg, nil
}

// ConfigSnapshot implements tui.Backend — see that interface's doc comment.
// Every mutator already caches the server's post-mutation config into
// lastCfg via mut (see Server.mutResult) — that's the whole point of the
// server attaching it to the same response, rather than requiring a second
// "Config" round trip here — so this just hands that back.
func (c *Client) ConfigSnapshot() config.Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastCfg
}

// AgentOptions fetches the server's agent/model/thinking-level tables. Not
// part of tui.Backend — like Config, the TUI fetches it once at startup
// rather than on every render.
func (c *Client) AgentOptions() ([]config.AgentOption, error) {
	r, err := c.call("AgentOptions", Args{})
	if err != nil {
		return nil, err
	}
	return r.Agents, nil
}

// Themes fetches the server's color palettes — the agent-state colors a
// front end renders, and the list a theme picker offers. Like AgentOptions,
// it isn't part of tui.Backend: fetched once at startup, not per render.
func (c *Client) Themes() ([]config.Theme, error) {
	r, err := c.call("Themes", Args{})
	if err != nil {
		return nil, err
	}
	return r.Themes, nil
}

func (c *Client) Sessions() []session.Session {
	r, err := c.call("Sessions", Args{})
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		return c.lastSessions
	}
	c.lastSessions = r.Sessions
	return r.Sessions
}

// SuggestedProject asks the core, not this process: over the socket the repo
// path has to exist on the server's machine.
func (c *Client) SuggestedProject() (string, string) {
	r, err := c.call("SuggestedProject", Args{})
	if err != nil {
		return "", ""
	}
	return r.Name, r.Hint
}

func (c *Client) CreateSession(req session.CreateRequest) (session.Session, string, error) {
	r, err := c.call("CreateSession", Args{Req: &req})
	if r.Session == nil {
		return session.Session{}, r.Hint, err
	}
	return *r.Session, r.Hint, err
}

// OpenSession is EnsureTmux over the wire: there is no OpenSession method
// any more, because the core does not open terminals — a front end does
// (see main.go's terminalBackend, which overrides this). It stays only so
// Client satisfies tui.Backend and can be wrapped.
func (c *Client) OpenSession(id string) (string, error) {
	return c.EnsureTmux(id)
}

func (c *Client) EnsureTmux(id string) (string, error) {
	r, err := c.call("EnsureTmux", Args{ID: id})
	return r.Hint, err
}

func (c *Client) DeleteSession(id string) (string, error) {
	r, err := c.call("DeleteSession", Args{ID: id})
	return r.Hint, err
}

func (c *Client) KillTmux(id string) error { return c.err0("KillTmux", Args{ID: id}) }

func (c *Client) WorktreeStatus(id string) (dirty, unpushed, ok bool) {
	r, err := c.call("WorktreeStatus", Args{ID: id})
	if err != nil {
		return false, false, false
	}
	return r.Dirty, r.Unpushed, r.OK
}

func (c *Client) ChangeSummary(id string) (filesChanged, unpushedCommits int, ok bool) {
	r, err := c.call("ChangeSummary", Args{ID: id})
	if err != nil {
		return 0, 0, false
	}
	return r.Files, r.Commits, r.OK
}

func (c *Client) SetSessionTags(id, ticket, pr string) (session.Session, error) {
	return c.sess("SetSessionTags", Args{ID: id, Ticket: ticket, PR: pr})
}

func (c *Client) SetSessionPrompt(id, prompt string) (session.Session, error) {
	return c.sess("SetSessionPrompt", Args{ID: id, Prompt: prompt})
}

func (c *Client) SetSessionAgent(id, agent string, dangerous bool) (session.Session, error) {
	return c.sess("SetSessionAgent", Args{ID: id, Agent: agent, Dangerous: &dangerous})
}

func (c *Client) RenameSession(id, newName string) (session.Session, error) {
	return c.sess("RenameSession", Args{ID: id, Name: newName})
}

func (c *Client) SetSessionArchived(id string, archived bool) (session.Session, error) {
	return c.sess("SetSessionArchived", Args{ID: id, On: archived})
}

func (c *Client) MoveSession(id string, delta int) error {
	return c.err0("MoveSession", Args{ID: id, Delta: delta})
}

func (c *Client) MoveProject(name string, delta int) error {
	return c.mut("MoveProject", Args{Name: name, Delta: delta})
}

func (c *Client) AddProject(name string, p config.Project) (string, error) {
	r, err := c.mutResult("AddProject", Args{Name: name, Proj: p})
	return r.Hint, err
}

func (c *Client) InitProjectAndAdd(name string, p config.Project) error {
	return c.mut("InitProjectAndAdd", Args{Name: name, Proj: p})
}

func (c *Client) AddPlainProject(name string, p config.Project) error {
	return c.mut("AddPlainProject", Args{Name: name, Proj: p})
}

func (c *Client) UpdateProject(name string, p config.Project) error {
	return c.mut("UpdateProject", Args{Name: name, Proj: p})
}

func (c *Client) RemoveProject(name string) error {
	return c.mut("RemoveProject", Args{Name: name})
}

func (c *Client) SetTheme(theme, appearance string) error {
	return c.mut("SetTheme", Args{Theme: theme, Appearance: appearance})
}

func (c *Client) SetAutoSubmitDefault(autoSubmit bool) error {
	return c.mut("SetAutoSubmitDefault", Args{On: autoSubmit})
}

func (c *Client) SetSortRecentFirst(recentFirst bool) error {
	return c.mut("SetSortRecentFirst", Args{On: recentFirst})
}

func (c *Client) SetAutoTmux(autoTmux bool) error {
	return c.mut("SetAutoTmux", Args{On: autoTmux})
}

func (c *Client) SetCompactDetail(compact bool) error {
	return c.mut("SetCompactDetail", Args{On: compact})
}

// Nudge implements sessionview.Source, asking the server for a snapshot now
// instead of at its next tick. Handed to the live stream goroutine rather
// than written to the connection here, so it can't race that goroutine's
// own writes; dropped on the floor when no stream is connected, since a
// reconnect re-emits everything anyway.
func (c *Client) Nudge() {
	c.nudgeOnce.Do(func() { c.nudgeCh = make(chan struct{}, 1) })
	select {
	case c.nudgeCh <- struct{}{}:
	default:
	}
}

func (c *Client) nudges() chan struct{} {
	c.nudgeOnce.Do(func() { c.nudgeCh = make(chan struct{}, 1) })
	return c.nudgeCh
}

// Run implements sessionview.Source, forwarding the server's snapshot stream
// and reconnecting until ctx is done. Each disconnect emits a snapshot
// carrying only an error, which update.go flashes once — without it the TUI
// keeps rendering the last views forever, which reads as live rather than
// frozen.
func (c *Client) Run(ctx context.Context, out chan<- sessionview.Snapshot) {
	const maxBackoff = 5 * time.Second
	backoff := 200 * time.Millisecond
	for ctx.Err() == nil {
		got, err := c.stream(ctx, out)
		if ctx.Err() != nil {
			return
		}
		if got {
			backoff = 200 * time.Millisecond // a working connection earns a fast retry
		}
		select {
		case out <- sessionview.Snapshot{PollTime: time.Now(), Err: fmt.Sprintf("status stream lost (%v); reconnecting", err)}:
		case <-ctx.Done():
			return
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// stream holds one connection open, forwarding snapshots. got reports
// whether any snapshot arrived, so Run can tell a healthy connection that
// dropped from one that never worked.
func (c *Client) stream(ctx context.Context, out chan<- sessionview.Snapshot) (got bool, err error) {
	conn, err := net.Dial("unix", c.Socket)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	enc := json.NewEncoder(conn)
	if err := enc.Encode(request{Method: "Watch"}); err != nil {
		return false, err
	}
	// Forward nudges on this connection. Only this goroutine ever writes to
	// conn after the request above, so Nudge itself never touches it.
	go func() {
		nudges := c.nudges()
		for {
			select {
			case <-nudges:
				if err := enc.Encode(nudgeRequest{Nudge: true}); err != nil {
					return
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	dec := json.NewDecoder(bufio.NewReader(conn))
	for {
		var snap sessionview.Snapshot
		if err := dec.Decode(&snap); err != nil {
			return got, err
		}
		got = true
		if snap.PollTime.IsZero() {
			snap.PollTime = time.Now()
		}
		select {
		case out <- snap:
		case <-ctx.Done():
			return got, ctx.Err()
		}
	}
}
