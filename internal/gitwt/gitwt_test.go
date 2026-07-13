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

type scriptErr string

func (s scriptErr) Error() string { return string(s) }

// scriptRunner returns canned output per git subcommand (args[0]). Refs whose
// `rev-parse --verify` target is listed in unresolvable return an error, so a
// test can simulate a base ref that doesn't exist in the worktree.
type scriptRunner struct {
	outputs      map[string]string
	unresolvable map[string]bool // rev-parse target (last arg) -> should fail
	calls        [][]string
}

func (s *scriptRunner) Run(dir string, args ...string) (string, error) {
	s.calls = append(s.calls, append([]string{"@" + dir}, args...))
	if len(args) > 0 {
		if args[0] == "rev-parse" && s.unresolvable[args[len(args)-1]] {
			return "", scriptErr("no such ref")
		}
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
	// the tracked-diff call must target the resolved merge-base commit, not the branch.
	want := []string{"@/wt", "diff", "--numstat", "abc123"}
	found := false
	for _, call := range sr.calls {
		if reflect.DeepEqual(call, want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no diff call against merge-base; calls = %v", sr.calls)
	}
}

func TestDiffStatAgainstIncludesUntracked(t *testing.T) {
	sr := &scriptRunner{outputs: map[string]string{
		"merge-base": "abc123\n",
		"ls-files":   "new.txt\n",
		// tracked numstat and the --no-index untracked numstat both use args[0]=="diff";
		// return the same 3/0 shape so the two are summed (tracked + untracked).
		"diff": "3\t0\tnew.txt\n",
	}}
	c := &Client{Runner: sr}
	got, err := c.DiffStatAgainst("/wt", "main")
	if err != nil {
		t.Fatal(err)
	}
	// tracked (3/0) + untracked (3/0) = 2 files, 6 additions.
	if want := (DiffStat{Files: 2, Additions: 6, Deletions: 0}); got != want {
		t.Fatalf("stat = %+v, want %+v", got, want)
	}
}

// When the first candidate ref doesn't resolve (e.g. origin/main with no
// remote), diffTarget must skip it and merge-base against the next resolvable
// ref — never degrading to HEAD, which would hide committed branch work.
func TestDiffTargetSkipsUnresolvableRef(t *testing.T) {
	sr := &scriptRunner{
		unresolvable: map[string]bool{"origin/main^{commit}": true},
		outputs:      map[string]string{"merge-base": "def456\n"},
	}
	c := &Client{Runner: sr}
	if got := c.diffTarget("/wt", "origin/main", "main"); got != "def456" {
		t.Fatalf("diffTarget = %q, want merge-base def456 via local main", got)
	}
}

func TestDiffTargetNoRefsIsHEAD(t *testing.T) {
	c := &Client{Runner: &scriptRunner{}}
	if got := c.diffTarget("/wt"); got != "HEAD" {
		t.Fatalf("diffTarget(none) = %q, want HEAD", got)
	}
}
