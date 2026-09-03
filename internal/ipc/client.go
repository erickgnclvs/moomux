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
	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tui"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// Client speaks to a Server over a unix socket. It implements tui.Backend,
// so the TUI runs against a remote core with no other changes, and
// watcher.Watcher, so status updates stream from the same place.
type Client struct {
	Socket string

	// cfg, when set, is the *config.Config handed to the TUI; mut keeps it
	// in sync with the server's after any config-changing call.
	cfg *config.Config

	// mu guards the last-good cache below. tui.Backend's Sessions/Projects/
	// TmuxAliveAll have no error return — they're called from rendering
	// paths, which have nowhere to put one — so a failed call would
	// otherwise return nil and read as "everything was deleted". That is
	// actively destructive: update.go prunes m.states against the live
	// session set, so one empty Sessions() wipes every agent state badge.
	// Returning the last known-good answer keeps the UI stale-but-true; Run
	// is what tells the user the connection is down.
	mu           sync.Mutex
	lastSessions []session.Session
	lastProjects []string
	lastAlive    map[string]bool
}

var (
	_ tui.Backend     = (*Client)(nil)
	_ watcher.Watcher = (*Client)(nil)
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

// mut wraps the calls that change server-side config. The TUI reads projects
// and settings straight off the *config.Config it was constructed with
// (m.cfg.OrderedProjectNames()), which locally is the same pointer App
// mutates — so over a socket that pointer would freeze at connect time and
// a newly added project would never appear. Refreshing it in place mirrors
// what config.Reload does locally.
func (c *Client) mut(method string, a Args) error {
	if err := c.err0(method, a); err != nil {
		return err
	}
	return c.refreshConfig()
}

func (c *Client) refreshConfig() error {
	if c.cfg == nil {
		return nil
	}
	fresh, err := c.Config()
	if err != nil || fresh == nil {
		return err
	}
	*c.cfg = *fresh
	return nil
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
	return r.Cfg, nil
}

// Bind makes cfg the config this client keeps refreshed — see mut.
func (c *Client) Bind(cfg *config.Config) { c.cfg = cfg }

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

func (c *Client) Projects() []string {
	r, err := c.call("Projects", Args{})
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		return c.lastProjects
	}
	c.lastProjects = r.Strings
	return r.Strings
}

func (c *Client) TmuxAliveAll() map[string]bool {
	r, err := c.call("TmuxAliveAll", Args{})
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		return c.lastAlive
	}
	c.lastAlive = r.Alive
	return r.Alive
}

func (c *Client) CreateSession(project, name, agent, existingBranch, ticket string, openTerminal bool, dangerous *bool, baseBranch, model, thinking string) (session.Session, string, error) {
	r, err := c.call("CreateSession", Args{
		Project: project, Name: name, Agent: agent, Branch: existingBranch, Ticket: ticket,
		OpenTerminal: openTerminal, Dangerous: dangerous, BaseBranch: baseBranch,
		Model: model, Thinking: thinking,
	})
	if r.Session == nil {
		return session.Session{}, r.Hint, err
	}
	return *r.Session, r.Hint, err
}

func (c *Client) StartFirstPrompt(tmuxSession, prompt string, autoSubmit bool) error {
	return c.err0("StartFirstPrompt", Args{TmuxSession: tmuxSession, Prompt: prompt, AutoSubmit: autoSubmit})
}

func (c *Client) OpenSession(id string) (string, error) {
	r, err := c.call("OpenSession", Args{ID: id})
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

func (c *Client) PRStatus(id string) (prstatus.Info, bool) {
	r, err := c.call("PRStatus", Args{ID: id})
	if err != nil || r.PR == nil {
		return prstatus.Info{}, false
	}
	return *r.PR, r.OK
}

func (c *Client) SetSessionStatusTitle(id string, st watcher.State) error {
	return c.err0("SetSessionStatusTitle", Args{ID: id, State: st})
}

func (c *Client) SetSessionTags(id, ticket, pr string) (session.Session, error) {
	return c.sess("SetSessionTags", Args{ID: id, Ticket: ticket, PR: pr})
}

func (c *Client) SetSessionPrompt(id, prompt string) (session.Session, error) {
	return c.sess("SetSessionPrompt", Args{ID: id, Prompt: prompt})
}

func (c *Client) SetSessionAgent(id, agent string, dangerous bool) (session.Session, error) {
	return c.sess("SetSessionAgent", Args{ID: id, Agent: agent, AgentDangerous: dangerous})
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

func (c *Client) AddProject(name string, p config.Project) error {
	return c.mut("AddProject", Args{Name: name, Proj: p})
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

// Run implements watcher.Watcher, forwarding the server's snapshot stream
// and reconnecting until ctx is done. Each disconnect emits a snapshot
// carrying only an error, which update.go flashes once — without it the TUI
// keeps rendering the last states forever, which reads as live rather than
// frozen.
func (c *Client) Run(ctx context.Context, out chan<- watcher.Snapshot) {
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
		case out <- watcher.Snapshot{PollTime: time.Now(), Err: fmt.Errorf("status stream lost (%v); reconnecting", err)}:
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
func (c *Client) stream(ctx context.Context, out chan<- watcher.Snapshot) (got bool, err error) {
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
	if err := json.NewEncoder(conn).Encode(request{Method: "Watch"}); err != nil {
		return false, err
	}
	dec := json.NewDecoder(bufio.NewReader(conn))
	for {
		var w snapshotWire
		if err := dec.Decode(&w); err != nil {
			return got, err
		}
		got = true
		snap := watcher.Snapshot{States: w.States, PollTime: w.PollTime}
		if snap.PollTime.IsZero() {
			snap.PollTime = time.Now()
		}
		if w.Err != "" {
			snap.Err = wireErr{msg: w.Err}
		}
		select {
		case out <- snap:
		case <-ctx.Done():
			return got, ctx.Err()
		}
	}
}
