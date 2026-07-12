package gitwt

import (
	"reflect"
	"testing"
)

type fakeRunner struct {
	calls [][]string
}

func (f *fakeRunner) Run(dir string, args ...string) (string, error) {
	c := append([]string{"@" + dir}, args...)
	f.calls = append(f.calls, c)
	return "", nil
}

func TestFetch(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.Fetch("/repo", "main"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "fetch", "origin", "main"}
	if !reflect.DeepEqual(fr.calls[0], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestAddWorktree(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.AddWorktree("/repo", "/wt/foo", "user/foo", "main"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "worktree", "add", "/wt/foo", "-b", "user/foo", "origin/main"}
	if !reflect.DeepEqual(fr.calls[len(fr.calls)-1], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestAddWorktreeExisting(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.AddWorktreeExisting("/repo", "/wt/foo", "user/foo"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "worktree", "add", "/wt/foo", "user/foo"}
	if !reflect.DeepEqual(fr.calls[len(fr.calls)-1], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestRemoveWorktree(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.RemoveWorktree("/repo", "/wt/foo"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "worktree", "remove", "/wt/foo", "--force"}
	if !reflect.DeepEqual(fr.calls[0], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestDeleteBranch(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if err := c.DeleteBranch("/repo", "user/foo"); err != nil {
		t.Fatal(err)
	}
	want := []string{"@/repo", "branch", "-D", "user/foo"}
	if !reflect.DeepEqual(fr.calls[0], want) {
		t.Fatalf("calls = %v", fr.calls)
	}
}

func TestParseNumstat(t *testing.T) {
	out := "2\t1\tf.txt\n1\t0\tg.txt\n-\t-\tbin.dat\n\n"
	got := parseNumstat(out)
	want := DiffStat{Files: 3, Additions: 3, Deletions: 1}
	if got != want {
		t.Fatalf("parseNumstat = %+v, want %+v", got, want)
	}
	if empty := parseNumstat("\n  \n"); empty != (DiffStat{}) {
		t.Fatalf("parseNumstat(empty) = %+v, want zero", empty)
	}
}

// scriptRunner returns canned output per git subcommand (args[0]).
type scriptRunner struct {
	outputs map[string]string
	calls   [][]string
}

func (s *scriptRunner) Run(dir string, args ...string) (string, error) {
	s.calls = append(s.calls, append([]string{"@" + dir}, args...))
	if len(args) > 0 {
		if out, ok := s.outputs[args[0]]; ok {
			return out, nil
		}
	}
	return "", nil
}

func TestDiffStatAgainstUsesMergeBase(t *testing.T) {
	sr := &scriptRunner{outputs: map[string]string{
		"merge-base": "abc123\n",
		"diff":       "2\t1\tf.txt\n",
	}}
	c := &Client{Runner: sr}
	got, err := c.DiffStatAgainst("/wt", "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if want := (DiffStat{Files: 1, Additions: 2, Deletions: 1}); got != want {
		t.Fatalf("stat = %+v, want %+v", got, want)
	}
	// last call must diff against the resolved merge-base commit, not the branch.
	last := sr.calls[len(sr.calls)-1]
	want := []string{"@/wt", "diff", "--numstat", "abc123"}
	if !reflect.DeepEqual(last, want) {
		t.Fatalf("diff call = %v, want %v", last, want)
	}
}

func TestDiffTargetEmptyRefIsHEAD(t *testing.T) {
	c := &Client{Runner: &scriptRunner{}}
	if got := c.diffTarget("/wt", ""); got != "HEAD" {
		t.Fatalf("diffTarget(empty) = %q, want HEAD", got)
	}
}
