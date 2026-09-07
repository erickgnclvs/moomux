package prstatus

import (
	"errors"
	"testing"
)

type fakeRunner struct {
	out string
	err error
}

func (f *fakeRunner) Run(_ string, args ...string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.out, nil
}

func TestFetch(t *testing.T) {
	c := &Client{Runner: &fakeRunner{out: `{"state":"OPEN","mergeable":"CONFLICTING","statusCheckRollup":[]}`}}
	info, err := c.Fetch("https://github.com/example/repo/pull/1")
	if err != nil {
		t.Fatal(err)
	}
	want := Info{State: "OPEN", Mergeable: "CONFLICTING", CI: "NONE"}
	if info != want {
		t.Fatalf("Fetch() = %+v, want %+v", info, want)
	}
}

func TestFetchRunnerError(t *testing.T) {
	c := &Client{Runner: &fakeRunner{err: errors.New("gh: not authenticated")}}
	if _, err := c.Fetch("https://github.com/example/repo/pull/1"); err == nil {
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
