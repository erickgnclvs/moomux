package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tmux"
)

// A branch name is nearly unconstrained, and this string is handed to tmux
// to run through a shell — an unquoted one ends the command and runs the
// rest.
func TestReviewScriptQuotesTheBaseRef(t *testing.T) {
	got := ReviewScript("a'b; rm -rf /")
	for _, want := range []string{
		`--quiet 'origin/a'\''b; rm -rf /'`,
		`--quiet 'a'\''b; rm -rf /'`,
		"git diff HEAD",
		"git status --short --branch",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("script missing %q:\n%s", want, got)
		}
	}
}

// origin/ first because a local base branch goes stale in a worktree
// checkout; the local one and a bare HEAD diff are the fallbacks.
func TestReviewScriptFallbackOrder(t *testing.T) {
	got := ReviewScript("main")
	origin, local, head := strings.Index(got, "origin/main"), strings.Index(got, "--quiet main"), strings.Index(got, "git diff HEAD")
	if !(origin >= 0 && origin < local && local < head) {
		t.Fatalf("wrong fallback order: %s", got)
	}
}

// The fallback must key off whether the ref exists, never off a diff's exit
// status: git exits 141 when the user quits the pager early, so a `||` chain
// between the diffs silently re-ran and ended up showing `git diff HEAD` —
// uncommitted work only — as the review.
func TestReviewScriptDoesNotChainDiffsOnExitStatus(t *testing.T) {
	got := ReviewScript("main")
	if strings.Count(got, "git diff --merge-base") != 1 {
		t.Fatalf("more than one merge-base diff to fall between:\n%s", got)
	}
	for _, cmd := range []string{"git diff --merge-base", "git diff HEAD"} {
		if i := strings.Index(got, cmd); strings.Contains(got[i+len(cmd):], "||") {
			t.Fatalf("a diff falls through on its exit status:\n%s", got)
		}
	}
}

func newReviewApp(t *testing.T, fr *fakeTmuxRunner, sessions ...session.Session) *App {
	t.Helper()
	a := &App{
		Cfg: &config.Config{Projects: map[string]config.Project{
			"proj":  {Repo: "/repo", BaseBranch: "trunk"},
			"notes": {Kind: "plain", Repo: "/notes"},
		}},
		Store: &session.Store{Path: filepath.Join(t.TempDir(), "sessions.json")},
		Tmux:  &tmux.Client{Runner: fr},
	}
	if err := a.Store.Load(); err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if err := a.Store.Put(s); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// Capture's whole job on the wire is the id-to-tmux-name mapping: a front
// end asks about session ids and must never need to know what moomux called
// the tmux session. Unknown ids drop out rather than erroring.
func TestCaptureKeysBySessionIDAndSkipsUnknownIDs(t *testing.T) {
	fr := &fakeTmuxRunner{out: map[string]string{
		"list-sessions -F #{session_name}": "moomux-a\nmoomux-b\n",
	}}
	a := newReviewApp(t, fr,
		session.Session{ID: "proj:a", Project: "proj", TmuxSession: "moomux-a"},
		session.Session{ID: "proj:b", Project: "proj", TmuxSession: "moomux-b"},
	)
	got := a.Capture([]string{"proj:a", "proj:b", "proj:gone"})
	if _, ok := got["proj:gone"]; ok {
		t.Fatalf("unknown id should be absent, got %v", got)
	}
	// One invocation for the batch, then one fallback each (the fake runner
	// returns empty output, so nothing parses out of the batch). The
	// per-session window lookups ahead of it are cached, one per session.
	var batch []string
	for _, call := range fr.calls {
		if len(call) >= 8 {
			batch = call
			break
		}
	}
	if batch == nil {
		t.Fatalf("expected a batched capture, got %v", fr.calls)
	}
	joined := strings.Join(batch, " ")
	if !strings.Contains(joined, "=moomux-a:^") || !strings.Contains(joined, "=moomux-b:^") {
		t.Fatalf("batch did not target both tmux sessions: %s", joined)
	}
}

// The session's own base is what its branch was cut from; the project's is
// the fallback for a resumed branch that never recorded one.
func TestReviewPrefersTheSessionBaseThenTheProjects(t *testing.T) {
	fr := &fakeTmuxRunner{}
	a := newReviewApp(t, fr,
		session.Session{ID: "proj:a", Project: "proj", TmuxSession: "moomux-a", WorktreePath: "/wt/a", BaseBranch: "release"},
		session.Session{ID: "proj:b", Project: "proj", TmuxSession: "moomux-b", WorktreePath: "/wt/b"},
	)
	for id, want := range map[string]string{"proj:a": "origin/release", "proj:b": "origin/trunk"} {
		fr.calls = nil
		if _, err := a.Review(id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		var all []string
		for _, c := range fr.calls {
			all = append(all, strings.Join(c, " "))
		}
		if !strings.Contains(strings.Join(all, "\n"), want) {
			t.Fatalf("%s: want %q in %v", id, want, all)
		}
	}
}

// respawn-window and new-window both fail on a parked session with nothing
// but an exit status, and reviving it would relaunch the agent — an
// expensive surprise from "show me the diff". So it has to be named.
func TestReviewOnAParkedSessionSaysSoAndDoesNotReviveIt(t *testing.T) {
	fr := &fakeTmuxRunner{failOn: map[string]bool{"has-session -t =moomux-a": true}}
	a := newReviewApp(t, fr,
		session.Session{ID: "proj:a", Project: "proj", Name: "a", TmuxSession: "moomux-a", WorktreePath: "/wt/a"})
	_, err := a.Review("proj:a")
	if err == nil || !strings.Contains(err.Error(), "parked") {
		t.Fatalf("err = %v, want it to name the session as parked", err)
	}
	for _, c := range fr.calls {
		if c[0] == "new-session" || c[0] == "respawn-window" || c[0] == "new-window" {
			t.Fatalf("review must not revive or open anything: %v", fr.calls)
		}
	}
}

func TestReviewRefusesAPlainProject(t *testing.T) {
	fr := &fakeTmuxRunner{}
	a := newReviewApp(t, fr, session.Session{ID: "notes:a", Project: "notes", TmuxSession: "moomux-n"})
	if _, err := a.Review("notes:a"); err == nil {
		t.Fatal("a plain project has nothing to diff")
	}
	if _, err := a.Review("proj:nope"); err == nil {
		t.Fatal("unknown session should error")
	}
}

// A session whose project has been removed from the config keeps its store
// entry. Without the lookup's ok, the zero Project is neither plain nor
// based on anything, so review would open a diff against "main" in a
// worktree the core no longer manages.
func TestReviewRefusesASessionWhoseProjectIsGone(t *testing.T) {
	fr := &fakeTmuxRunner{}
	a := newReviewApp(t, fr, session.Session{ID: "old:a", Project: "old", TmuxSession: "moomux-a", WorktreePath: "/wt/a"})
	_, err := a.Review("old:a")
	if err == nil || !strings.Contains(err.Error(), "old") {
		t.Fatalf("err = %v, want it to name the missing project", err)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("nothing should have been run: %v", fr.calls)
	}
}

// Parking keeps the session's tmux name, and a dead name in the batch
// aborts the chained sequence — every session after it would then cost a
// fork of its own, every poll tick.
func TestCaptureSkipsParkedSessions(t *testing.T) {
	fr := &fakeTmuxRunner{out: map[string]string{
		"list-sessions -F #{session_name}": "moomux-a\n",
	}}
	a := newReviewApp(t, fr,
		session.Session{ID: "proj:a", Project: "proj", TmuxSession: "moomux-a"},
		session.Session{ID: "proj:parked", Project: "proj", TmuxSession: "moomux-parked"},
	)
	a.Capture([]string{"proj:a", "proj:parked"})
	for _, call := range fr.calls {
		if strings.Contains(strings.Join(call, " "), "moomux-parked") {
			t.Fatalf("parked session reached tmux: %v", call)
		}
	}
}
