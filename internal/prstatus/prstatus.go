// Package prstatus reports a GitHub pull request's merge/CI status via the
// gh CLI.
package prstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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

// rawPR mirrors the subset of `gh pr view --json` fields Fetch requests.
type rawPR struct {
	URL               string     `json:"url"`
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

// Fetch reports prURL's merge/CI status. An error means gh isn't installed,
// the user isn't authenticated, or the PR couldn't be resolved — callers
// treat that as "status unknown", the same way gitwt.WorktreeStatus's ok=false
// covers a path that isn't a git repo.
func (c *Client) Fetch(prURL string) (Info, error) {
	raw, err := c.view("", prURL)
	if err != nil {
		return Info{}, err
	}
	return raw.info(), nil
}

// FetchBranch reports the status *and* URL of the pull request open for the
// branch checked out in dir — `gh pr view` with no argument resolves the PR
// from the current branch. It's how a session gets its PR without anyone
// running `moomux tag`: the PR is found wherever it was opened, including
// from another machine or the GitHub web UI. An error covers every "no PR
// here" case (no PR for the branch, not a GitHub repo, gh missing or logged
// out), which callers treat as "nothing to attach yet".
func (c *Client) FetchBranch(dir string) (Info, string, error) {
	raw, err := c.view(dir)
	if err != nil {
		return Info{}, "", err
	}
	if raw.URL == "" {
		return Info{}, "", fmt.Errorf("gh pr view: no URL for the branch in %s", dir)
	}
	return raw.info(), raw.URL, nil
}

// view runs `gh pr view` in dir, for the given PR argument (none = the
// branch checked out there).
func (c *Client) view(dir string, prArgs ...string) (rawPR, error) {
	args := append([]string{"pr", "view"}, prArgs...)
	out, err := c.Runner.Run(dir, append(args, "--json", "url,state,mergeable,statusCheckRollup")...)
	if err != nil {
		return rawPR{}, err
	}
	var raw rawPR
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return rawPR{}, fmt.Errorf("parse gh pr view output: %w", err)
	}
	return raw, nil
}

func (r rawPR) info() Info {
	return Info{
		State:     r.State,
		Mergeable: r.Mergeable,
		CI:        aggregateCI(r.StatusCheckRollup),
	}
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
