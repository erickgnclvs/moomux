package prstatus

import (
	"errors"
	"slices"
	"testing"
)

type fakeRunner struct {
	out  string
	err  error
	dir  string
	args []string
}

func (f *fakeRunner) Run(dir string, args ...string) (string, error) {
	f.dir, f.args = dir, args
	if f.err != nil {
		return "", f.err
	}
	return f.out, nil
}

func TestFetch(t *testing.T) {
	c := &Client{Runner: &fakeRunner{out: `{"url":"https://github.com/example/repo/pull/1","state":"OPEN","mergeable":"CONFLICTING","statusCheckRollup":[]}`}}
	pr, err := c.Fetch("", "https://github.com/example/repo/pull/1")
	if err != nil {
		t.Fatal(err)
	}
	want := Info{State: "OPEN", Mergeable: "CONFLICTING", CI: "NONE"}
	if pr.Info != want {
		t.Fatalf("Fetch() = %+v, want %+v", pr.Info, want)
	}
	if pr.URL != "https://github.com/example/repo/pull/1" {
		t.Errorf("Fetch() URL = %q, want the PR's own URL", pr.URL)
	}
}

// TestFetchBranchReportsURLAndTicket: with no PR URL to look up, Fetch
// resolves the branch's PR in dir and reports both the URL to attach and any
// ticket link the PR carries.
func TestFetchBranchReportsURLAndTicket(t *testing.T) {
	r := &fakeRunner{out: `{"url":"https://github.com/example/repo/pull/7","title":"Fix the thing","body":"Closes https://linear.app/acme/issue/ENG-412 finally.","state":"OPEN","statusCheckRollup":[]}`}
	c := &Client{Runner: r}
	pr, err := c.Fetch("/wt/a", "")
	if err != nil {
		t.Fatal(err)
	}
	if pr.URL != "https://github.com/example/repo/pull/7" {
		t.Errorf("URL = %q, want the discovered PR URL", pr.URL)
	}
	if pr.Ticket != "https://linear.app/acme/issue/ENG-412" {
		t.Errorf("Ticket = %q, want the linear link with its trailing text dropped", pr.Ticket)
	}
	if r.dir != "/wt/a" {
		t.Errorf("gh ran in %q, want the worktree", r.dir)
	}
	if slices.Contains(r.args, "https://github.com/example/repo/pull/7") {
		t.Error("a branch lookup must not pass a PR argument")
	}
}

// TestFetchNoURL: gh returning a response without a URL is treated as a
// failed lookup rather than a session tagged with an empty PR.
func TestFetchNoURL(t *testing.T) {
	c := &Client{Runner: &fakeRunner{out: `{"state":"OPEN","statusCheckRollup":[]}`}}
	if _, err := c.Fetch("/wt/a", ""); err == nil {
		t.Fatal("expected an error when the response carries no URL")
	}
}

func TestTicketIn(t *testing.T) {
	cases := []struct {
		name, title, body, want string
	}{
		{"none", "Fix the thing", "no links here", ""},
		{"asana in body", "Fix", "ticket: https://app.asana.com/0/123/456", "https://app.asana.com/0/123/456"},
		{"jira in title", "PROJ-1 https://acme.atlassian.net/browse/PROJ-412", "", "https://acme.atlassian.net/browse/PROJ-412"},
		{"title wins over body", "https://linear.app/acme/issue/ENG-1", "https://app.asana.com/0/9/9", "https://linear.app/acme/issue/ENG-1"},
		{"markdown link", "Fix", "see [the ticket](https://linear.app/acme/issue/ENG-2).", "https://linear.app/acme/issue/ENG-2"},
		{"bare key is not a link", "PROJ-412: fix the thing", "refs PROJ-412", ""},
		{"unknown host ignored", "Fix", "https://tickets.example.com/T-1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ticketIn(tc.title, tc.body); got != tc.want {
				t.Fatalf("ticketIn() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFetchRunnerError(t *testing.T) {
	c := &Client{Runner: &fakeRunner{err: errors.New("gh: not authenticated")}}
	if _, err := c.Fetch("", "https://github.com/example/repo/pull/1"); err == nil {
		t.Fatal("expected an error when the gh call fails")
	}
}

// TestAggregateCI guards the CI-status rollup logic: a single failing check
// wins outright regardless of what else is present, a still-running check
// (without any failure) reports PENDING, all-success reports PASSING, and no
// checks at all reports NONE.
func TestAggregateCI(t *testing.T) {
	cases := []struct {
		name   string
		checks []rawCheck
		want   string
	}{
		{"no checks", nil, "NONE"},
		{
			"all success",
			[]rawCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}, {Status: "COMPLETED", Conclusion: "SUCCESS"}},
			"PASSING",
		},
		{
			"one failing among successes",
			[]rawCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}, {Status: "COMPLETED", Conclusion: "FAILURE"}},
			"FAILING",
		},
		{
			"one still running, none failed",
			[]rawCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}, {Status: "IN_PROGRESS"}},
			"PENDING",
		},
		{
			"pending check alongside a failure still reports failing",
			[]rawCheck{{Status: "IN_PROGRESS"}, {Status: "COMPLETED", Conclusion: "FAILURE"}},
			"FAILING",
		},
		{
			"legacy StatusContext failure",
			[]rawCheck{{Typename: "StatusContext", State: "FAILURE"}},
			"FAILING",
		},
		{
			"legacy StatusContext pending",
			[]rawCheck{{Typename: "StatusContext", State: "PENDING"}},
			"PENDING",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := aggregateCI(tc.checks); got != tc.want {
				t.Fatalf("aggregateCI(%+v) = %q, want %q", tc.checks, got, tc.want)
			}
		})
	}
}
