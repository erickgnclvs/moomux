// Package prstatus reports a GitHub pull request's merge/CI status via the
// gh CLI.
package prstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// runTimeout bounds the gh subprocess — it hits the network, so an
// unresponsive GitHub API shouldn't hang the caller forever.
var runTimeout = 30 * time.Second

// Info is a PR's merge/CI status as reported by `gh pr view`.
// Info crosses the socket inside sessionview.View, so the json tags are a
// contract with every front end — without them Go would serialize the
// capitalized Go names, inconsistently with every other struct on the wire.
type Info struct {
	State     string `json:"state"`     // OPEN, MERGED, CLOSED
	Mergeable string `json:"mergeable"` // MERGEABLE, CONFLICTING, UNKNOWN
	CI        string `json:"ci"`        // PASSING, FAILING, PENDING, NONE
	// Unresolved is the number of open (unresolved) review threads on the
	// PR — a reviewer's code comment nobody has answered is as much a
	// "don't merge this yet" as a red check.
	Unresolved int `json:"unresolved"`
}

// Runner takes the working directory first, like gitwt.Runner: a
// branch-scoped `gh pr view` only resolves the right repo and PR when it runs
// inside the worktree. URL-scoped calls pass "" and run wherever moomux does.
type Runner interface {
	Run(dir string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	// Without WaitDelay, Output() can still block past ctx's deadline if gh
	// forked a child that inherited the output pipe — see gitwt.execRunner.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh %v: %w", args, err)
	}
	return string(out), nil
}

func ExecRunner() Runner { return execRunner{} }

type Client struct {
	Runner Runner
}

func New() *Client { return &Client{Runner: ExecRunner()} }

// PR is everything one `gh pr view` lookup turned up: the merge/CI status,
// the PR's own URL (what a branch lookup exists to find), and any ticket
// link its title or body carries.
type PR struct {
	Info
	URL    string
	Ticket string
}

// rawPR mirrors the subset of `gh pr view --json` fields Fetch requests.
type rawPR struct {
	URL               string     `json:"url"`
	Title             string     `json:"title"`
	Body              string     `json:"body"`
	State             string     `json:"state"`
	Mergeable         string     `json:"mergeable"`
	StatusCheckRollup []rawCheck `json:"statusCheckRollup"`
}

// rawCheck is one entry of statusCheckRollup — either a GitHub Actions
// CheckRun (Status/Conclusion) or a legacy commit StatusContext (State).
type rawCheck struct {
	Typename   string `json:"__typename"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// Fetch reports on a pull request via the gh CLI. With prURL set it looks up
// that PR; with prURL empty it resolves the PR open for the branch checked
// out in dir — which is how a session gets its PR without anyone running
// `moomux tag`, wherever the PR was opened from, including the GitHub web
// UI. dir is the working directory for the gh call and only matters for the
// branch form.
//
// An error means gh isn't installed, the user isn't authenticated, or no PR
// could be resolved — callers treat that as "status unknown" (or "nothing to
// attach yet"), the same way gitwt.WorktreeStatus's ok=false covers a path
// that isn't a git repo.
func (c *Client) Fetch(dir, prURL string) (PR, error) {
	args := []string{"pr", "view"}
	if prURL != "" {
		args = append(args, prURL)
	}
	out, err := c.Runner.Run(dir, append(args, "--json", "url,title,body,state,mergeable,statusCheckRollup")...)
	if err != nil {
		return PR{}, err
	}
	var raw rawPR
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return PR{}, fmt.Errorf("parse gh pr view output: %w", err)
	}
	if raw.URL == "" {
		return PR{}, fmt.Errorf("gh pr view: no URL in the response")
	}
	info := Info{
		State:     raw.State,
		Mergeable: raw.Mergeable,
		CI:        aggregateCI(raw.StatusCheckRollup),
	}
	// Only an open PR's comments are worth flagging, and the extra round
	// trip is only spent there.
	if raw.State == "OPEN" {
		info.Unresolved = c.unresolvedThreads(raw.URL)
	}
	return PR{
		Info:   info,
		URL:    raw.URL,
		Ticket: ticketIn(raw.Title, raw.Body),
	}, nil
}

// threadQuery counts a PR's unresolved review threads. `gh pr view --json`
// has no field for them, so this is a second call — GraphQL is the only
// place resolution state is exposed.
const threadQuery = `query($url:URI!){resource(url:$url){... on PullRequest{reviewThreads(first:100){nodes{isResolved}}}}}`

// unresolvedThreads returns the count of open review threads on prURL, or 0
// if the lookup fails — an unavailable count degrades to "nothing to flag"
// rather than failing the whole status fetch.
//
// ponytail: first 100 threads only; paginate if a PR ever has more than
// that and the count matters beyond "some".
func (c *Client) unresolvedThreads(prURL string) int {
	out, err := c.Runner.Run("", "api", "graphql", "-f", "query="+threadQuery, "-f", "url="+prURL)
	if err != nil {
		return 0
	}
	var resp struct {
		Data struct {
			Resource struct {
				ReviewThreads struct {
					Nodes []struct {
						IsResolved bool `json:"isResolved"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"resource"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return 0
	}
	n := 0
	for _, t := range resp.Data.Resource.ReviewThreads.Nodes {
		if !t.IsResolved {
			n++
		}
	}
	return n
}

// ticketURL matches the ticket links that are unambiguous on sight: an
// Asana task, a Jira issue on a hosted atlassian.net, or a Linear issue.
// Deliberately URL-only — a bare "PROJ-412" in a branch name can't be turned
// into a link without knowing which host it belongs to, and prose references
// are as likely to name someone else's ticket as this session's. Guessing
// those is the agent's job in the /tag skill, not the core's.
var ticketURL = regexp.MustCompile(`https://(?:app\.asana\.com|[A-Za-z0-9-]+\.atlassian\.net/browse|linear\.app)[^\s)>"'\]]*`)

// ticketIn returns the first ticket link found in a PR's title or body.
func ticketIn(title, body string) string {
	for _, text := range []string{title, body} {
		if m := ticketURL.FindString(text); m != "" {
			// Markdown and prose routinely end a URL with punctuation that
			// isn't part of it ("see <url>." / "(<url>),").
			return strings.TrimRight(m, ".,;:")
		}
	}
	return ""
}

// aggregateCI collapses every check into a single overall status: any
// failure wins outright, otherwise any still-running check makes it PENDING,
// otherwise PASSING — or NONE if the PR has no checks configured at all.
func aggregateCI(checks []rawCheck) string {
	if len(checks) == 0 {
		return "NONE"
	}
	pending := false
	for _, c := range checks {
		if c.Typename == "StatusContext" {
			switch c.State {
			case "FAILURE", "ERROR":
				return "FAILING"
			case "PENDING", "EXPECTED":
				pending = true
			}
			continue
		}
		// CheckRun (or an unrecognized __typename — treat like a CheckRun,
		// the more common shape).
		if c.Status != "COMPLETED" {
			pending = true
			continue
		}
		switch c.Conclusion {
		case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "CANCELLED", "ACTION_REQUIRED":
			return "FAILING"
		}
	}
	if pending {
		return "PENDING"
	}
	return "PASSING"
}
