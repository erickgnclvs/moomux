package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	out    map[string]string
}

func (f *fakeGitRunner) Run(dir string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{"@" + dir}, args...))
	if f.failOn[strings.Join(args, " ")] {
		return "", errors.New("git failed: " + strings.Join(args, " "))
	}
	return f.out[strings.Join(args, " ")], nil
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
		// Output text matters for has-session: HasSession only treats
		// "can't find session"/"error connecting to" as a real absence, not
		// every exit-1 failure. Every failOn in this file simulates a
		// not-yet-created session, so this text keeps that behavior; it's a
		// no-op for other commands, which don't inspect the output text.
		return "can't find session: " + key, exitErr{}
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
	git := &fakeGitRunner{failOn: map[string]bool{}, out: map[string]string{}}
	tm := &fakeTmuxRunner{out: map[string]string{}, failOn: map[string]bool{}}
	term := &fakeTerminal{}
	store := &session.Store{Path: filepath.Join(dir, "sessions.json")}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.toml")
	cfg := &config.Config{Projects: projects}
	// Persist the fixture's initial projects: App methods reload config
	// from CfgPath before every mutation (matching the real startup path,
	// where Cfg always originates from config.Load(CfgPath)), so an
	// in-memory-only Cfg with nothing on disk would look like every
	// project vanished the moment any mutating method ran.
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	a := &App{
		Cfg:          cfg,
		CfgPath:      cfgPath,
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

func TestSendPromptTypesIntoSession(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	if err := a.SendPrompt("moomux-foo", "fix the bug"); err != nil {
		t.Fatal(err)
	}
	want := []string{"send-keys", "-t", "=moomux-foo:", "fix the bug", "Enter"}
	if len(tm.calls) != 1 || !reflect.DeepEqual(tm.calls[0], want) {
		t.Fatalf("calls = %v", tm.calls)
	}
}

func TestSendPromptEmptyIsNoop(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	if err := a.SendPrompt("moomux-foo", ""); err != nil {
		t.Fatal(err)
	}
	if len(tm.calls) != 0 {
		t.Fatalf("expected no tmux calls, got %v", tm.calls)
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
	_ = a.Store.Put(session.Session{ID: "demo:z", Project: "demo", Name: "z", Agent: "codex", AgentPort: 4102})
	if got := a.nextOpenCodePort(); got != 4103 {
		t.Fatalf("retained port: got %d, want 4103", got)
	}
}

func TestTmuxSessionNameUniqueAcrossProjects(t *testing.T) {
	a := TmuxSessionName("proja:feat", "feat")
	b := TmuxSessionName("projb:feat", "feat")
	if a == b {
		t.Fatalf("same tmux name %q for two projects", a)
	}
	if !strings.HasPrefix(a, "moomux-feat-") {
		t.Fatalf("name = %q", a)
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
	tn := TmuxSessionName("demo:feat", "feat")
	tm.out["list-panes -t ="+tn+": -F #{pane_id}"] = "%0\n"
	noBranch(git, "feat")

	s, hint, err := a.CreateSession("demo", "feat", "", "", "https://ticket/1")
	if err != nil {
		t.Fatal(err)
	}
	if hint != "" {
		t.Fatalf("hint = %q", hint)
	}
	wantWt := filepath.Join(a.WorktreeRoot, "demo", "feat")
	if s.WorktreePath != wantWt || s.Branch != "feat" || !s.NewBranch || s.TmuxSession != tn || s.Ticket != "https://ticket/1" {
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
	if !tm.called("new-session -d -s " + tn + " -c " + wantWt) {
		t.Fatalf("no tmux new-session; calls = %v", tm.calls)
	}
	if len(term.calls) != 1 || term.calls[0] != [2]string{tn, "feat"} {
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
	tm.out["list-panes -t ="+TmuxSessionName("demo:feat", "feat")+": -F #{pane_id}"] = "%0\n"
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
	tm.out["list-panes -t ="+TmuxSessionName("demo:login-page", "login-page")+": -F #{pane_id}"] = "%0\n"

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

func TestCreateSessionExistingBranchRemovesStaleCleanWorktree(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	tm.out["list-panes -t ="+TmuxSessionName("demo:login-page", "login-page")+": -F #{pane_id}"] = "%0\n"
	staleWT := filepath.Join(a.WorktreeRoot, "demo", "old-login-page")
	git.out["worktree list --porcelain"] = "worktree " + staleWT + "\nbranch refs/heads/feature/login-page\n"

	s, _, err := a.CreateSession("demo", "", "", "feature/login-page", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Branch != "feature/login-page" {
		t.Fatalf("session = %+v", s)
	}
	wantRemove := "@/repo worktree remove " + staleWT + " --force"
	found := false
	for _, c := range git.calls {
		if strings.Join(c, " ") == wantRemove {
			found = true
		}
	}
	if !found {
		t.Fatalf("no stale worktree removal; calls = %v", git.calls)
	}
}

func TestCreateSessionExistingBranchLiveStaleWorktreeBlocks(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	staleWT := filepath.Join(a.WorktreeRoot, "demo", "old-login-page")
	git.out["worktree list --porcelain"] = "worktree " + staleWT + "\nbranch refs/heads/feature/login-page\n"

	staleTmux := TmuxSessionName("demo:old-login-page", "old-login-page")
	if err := a.Store.Put(session.Session{
		ID:           "demo:old-login-page",
		Project:      "demo",
		Name:         "old-login-page",
		Branch:       "feature/login-page",
		WorktreePath: staleWT,
		TmuxSession:  staleTmux,
	}); err != nil {
		t.Fatal(err)
	}
	// has-session succeeds by default (no failOn entry), simulating a still-live pane.

	_, _, err := a.CreateSession("demo", "", "", "feature/login-page", "")
	if err == nil {
		t.Fatal("expected error for stale worktree still in use by a live tmux session")
	}
	for _, c := range git.calls {
		if strings.HasPrefix(strings.Join(c, " "), "@/repo worktree remove") {
			t.Fatalf("should not remove worktree in use by a live tmux session; calls = %v", git.calls)
		}
	}
	if !tm.called("has-session -t =" + staleTmux) {
		t.Fatalf("expected HasSession check for stale tmux session; calls = %v", tm.calls)
	}
}

func TestCreateSessionExistingBranchDirtyStaleWorktreeBlocks(t *testing.T) {
	a, git, _, _ := newTestApp(t, gitProject("/repo"))
	staleWT := filepath.Join(a.WorktreeRoot, "demo", "old-login-page")
	git.out["worktree list --porcelain"] = "worktree " + staleWT + "\nbranch refs/heads/feature/login-page\n"
	git.out["status --porcelain"] = " M dirty/file.go\n"

	_, _, err := a.CreateSession("demo", "", "", "feature/login-page", "")
	if err == nil {
		t.Fatal("expected error for dirty stale worktree")
	}
	for _, c := range git.calls {
		if strings.HasPrefix(strings.Join(c, " "), "@/repo worktree remove") {
			t.Fatalf("should not remove dirty worktree; calls = %v", git.calls)
		}
	}
}

func TestCreateSessionOpenCodePorts(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	tm.out["list-panes -t ="+TmuxSessionName("demo:one", "one")+": -F #{pane_id}"] = "%0\n"
	tm.out["list-panes -t ="+TmuxSessionName("demo:two", "two")+": -F #{pane_id}"] = "%0\n"
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
	tm.out["list-panes -t ="+TmuxSessionName("notes:todo", "todo")+": -F #{pane_id}"] = "%0\n"

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

func TestCreateSessionRejectsBogusAgent(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	if _, _, err := a.CreateSession("demo", "feat", "clude", "", ""); err == nil {
		t.Fatal("bogus agent must be rejected, not silently coerced to claude")
	}
}

func TestCreateSessionRejectsProjectDefaultBogusAgent(t *testing.T) {
	// A hand-edited config.toml with a typo'd project-level agent must fail
	// session creation instead of silently launching claude while storing
	// the bogus name (agentCmd's switch defaults unrecognized values to "claude").
	projects := map[string]config.Project{
		"demo": {Kind: "git", Repo: "/repo", BaseBranch: "main", Agent: "clude"},
	}
	a, _, _, _ := newTestApp(t, projects)
	if _, _, err := a.CreateSession("demo", "feat", "", "", ""); err == nil {
		t.Fatal("bogus project-level agent must be rejected")
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

	// tmux new-session fails: the worktree git just created is useless
	// without a session to run it in, so CreateSession must clean it up
	// rather than leaving an orphaned checkout behind.
	noBranch(git, "tmuxfail")
	tmuxfailWt := filepath.Join(a.WorktreeRoot, "demo", "tmuxfail")
	tm.failOn["new-session -d -s "+TmuxSessionName("demo:tmuxfail", "tmuxfail")+" -c "+tmuxfailWt+" -n tmuxfail"] = true
	if _, _, err := a.CreateSession("demo", "tmuxfail", "", "", ""); err == nil || !strings.Contains(err.Error(), "tmux new-session") {
		t.Fatalf("err = %v", err)
	}
	wantRemove := "@/repo worktree remove " + tmuxfailWt + " --force"
	found := false
	for _, c := range git.calls {
		if strings.Join(c, " ") == wantRemove {
			found = true
		}
	}
	if !found {
		t.Fatalf("no worktree cleanup after tmux failure; calls = %v", git.calls)
	}

	// terminal open fails: the worktree and tmux session already exist by
	// this point, so CreateSession degrades to a manual-attach hint instead
	// of failing and stranding them outside the store.
	noBranch(git, "termfail")
	termfailTn := TmuxSessionName("demo:termfail", "termfail")
	tm.out["list-panes -t ="+termfailTn+": -F #{pane_id}"] = "%0\n"
	term.err = errors.New("no terminal")
	s, hint, err := a.CreateSession("demo", "termfail", "", "", "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if s.TmuxSession != termfailTn {
		t.Fatalf("session = %+v", s)
	}
	if !strings.Contains(hint, "tmux attach -t "+termfailTn) {
		t.Fatalf("hint = %q", hint)
	}

	// store.Put fails too: the tmux session is already running (and, per
	// above, the terminal-open failure already produced a manual-attach
	// hint) — that hint must survive in the returned error instead of being
	// lost, since ErrorMsg only ever surfaces err.Error() to the user.
	noBranch(git, "storefail")
	storefailTn := TmuxSessionName("demo:storefail", "storefail")
	tm.out["list-panes -t ="+storefailTn+": -F #{pane_id}"] = "%0\n"
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a.Store.Path = filepath.Join(blocker, "sessions.json")
	_, hint, err = a.CreateSession("demo", "storefail", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "store:") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "tmux attach -t "+storefailTn) {
		t.Fatalf("store-put error dropped the manual-attach hint: %v", err)
	}
	if !strings.Contains(hint, "tmux attach -t "+storefailTn) {
		t.Fatalf("hint = %q", hint)
	}
}

func TestOpenSessionAlive(t *testing.T) {
	a, _, tm, term := newTestApp(t, gitProject("/repo"))
	term.hint = "run: tmux attach -t moomux-feat"
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat",
		WorktreePath: "/wt/feat", Agent: "codex",
	})
	tm.out["list-panes -t =moomux-feat: -F #{pane_current_path}"] = "/wt/feat\n"

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

func TestOpenSessionDeadAllocatesOpenCodePort(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{
		ID: "demo:oc", Project: "demo", Name: "oc", TmuxSession: "moomux-oc",
		WorktreePath: "/wt/oc", Agent: "opencode",
	})
	tm.failOn["has-session -t =moomux-oc"] = true
	// The dead session gets lazily migrated to the hashed name before recreation.
	tm.out["list-panes -t ="+TmuxSessionName("demo:oc", "oc")+": -F #{pane_id}"] = "%0\n"

	if _, err := a.OpenSession("demo:oc"); err != nil {
		t.Fatal(err)
	}
	if !tm.called("send-keys -t %0 opencode --port 4096 Enter") {
		t.Fatalf("expected allocated opencode port; calls = %v", tm.calls)
	}
	got, ok := a.Store.Get("demo:oc")
	if !ok || got.AgentPort != 4096 {
		t.Fatalf("stored session = %+v, ok=%v", got, ok)
	}
}

func TestOpenSessionCwdMismatchRecreates(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat", WorktreePath: "/wt/feat"})
	tn := TmuxSessionName("demo:feat", "feat")
	tm.out["list-panes -t =moomux-feat: -F #{pane_current_path}"] = "/somewhere/else\n"
	tm.out["list-panes -t ="+tn+": -F #{pane_id}"] = "%0\n"

	if _, err := a.OpenSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	if !tm.called("kill-session -t =moomux-feat") {
		t.Fatalf("expected kill-session; calls = %v", tm.calls)
	}
	// Recreation happens under the lazily-migrated hashed name.
	if !tm.called("new-session -d -s " + tn + " -c /wt/feat") {
		t.Fatalf("expected recreation; calls = %v", tm.calls)
	}
}

func TestOpenSessionDeadRecreatesWithAgent(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{
		ID: "demo:oc", Project: "demo", Name: "oc", TmuxSession: "moomux-oc",
		WorktreePath: "/wt/oc", Agent: "opencode", AgentPort: 4099,
	})
	tm.failOn["has-session -t =moomux-oc"] = true
	tm.out["list-panes -t ="+TmuxSessionName("demo:oc", "oc")+": -F #{pane_id}"] = "%0\n"

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
	if !tm.called("kill-session -t =moomux-a") {
		t.Fatalf("calls = %v", tm.calls)
	}

	// dead session: no-op, no error
	tm.calls = nil
	tm.failOn["has-session -t =moomux-a"] = true
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

// TestProjectMutationsSurviveConcurrentWriter mirrors the session store's
// TestConcurrentWriterSurvivesMutation: two App instances (e.g. two moomux
// TUIs, or a TUI + `moomux spawn`) sharing the same config.toml must not
// let one's write clobber a project the other added in the meantime.
func TestProjectMutationsSurviveConcurrentWriter(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.Save(cfgPath, &config.Config{Projects: map[string]config.Project{}}); err != nil {
		t.Fatal(err)
	}

	newApp := func() *App {
		return &App{
			Cfg:          &config.Config{Projects: map[string]config.Project{}},
			CfgPath:      cfgPath,
			Store:        &session.Store{Path: filepath.Join(dir, "sessions.json")},
			Tmux:         &tmux.Client{Runner: &fakeTmuxRunner{out: map[string]string{}, failOn: map[string]bool{}}},
			Terminal:     &fakeTerminal{},
			Git:          &gitwt.Client{Runner: &fakeGitRunner{failOn: map[string]bool{}, out: map[string]string{}}},
			WorktreeRoot: filepath.Join(dir, "worktrees"),
		}
	}
	first, second := newApp(), newApp()

	if err := second.AddPlainProject("beta", config.Project{Repo: filepath.Join(dir, "beta")}); err != nil {
		t.Fatal(err)
	}
	// first's Cfg was built before beta was added; its own mutation must
	// not overwrite beta with a stale in-memory snapshot.
	if err := first.AddPlainProject("alpha", config.Project{Repo: filepath.Join(dir, "alpha")}); err != nil {
		t.Fatal(err)
	}

	final, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := final.Projects["beta"]; !ok {
		t.Fatal("project \"beta\" added by a concurrent App was lost")
	}
	if _, ok := final.Projects["alpha"]; !ok {
		t.Fatal("project \"alpha\" missing")
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

func TestAddProjectRejectsBogusAgent(t *testing.T) {
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	a, _, _, _ := newTestApp(t, map[string]config.Project{})

	if err := a.AddProject("demo", config.Project{Repo: repo, Agent: "clude"}); err == nil {
		t.Fatal("bogus agent must be rejected")
	}
	if _, ok := a.Cfg.Projects["demo"]; ok {
		t.Fatal("rejected project must not be saved")
	}
}

func TestUpdateProject(t *testing.T) {
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	a, _, _, _ := newTestApp(t, map[string]config.Project{
		"demo": {Kind: "git", Repo: repo, BaseBranch: "main", Agent: "claude"},
	})

	updated := config.Project{
		Repo: repo, BaseBranch: "trunk", BranchPrefix: "alan",
		Agent: "codex", NoWorktree: true,
	}
	if err := a.UpdateProject("demo", updated); err != nil {
		t.Fatal(err)
	}
	got := a.Cfg.Projects["demo"]
	if got.Kind != "git" || got.BaseBranch != "trunk" ||
		got.BranchPrefix != "alan" || got.Agent != "codex" || !got.NoWorktree {
		t.Fatalf("project = %+v", got)
	}

	loaded, err := config.Load(a.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Projects["demo"]; got.Agent != "codex" || got.Kind != "git" {
		t.Fatalf("persisted project = %+v", got)
	}
}

func TestUpdatePlainProjectPreservesKindAndClearsGitSettings(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "notes")
	a, _, _, _ := newTestApp(t, map[string]config.Project{
		"notes": {Kind: "plain", Repo: t.TempDir(), Agent: "claude"},
	})

	err := a.UpdateProject("notes", config.Project{
		Kind: "git", Repo: repo, BaseBranch: "main", BranchPrefix: "x",
		Agent: "opencode", NoWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := a.Cfg.Projects["notes"]
	if got.Kind != "plain" || got.Repo != repo || got.Agent != "opencode" ||
		got.BaseBranch != "" || got.BranchPrefix != "" || got.NoWorktree {
		t.Fatalf("project = %+v", got)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("plain repo was not created: %v", err)
	}
}

func TestUpdateProjectNoWorktreeFlipBlockedWithSessions(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:feat", Project: "demo", Name: "feat", WorktreePath: "/wt/feat"})

	p := a.Cfg.Projects["demo"]
	p.Agent = "claude"
	p.NoWorktree = true
	if err := a.UpdateProject("demo", p); err == nil {
		t.Fatal("flipping worktree mode with live sessions must fail")
	}
	if a.Cfg.Projects["demo"].NoWorktree {
		t.Fatal("config must be unchanged after rejected update")
	}
}

func TestUpdateProjectValidationAndRollback(t *testing.T) {
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	original := config.Project{Kind: "git", Repo: repo, BaseBranch: "main", Agent: "claude"}
	a, _, _, _ := newTestApp(t, map[string]config.Project{"demo": original})

	if err := a.UpdateProject("missing", original); err == nil {
		t.Fatal("unknown project must fail")
	}
	invalid := original
	invalid.Agent = "other"
	if err := a.UpdateProject("demo", invalid); err == nil {
		t.Fatal("unknown agent must fail")
	}
	invalid = original
	invalid.Repo = t.TempDir()
	if err := a.UpdateProject("demo", invalid); !errors.Is(err, gitwt.ErrNotGitRepo) {
		t.Fatalf("non-repository error = %v", err)
	}

	a.CfgPath = t.TempDir()
	updated := original
	updated.Agent = "codex"
	if err := a.UpdateProject("demo", updated); err == nil {
		t.Fatal("config write must fail")
	}
	if got := a.Cfg.Projects["demo"]; got != original {
		t.Fatalf("project after rollback = %+v, want %+v", got, original)
	}
}

// TestUpdateProjectAcceptsDefaultAgent guards against validating the raw
// Agent field instead of its resolved name: Agent == "" means "use the
// default" (see config.Project.AgentName), the same legitimate value
// validateProject already accepts, and must not be rejected as unknown.
func TestUpdateProjectAcceptsDefaultAgent(t *testing.T) {
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	original := config.Project{Kind: "git", Repo: repo, BaseBranch: "main", Agent: "claude"}
	a, _, _, _ := newTestApp(t, map[string]config.Project{"demo": original})

	defaulted := original
	defaulted.Agent = ""
	if err := a.UpdateProject("demo", defaulted); err != nil {
		t.Fatalf("default agent must be accepted: %v", err)
	}
}

func TestSetSessionAgentDoesNotTouchTmux(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	original := session.Session{
		ID: "demo:a", Project: "demo", Name: "a",
		Agent: "claude", AgentPort: 4099,
	}
	if err := a.Store.Put(original); err != nil {
		t.Fatal(err)
	}
	tm.calls = nil

	got, err := a.SetSessionAgent(original.ID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent != "codex" || got.AgentPort != 4099 {
		t.Fatalf("session = %+v", got)
	}
	if len(tm.calls) != 0 {
		t.Fatalf("tmux calls = %v", tm.calls)
	}

	stored, ok := a.Store.Get(original.ID)
	if !ok || stored.Agent != "codex" {
		t.Fatalf("stored session = %+v, ok=%v", stored, ok)
	}
}

func TestSetSessionAgentValidationAndRollback(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	original := session.Session{ID: "demo:a", Project: "demo", Name: "a", Agent: "claude"}
	if err := a.Store.Put(original); err != nil {
		t.Fatal(err)
	}

	if _, err := a.SetSessionAgent("demo:missing", "codex"); err == nil {
		t.Fatal("unknown session must fail")
	}
	if _, err := a.SetSessionAgent(original.ID, "other"); err == nil {
		t.Fatal("unknown agent must fail")
	}

	a.Store.Path = t.TempDir()
	if _, err := a.SetSessionAgent(original.ID, "codex"); err == nil {
		t.Fatal("session write must fail")
	}
	got, ok := a.Store.Get(original.ID)
	if !ok || got != original {
		t.Fatalf("session after rollback = %+v, ok=%v", got, ok)
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
	if err := a.RemoveProject("demo"); err == nil || !strings.Contains(err.Error(), "still has sessions") {
		t.Fatalf("err = %v", err)
	}
	// archived sessions block removal too
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", Archived: true})
	if err := a.RemoveProject("demo"); err == nil || !strings.Contains(err.Error(), "still has sessions") {
		t.Fatalf("archived err = %v", err)
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
	if !tm.called("kill-session -t =moomux-feat") {
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

// A worktree another session already deleted must not block this delete.
func TestDeleteSessionMissingWorktree(t *testing.T) {
	a, git, _, _ := newTestApp(t, gitProject("/repo"))
	wt := filepath.Join(a.WorktreeRoot, "demo", "feat") // gone from disk and from git
	git.failOn["worktree remove "+wt+" --force"] = true
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", Branch: "feat", NewBranch: true,
		TmuxSession: "moomux-feat", WorktreePath: wt,
	})

	if err := a.DeleteSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Store.Get("demo:feat"); ok {
		t.Fatal("session still in store")
	}
}

func TestCreateSessionDuplicateName(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	tm.out["list-panes -t =moomux-feat: -F #{pane_id}"] = "%0\n"
	noBranch(git, "feat")
	if _, _, err := a.CreateSession("demo", "feat", "", "", ""); err != nil {
		t.Fatal(err)
	}
	_, _, err := a.CreateSession("demo", "feat", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
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
	// Session whose project was removed from config: its worktree dir is
	// cleaned up (no git calls) — but only when moomux created it, i.e. it
	// lives under WorktreeRoot.
	a, git, _, _ := newTestApp(t, map[string]config.Project{})
	wt := filepath.Join(a.WorktreeRoot, "gone", "x")
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

func TestDeleteSessionOrphanedProjectKeepsRealFolder(t *testing.T) {
	// For a plain/no-worktree session, WorktreePath is the user's actual
	// project folder. If the project vanishes from config, deleting the
	// session must NOT delete that folder.
	a, _, _, _ := newTestApp(t, map[string]config.Project{})
	repo := filepath.Join(t.TempDir(), "real-project")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Put(session.Session{ID: "gone:x", Project: "gone", Name: "x", TmuxSession: "moomux-x", WorktreePath: repo})

	if err := a.DeleteSession("gone:x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("user's project folder was deleted: %v", err)
	}
	if _, ok := a.Store.Get("gone:x"); ok {
		t.Fatal("store entry should still be deleted")
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
