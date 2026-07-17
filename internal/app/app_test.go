package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tmux"
)

// fakeGitRunner records git invocations. Keys in failOn (joined args, without
// the dir) make that call fail, so tests can simulate missing remotes,
// missing branches, or worktree failures.
type fakeGitRunner struct {
	calls  [][]string
	failOn map[string]bool
}

func (f *fakeGitRunner) Run(dir string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{"@" + dir}, args...))
	if f.failOn[strings.Join(args, " ")] {
		return "", errors.New("git failed: " + strings.Join(args, " "))
	}
	return "", nil
}

// fakeTmuxRunner records tmux invocations; failOn works like fakeGitRunner's
// but failures carry an ExitCode so HasSession treats them as "absent".
type fakeTmuxRunner struct {
	calls  [][]string
	out    map[string]string
	failOn map[string]bool
}

type exitErr struct{}

func (exitErr) Error() string { return "exit status 1" }
func (exitErr) ExitCode() int { return 1 }

func (f *fakeTmuxRunner) Run(args ...string) (string, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.failOn[key] {
		return "", exitErr{}
	}
	return f.out[key], nil
}

func (f *fakeTmuxRunner) called(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(strings.Join(c, " "), prefix) {
			return true
		}
	}
	return false
}

type fakeTerminal struct {
	calls [][2]string
	hint  string
	err   error
}

func (f *fakeTerminal) OpenSession(tmuxSession, title string) (string, error) {
	f.calls = append(f.calls, [2]string{tmuxSession, title})
	return f.hint, f.err
}

// noBranch marks the rev-parse existence check for branch as failing, i.e.
// "branch does not exist yet" — the normal case when creating a session.
func noBranch(fr *fakeGitRunner, branch string) {
	fr.failOn["rev-parse --verify --quiet refs/heads/"+branch] = true
}

func newTestApp(t *testing.T, projects map[string]config.Project) (*App, *fakeGitRunner, *fakeTmuxRunner, *fakeTerminal) {
	t.Helper()
	dir := t.TempDir()
	git := &fakeGitRunner{failOn: map[string]bool{}}
	tm := &fakeTmuxRunner{out: map[string]string{}, failOn: map[string]bool{}}
	term := &fakeTerminal{}
	store := &session.Store{Path: filepath.Join(dir, "sessions.json")}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	a := &App{
		Cfg:          &config.Config{Projects: projects},
		CfgPath:      filepath.Join(dir, "config.toml"),
		Store:        store,
		Tmux:         &tmux.Client{Runner: tm},
		Terminal:     term,
		Git:          &gitwt.Client{Runner: git},
		WorktreeRoot: filepath.Join(dir, "worktrees"),
	}
	return a, git, tm, term
}

func gitProject(repo string) map[string]config.Project {
	return map[string]config.Project{
		"demo": {Kind: "git", Repo: repo, BaseBranch: "main"},
	}
}

func TestAgentCmd(t *testing.T) {
	for agent, want := range map[string]string{
		"codex": "codex", "opencode": "opencode", "claude": "claude", "": "claude", "other": "claude",
	} {
		if got := agentCmd(agent); got != want {
			t.Errorf("agentCmd(%q) = %q, want %q", agent, got, want)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"login-page":      "login-page",
		"Fix Bug #42":     "Fix-Bug--42",
		"a/b.c":           "a-b-c",
		"--trim--":        "trim",
		"///":             "session",
		"":                "session",
		"under_score_ok9": "under_score_ok9",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveNameFromBranch(t *testing.T) {
	cases := map[string]string{
		"feature/login-page": "login-page",
		"main":               "main",
		"a/b/c.d":            "c-d",
	}
	for in, want := range cases {
		if got := deriveNameFromBranch(in); got != want {
			t.Errorf("deriveNameFromBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueNameFromBranch(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	if got := a.uniqueNameFromBranch("demo", "feature/login"); got != "login" {
		t.Fatalf("got %q, want login", got)
	}
	for _, name := range []string{"login", "login-2"} {
		if err := a.Store.Put(session.Session{ID: session.MakeID("demo", name), Project: "demo", Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if got := a.uniqueNameFromBranch("demo", "feature/login"); got != "login-3" {
		t.Fatalf("got %q, want login-3", got)
	}
}

func TestNextOpenCodePort(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	if got := a.nextOpenCodePort(); got != 4096 {
		t.Fatalf("empty store: got %d, want 4096", got)
	}
	_ = a.Store.Put(session.Session{ID: "demo:x", Project: "demo", Name: "x", Agent: "opencode", AgentPort: 4100})
	_ = a.Store.Put(session.Session{ID: "demo:y", Project: "demo", Name: "y", Agent: "claude"})
	if got := a.nextOpenCodePort(); got != 4101 {
		t.Fatalf("got %d, want 4101", got)
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := expandHome("~/repo"); got != filepath.Join(home, "repo") {
		t.Fatalf("got %q", got)
	}
	if got := expandHome("/abs/path"); got != "/abs/path" {
		t.Fatalf("got %q", got)
	}
}

func TestWorktreeRootDefault(t *testing.T) {
	got := WorktreeRootDefault()
	if !strings.HasSuffix(got, filepath.Join(".local", "share", "moomux", "worktrees")) {
		t.Fatalf("got %q", got)
	}
}

func TestCreateSessionWorktree(t *testing.T) {
	a, git, tm, term := newTestApp(t, gitProject("/repo"))
	tm.out["list-panes -t moomux-feat -F #{pane_id}"] = "%0\n"
	noBranch(git, "feat")

	s, hint, err := a.CreateSession("demo", "feat", "", "", "https://ticket/1")
	if err != nil {
		t.Fatal(err)
	}
	if hint != "" {
		t.Fatalf("hint = %q", hint)
	}
	wantWt := filepath.Join(a.WorktreeRoot, "demo", "feat")
	if s.WorktreePath != wantWt || s.Branch != "feat" || !s.NewBranch || s.TmuxSession != "moomux-feat" || s.Ticket != "https://ticket/1" {
		t.Fatalf("session = %+v", s)
	}
	if s.AgentName() != "claude" {
		t.Fatalf("agent = %q", s.AgentName())
	}
	// worktree add -b for a fresh branch, based on origin/main (remote present).
	found := false
	for _, c := range git.calls {
		if strings.Join(c, " ") == "@/repo worktree add "+wantWt+" -b feat origin/main" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no worktree add call; calls = %v", git.calls)
	}
	if !tm.called("new-session -d -s moomux-feat -c " + wantWt) {
		t.Fatalf("no tmux new-session; calls = %v", tm.calls)
	}
	if len(term.calls) != 1 || term.calls[0] != [2]string{"moomux-feat", "feat"} {
		t.Fatalf("terminal calls = %v", term.calls)
	}
	if _, ok := a.Store.Get("demo:feat"); !ok {
		t.Fatal("session not persisted")
	}
}

func TestCreateSessionBranchPrefix(t *testing.T) {
	projects := map[string]config.Project{
		"demo": {Kind: "git", Repo: "/repo", BaseBranch: "main", BranchPrefix: "user"},
	}
	a, git, tm, _ := newTestApp(t, projects)
	tm.out["list-panes -t moomux-feat -F #{pane_id}"] = "%0\n"
	noBranch(git, "user/feat")

	s, _, err := a.CreateSession("demo", "feat", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Branch != "user/feat" {
		t.Fatalf("branch = %q", s.Branch)
	}
}

func TestCreateSessionExistingBranch(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	tm.out["list-panes -t moomux-login-page -F #{pane_id}"] = "%0\n"

	s, _, err := a.CreateSession("demo", "", "", "feature/login-page", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "login-page" || s.Branch != "feature/login-page" || s.NewBranch {
		t.Fatalf("session = %+v", s)
	}
	wantWt := filepath.Join(a.WorktreeRoot, "demo", "login-page")
	found := false
	for _, c := range git.calls {
		if strings.Join(c, " ") == "@/repo worktree add "+wantWt+" feature/login-page" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no worktree add (existing) call; calls = %v", git.calls)
	}
}

func TestCreateSessionOpenCodePorts(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	tm.out["list-panes -t moomux-one -F #{pane_id}"] = "%0\n"
	tm.out["list-panes -t moomux-two -F #{pane_id}"] = "%0\n"
	noBranch(git, "one")
	noBranch(git, "two")

	s1, _, err := a.CreateSession("demo", "one", "opencode", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if s1.AgentPort != 4096 {
		t.Fatalf("port = %d", s1.AgentPort)
	}
	s2, _, err := a.CreateSession("demo", "two", "opencode", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if s2.AgentPort != 4097 {
		t.Fatalf("port = %d", s2.AgentPort)
	}
	want := "send-keys -t %0 opencode --port 4097 Enter"
	if !tm.called(want) {
		t.Fatalf("no %q; calls = %v", want, tm.calls)
	}
}

func TestCreateSessionPlainProject(t *testing.T) {
	projects := map[string]config.Project{
		"notes": {Kind: "plain", Repo: "/notes"},
	}
	a, git, tm, _ := newTestApp(t, projects)
	tm.out["list-panes -t moomux-todo -F #{pane_id}"] = "%0\n"

	s, _, err := a.CreateSession("notes", "todo", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.WorktreePath != "/notes" || s.Branch != "" || s.NewBranch {
		t.Fatalf("session = %+v", s)
	}
	if len(git.calls) != 0 {
		t.Fatalf("plain project must not touch git; calls = %v", git.calls)
	}
}

func TestCreateSessionErrors(t *testing.T) {
	a, git, tm, term := newTestApp(t, gitProject("/repo"))

	if _, _, err := a.CreateSession("nope", "x", "", "", ""); err == nil {
		t.Fatal("unknown project must fail")
	}
	if _, _, err := a.CreateSession("demo", "", "", "", ""); err == nil {
		t.Fatal("empty name+branch must fail")
	}

	// git worktree add fails
	noBranch(git, "bad")
	git.failOn["worktree add "+filepath.Join(a.WorktreeRoot, "demo", "bad")+" -b bad origin/main"] = true
	if _, _, err := a.CreateSession("demo", "bad", "", "", ""); err == nil || !strings.Contains(err.Error(), "git worktree add") {
		t.Fatalf("err = %v", err)
	}

	// tmux new-session fails
	noBranch(git, "tmuxfail")
	tm.failOn["new-session -d -s moomux-tmuxfail -c "+filepath.Join(a.WorktreeRoot, "demo", "tmuxfail")+" -n tmuxfail"] = true
	if _, _, err := a.CreateSession("demo", "tmuxfail", "", "", ""); err == nil || !strings.Contains(err.Error(), "tmux new-session") {
		t.Fatalf("err = %v", err)
	}

	// terminal open fails: the worktree and tmux session already exist by
	// this point, so CreateSession degrades to a manual-attach hint instead
	// of failing and stranding them outside the store.
	noBranch(git, "termfail")
	tm.out["list-panes -t moomux-termfail -F #{pane_id}"] = "%0\n"
	term.err = errors.New("no terminal")
	s, hint, err := a.CreateSession("demo", "termfail", "", "", "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if s.TmuxSession != "moomux-termfail" {
		t.Fatalf("session = %+v", s)
	}
	if !strings.Contains(hint, "tmux attach -t moomux-termfail") {
		t.Fatalf("hint = %q", hint)
	}
}

func TestOpenSessionAlive(t *testing.T) {
	a, _, tm, term := newTestApp(t, gitProject("/repo"))
	term.hint = "run: tmux attach -t moomux-feat"
	_ = a.Store.Put(session.Session{ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat", WorktreePath: "/wt/feat"})
	tm.out["list-panes -t moomux-feat -F #{pane_current_path}"] = "/wt/feat\n"

	hint, err := a.OpenSession("demo:feat")
	if err != nil {
		t.Fatal(err)
	}
	if hint != term.hint {
		t.Fatalf("hint = %q", hint)
	}
	if tm.called("new-session") {
		t.Fatalf("must not recreate a live session; calls = %v", tm.calls)
	}
	if len(term.calls) != 1 {
		t.Fatalf("terminal calls = %v", term.calls)
	}
}

func TestOpenSessionCwdMismatchRecreates(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat", WorktreePath: "/wt/feat"})
	tm.out["list-panes -t moomux-feat -F #{pane_current_path}"] = "/somewhere/else\n"
	tm.out["list-panes -t moomux-feat -F #{pane_id}"] = "%0\n"

	if _, err := a.OpenSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	if !tm.called("kill-session -t moomux-feat") {
		t.Fatalf("expected kill-session; calls = %v", tm.calls)
	}
	if !tm.called("new-session -d -s moomux-feat -c /wt/feat") {
		t.Fatalf("expected recreation; calls = %v", tm.calls)
	}
}

func TestOpenSessionDeadRecreatesWithAgent(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{
		ID: "demo:oc", Project: "demo", Name: "oc", TmuxSession: "moomux-oc",
		WorktreePath: "/wt/oc", Agent: "opencode", AgentPort: 4099,
	})
	tm.failOn["has-session -t moomux-oc"] = true
	tm.out["list-panes -t moomux-oc -F #{pane_id}"] = "%0\n"

	if _, err := a.OpenSession("demo:oc"); err != nil {
		t.Fatal(err)
	}
	if !tm.called("send-keys -t %0 opencode --port 4099 Enter") {
		t.Fatalf("expected opencode relaunch with port; calls = %v", tm.calls)
	}
}

func TestOpenSessionUnknown(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	if _, err := a.OpenSession("demo:nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestTmuxAliveAll(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", TmuxSession: "moomux-a"})
	_ = a.Store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", TmuxSession: "moomux-b"})
	tm.out["list-sessions -F #{session_name}"] = "moomux-a\nunrelated\n"

	got := a.TmuxAliveAll()
	if !got["demo:a"] || got["demo:b"] {
		t.Fatalf("got %v", got)
	}
}

func TestKillTmux(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", TmuxSession: "moomux-a"})

	if err := a.KillTmux("demo:a"); err != nil {
		t.Fatal(err)
	}
	if !tm.called("kill-session -t moomux-a") {
		t.Fatalf("calls = %v", tm.calls)
	}

	// dead session: no-op, no error
	tm.calls = nil
	tm.failOn["has-session -t moomux-a"] = true
	if err := a.KillTmux("demo:a"); err != nil {
		t.Fatal(err)
	}
	if tm.called("kill-session") {
		t.Fatalf("must not kill a dead session; calls = %v", tm.calls)
	}

	if err := a.KillTmux("demo:nope"); err == nil {
		t.Fatal("unknown id must fail")
	}
}

func TestMoveSession(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", Order: 1})
	_ = a.Store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", Order: 2})

	if err := a.MoveSession("demo:b", -1); err != nil {
		t.Fatal(err)
	}
	if got := a.Store.ByProject("demo"); got[0].ID != "demo:b" || got[1].ID != "demo:a" {
		t.Fatalf("order = %v, %v", got[0].ID, got[1].ID)
	}
	// out of bounds: no-op
	if err := a.MoveSession("demo:b", -1); err != nil {
		t.Fatal(err)
	}
	if got := a.Store.ByProject("demo"); got[0].ID != "demo:b" {
		t.Fatalf("unexpected reorder: %v", got)
	}
	if err := a.MoveSession("demo:nope", 1); err == nil {
		t.Fatal("unknown id must fail")
	}
}

func TestMoveProject(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{
		"alpha": {Repo: "/a"}, "beta": {Repo: "/b"},
	})

	if err := a.MoveProject("beta", -1); err != nil {
		t.Fatal(err)
	}
	if got := a.Projects(); got[0] != "beta" || got[1] != "alpha" {
		t.Fatalf("order = %v", got)
	}
	// out of bounds: no-op
	if err := a.MoveProject("beta", -1); err != nil {
		t.Fatal(err)
	}
	if err := a.MoveProject("nope", 1); err == nil {
		t.Fatal("unknown project must fail")
	}
}

func TestSetSessionTags(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a"})

	s, err := a.SetSessionTags("demo:a", "https://ticket/1", "https://pr/2")
	if err != nil {
		t.Fatal(err)
	}
	if s.Ticket != "https://ticket/1" || s.PR != "https://pr/2" {
		t.Fatalf("session = %+v", s)
	}
	if _, err := a.SetSessionTags("demo:nope", "", ""); err == nil {
		t.Fatal("unknown id must fail")
	}
}

func TestSetSessionArchived(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a"})

	s, err := a.SetSessionArchived("demo:a", true)
	if err != nil || !s.Archived {
		t.Fatalf("s=%+v err=%v", s, err)
	}
	s, err = a.SetSessionArchived("demo:a", false)
	if err != nil || s.Archived {
		t.Fatalf("s=%+v err=%v", s, err)
	}
}

func TestAddProject(t *testing.T) {
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	a, _, _, _ := newTestApp(t, map[string]config.Project{})

	if err := a.AddProject("demo", config.Project{Repo: repo}); err != nil {
		t.Fatal(err)
	}
	p := a.Cfg.Projects["demo"]
	if p.Kind != "git" || p.BaseBranch != "main" {
		t.Fatalf("project = %+v", p)
	}
	if _, err := os.Stat(a.CfgPath); err != nil {
		t.Fatalf("config not saved: %v", err)
	}
}

func TestAddProjectNotARepo(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{})
	err := a.AddProject("demo", config.Project{Repo: t.TempDir()})
	if !errors.Is(err, gitwt.ErrNotGitRepo) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateProjectErrors(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{"exists": {Repo: "/x"}})
	cases := []struct {
		name string
		p    config.Project
	}{
		{"", config.Project{Repo: "/x"}},
		{"has space", config.Project{Repo: "/x"}},
		{"has/slash", config.Project{Repo: "/x"}},
		{"exists", config.Project{Repo: "/x"}},
		{"norepo", config.Project{}},
	}
	for _, c := range cases {
		p := c.p
		if err := a.validateProject(c.name, &p); err == nil {
			t.Errorf("validateProject(%q, %+v) should fail", c.name, c.p)
		}
	}
	// home expansion + base branch default
	p := config.Project{Repo: "~/somewhere"}
	if err := a.validateProject("ok", &p); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if p.Repo != filepath.Join(home, "somewhere") || p.BaseBranch != "main" {
		t.Fatalf("project = %+v", p)
	}
}

func TestInitProjectAndAdd(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{})
	repo := filepath.Join(t.TempDir(), "fresh")

	if err := a.InitProjectAndAdd("demo", config.Project{Repo: repo, BaseBranch: "trunk"}); err != nil {
		t.Fatal(err)
	}
	if err := gitwt.IsRepo(repo); err != nil {
		t.Fatalf("repo not initialized: %v", err)
	}
	if p := a.Cfg.Projects["demo"]; p.Kind != "git" || p.BaseBranch != "trunk" {
		t.Fatalf("project = %+v", p)
	}
}

func TestAddPlainProject(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{})
	dir := filepath.Join(t.TempDir(), "notes")

	if err := a.AddPlainProject("notes", config.Project{Repo: dir, BaseBranch: "main", BranchPrefix: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dir not created: %v", err)
	}
	p := a.Cfg.Projects["notes"]
	if p.Kind != "plain" || p.BaseBranch != "" || p.BranchPrefix != "" {
		t.Fatalf("project = %+v", p)
	}
}

func TestRemoveProject(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{"demo": {Repo: "/x"}})

	if err := a.RemoveProject("nope"); err == nil {
		t.Fatal("unknown project must fail")
	}
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a"})
	if err := a.RemoveProject("demo"); err == nil || !strings.Contains(err.Error(), "active sessions") {
		t.Fatalf("err = %v", err)
	}
	_ = a.Store.Delete("demo:a")
	if err := a.RemoveProject("demo"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Cfg.Projects["demo"]; ok {
		t.Fatal("project not removed")
	}
}

func TestDeleteSessionWorktree(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	wt := filepath.Join(a.WorktreeRoot, "demo", "feat") // never created on disk
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", Branch: "feat", NewBranch: true,
		TmuxSession: "moomux-feat", WorktreePath: wt,
	})

	if err := a.DeleteSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	if !tm.called("kill-session -t moomux-feat") {
		t.Fatalf("tmux calls = %v", tm.calls)
	}
	var sawRemove, sawBranchDelete bool
	for _, c := range git.calls {
		joined := strings.Join(c, " ")
		if joined == "@/repo worktree remove "+wt+" --force" {
			sawRemove = true
		}
		if joined == "@/repo branch -D feat" {
			sawBranchDelete = true
		}
	}
	if !sawRemove || !sawBranchDelete {
		t.Fatalf("git calls = %v", git.calls)
	}
	if _, ok := a.Store.Get("demo:feat"); ok {
		t.Fatal("session still in store")
	}
}

func TestDeleteSessionKeepsUserBranch(t *testing.T) {
	a, git, _, _ := newTestApp(t, gitProject("/repo"))
	wt := filepath.Join(a.WorktreeRoot, "demo", "feat")
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", Branch: "feature/existing", NewBranch: false,
		TmuxSession: "moomux-feat", WorktreePath: wt,
	})
	git.failOn["has-session"] = true

	if err := a.DeleteSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	for _, c := range git.calls {
		if strings.Contains(strings.Join(c, " "), "branch -D") {
			t.Fatalf("must not delete a pre-existing branch; calls = %v", git.calls)
		}
	}
}

func TestDeleteSessionOrphanedProject(t *testing.T) {
	// Session whose project was removed from config: only its worktree dir
	// is cleaned up, no git calls.
	a, git, _, _ := newTestApp(t, map[string]config.Project{})
	wt := filepath.Join(t.TempDir(), "orphan")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Put(session.Session{ID: "gone:x", Project: "gone", Name: "x", TmuxSession: "moomux-x", WorktreePath: wt})

	if err := a.DeleteSession("gone:x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir not removed: %v", err)
	}
	if len(git.calls) != 0 {
		t.Fatalf("git calls = %v", git.calls)
	}
	if err := a.DeleteSession("gone:nope"); err == nil {
		t.Fatal("unknown id must fail")
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := runGit(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

func runGit(dir string, args ...string) (string, error) {
	return gitwt.ExecRunner().Run(dir, args...)
}
