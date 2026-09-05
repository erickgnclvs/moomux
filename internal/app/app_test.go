package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/erickgnclvs/moomux/internal/codexhook"
	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tmux"
	"github.com/erickgnclvs/moomux/internal/watcher"
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
	// seq, when set for a key, returns successive values on each call to
	// that key (staying on the last one once exhausted) instead of out's
	// fixed value — used to simulate pane content actually changing across
	// polls (e.g. idle shell prompt -> agent startup -> agent idle).
	seq    map[string][]string
	seqIdx map[string]int
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
	if vals, ok := f.seq[key]; ok && len(vals) > 0 {
		if f.seqIdx == nil {
			f.seqIdx = map[string]int{}
		}
		i := f.seqIdx[key]
		if i >= len(vals) {
			i = len(vals) - 1
		}
		f.seqIdx[key] = i + 1
		return vals[i], nil
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

// fakeTabTerminal implements terminal.TabReopener, mimicking iTerm2: it
// records the tabID it was asked to reopen and always claims to find it
// (or, if newTabID is set, reports it as freshly created instead).
type fakeTabTerminal struct {
	gotTabID string
	newTabID string
}

func (f *fakeTabTerminal) OpenSession(tmuxSession, title string) (string, error) {
	return "", nil
}

func (f *fakeTabTerminal) OpenTab(tabID, tmuxSession, title string) (string, string, error) {
	f.gotTabID = tabID
	if f.newTabID != "" {
		return f.newTabID, "", nil
	}
	return tabID, "", nil
}

// fakeCloseTabTerminal implements terminal.TabCloser (and the base
// TerminalOpener), mimicking iTerm2, to test KillTmux's tab-closing path.
type fakeCloseTabTerminal struct {
	closed []string
	err    error
}

func (f *fakeCloseTabTerminal) OpenSession(tmuxSession, title string) (string, error) {
	return "", nil
}

func (f *fakeCloseTabTerminal) CloseTab(tabID string) error {
	f.closed = append(f.closed, tabID)
	return f.err
}

// noBranch marks the rev-parse existence check for branch as failing, i.e.
// "branch does not exist yet" — the normal case when creating a session.
func noBranch(fr *fakeGitRunner, branch string) {
	fr.failOn["rev-parse --verify --quiet refs/heads/"+branch] = true
}

func newTestApp(t *testing.T, projects map[string]config.Project) (*App, *fakeGitRunner, *fakeTmuxRunner, *fakeTerminal) {
	t.Helper()
	dir := t.TempDir()
	// Both claudehook and codexhook's needs-input installers write into the
	// real os.UserHomeDir() by design (global install — see their doc
	// comments). Sandbox HOME so no test run touches the developer's actual
	// ~/.claude/settings.json or ~/.codex/hooks.json.
	t.Setenv("HOME", t.TempDir())
	// Sandbox against the developer's own remote-session signals (e.g. this
	// suite may itself run inside Moshi) so tests assuming a local
	// environment aren't at the mercy of ambient env vars; tests that want
	// browser.Remote() to report true set these explicitly.
	t.Setenv("SSH_TTY", "")
	t.Setenv("MOSHI_CLIENT", "")
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

	s, hint, err := a.CreateSession("demo", "feat", "", "", "https://ticket/1", true, false)
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

func TestCreateSessionInstallsClaudeHooks(t *testing.T) {
	// Claude hooks install globally (see claudehook.EnsureHooksInstalled's
	// doc comment), not per-worktree — newTestApp already sandboxes HOME so
	// this doesn't touch the real developer's ~/.claude/settings.json.
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	tn := TmuxSessionName("demo:feat", "feat")
	tm.out["list-panes -t ="+tn+": -F #{pane_id}"] = "%0\n"
	noBranch(git, "feat")

	if _, _, err := a.CreateSession("demo", "feat", "claude", "", "", true, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("expected ~/.claude/settings.json to be written: %v", err)
	}
	if !strings.Contains(string(data), "moomux hook claude set") || !strings.Contains(string(data), "moomux hook claude clear") {
		t.Fatalf("settings.json missing moomux hooks: %s", data)
	}
}

func TestCreateSessionInstallsTagCommand(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	tn := TmuxSessionName("demo:feat", "feat")
	tm.out["list-panes -t ="+tn+": -F #{pane_id}"] = "%0\n"
	noBranch(git, "feat")

	if _, _, err := a.CreateSession("demo", "feat", "claude", "", "", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "commands", "tag.md")); err != nil {
		t.Fatalf("expected ~/.claude/commands/tag.md to be written: %v", err)
	}
}

func TestCreateSessionSkipsClaudeHooksForOtherAgents(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	tn := TmuxSessionName("demo:feat", "feat")
	tm.out["list-panes -t ="+tn+": -F #{pane_id}"] = "%0\n"
	noBranch(git, "feat")

	// opencode has no needs-input installer at all, unlike claude/codex —
	// picking it here keeps this test's assertion (no settings.json)
	// meaningful without also asserting anything about claude's or codex's
	// own hooks file.
	if _, _, err := a.CreateSession("demo", "feat", "opencode", "", "", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no ~/.claude/settings.json for a non-claude agent, stat err = %v", err)
	}
}

func TestCreateSessionInstallsCodexHooks(t *testing.T) {
	// Codex hooks install globally (see codexhook.EnsureHooks's doc comment),
	// not per-worktree — newTestApp already sandboxes HOME so this doesn't
	// touch the real developer's ~/.codex/hooks.json.
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	tn := TmuxSessionName("demo:feat", "feat")
	tm.out["list-panes -t ="+tn+": -F #{pane_id}"] = "%0\n"
	noBranch(git, "feat")

	if _, _, err := a.CreateSession("demo", "feat", "codex", "", "", true, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("expected ~/.codex/hooks.json to be written: %v", err)
	}
	if !strings.Contains(string(data), "moomux hook codex set") || !strings.Contains(string(data), "moomux hook codex clear") {
		t.Fatalf("hooks.json missing moomux hooks: %s", data)
	}
}

func TestCreateSessionInstallsKillCommand(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	tn := TmuxSessionName("demo:feat", "feat")
	tm.out["list-panes -t ="+tn+": -F #{pane_id}"] = "%0\n"
	noBranch(git, "feat")

	if _, _, err := a.CreateSession("demo", "feat", "claude", "", "", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "commands", "kill.md")); err != nil {
		t.Fatalf("expected ~/.claude/commands/kill.md to be written: %v", err)
	}
}

func TestCreateSessionInstallsKillPrompt(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	tn := TmuxSessionName("demo:feat", "feat")
	tm.out["list-panes -t ="+tn+": -F #{pane_id}"] = "%0\n"
	noBranch(git, "feat")

	if _, _, err := a.CreateSession("demo", "feat", "codex", "", "", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "prompts", "kill.md")); err != nil {
		t.Fatalf("expected ~/.codex/prompts/kill.md to be written: %v", err)
	}
}

func TestCreateSessionDangerousAppendsAgentFlag(t *testing.T) {
	cases := []struct {
		agent   string
		wantCmd string
	}{
		{"claude", "claude --dangerously-skip-permissions"},
		{"codex", "codex --yolo"},
		{"opencode", "opencode --port 4096"}, // no flag: opencode has none to append
	}
	for _, tc := range cases {
		t.Run(tc.agent, func(t *testing.T) {
			a, git, tm, _ := newTestApp(t, gitProject("/repo"))
			tn := TmuxSessionName("demo:feat", "feat")
			tm.out["list-panes -t ="+tn+": -F #{pane_id}"] = "%0\n"
			noBranch(git, "feat")

			s, _, err := a.CreateSession("demo", "feat", tc.agent, "", "", true, true)
			if err != nil {
				t.Fatal(err)
			}
			if !s.Dangerous {
				t.Fatalf("session.Dangerous = false, want true")
			}
			found := false
			for _, c := range tm.calls {
				if slices.Contains(c, tc.wantCmd) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no send-keys with %q; calls = %v", tc.wantCmd, tm.calls)
			}
		})
	}
}

func TestCreateSessionBranchPrefix(t *testing.T) {
	projects := map[string]config.Project{
		"demo": {Kind: "git", Repo: "/repo", BaseBranch: "main", BranchPrefix: "user"},
	}
	a, git, tm, _ := newTestApp(t, projects)
	tm.out["list-panes -t ="+TmuxSessionName("demo:feat", "feat")+": -F #{pane_id}"] = "%0\n"
	noBranch(git, "user/feat")

	s, _, err := a.CreateSession("demo", "feat", "", "", "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Branch != "user/feat" {
		t.Fatalf("branch = %q", s.Branch)
	}
}

func TestCreateSessionBranchPrefixTrailingSlash(t *testing.T) {
	projects := map[string]config.Project{
		"demo": {Kind: "git", Repo: "/repo", BaseBranch: "main", BranchPrefix: "user/"},
	}
	a, git, tm, _ := newTestApp(t, projects)
	tm.out["list-panes -t ="+TmuxSessionName("demo:feat", "feat")+": -F #{pane_id}"] = "%0\n"
	noBranch(git, "user/feat")

	s, _, err := a.CreateSession("demo", "feat", "", "", "", true, false)
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

	s, _, err := a.CreateSession("demo", "", "", "feature/login-page", "", true, false)
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

// A branch name typed into the branch field that doesn't resolve anywhere
// (no local branch, no origin/<branch>) is a typo: fail with a message the
// user can act on, and create nothing, so the form can stay open for a fix.
func TestCreateSessionUnknownBranchFailsWithoutCreating(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	tm.out["list-panes -t ="+TmuxSessionName("demo:merchant-physical", "merchant-physical")+": -F #{pane_id}"] = "%0\n"
	noBranch(git, "merchant-physical")
	git.failOn["rev-parse --verify --quiet refs/remotes/origin/merchant-physical"] = true

	_, _, err := a.CreateSession("demo", "", "", "merchant-physical", "", true, false)
	if err == nil {
		t.Fatal("want an error for a branch that doesn't exist")
	}
	if !strings.Contains(err.Error(), "no branch \"merchant-physical\"") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range git.calls {
		if slices.Contains(c, "add") && slices.Contains(c, "worktree") {
			t.Fatalf("worktree was created anyway: %v", git.calls)
		}
	}
	if len(a.Store.All()) != 0 {
		t.Fatalf("session stored despite failure: %v", a.Store.All())
	}
}

func TestCreateSessionExistingBranchRemovesStaleCleanWorktree(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	tm.out["list-panes -t ="+TmuxSessionName("demo:login-page", "login-page")+": -F #{pane_id}"] = "%0\n"
	staleWT := filepath.Join(a.WorktreeRoot, "demo", "old-login-page")
	git.out["worktree list --porcelain"] = "worktree " + staleWT + "\nbranch refs/heads/feature/login-page\n"

	s, _, err := a.CreateSession("demo", "", "", "feature/login-page", "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Branch != "feature/login-page" {
		t.Fatalf("session = %+v", s)
	}
	wantRemove := "@/repo worktree remove " + staleWT + " --force --force"
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

	_, _, err := a.CreateSession("demo", "", "", "feature/login-page", "", true, false)
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

	_, _, err := a.CreateSession("demo", "", "", "feature/login-page", "", true, false)
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

	s1, _, err := a.CreateSession("demo", "one", "opencode", "", "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if s1.AgentPort != 4096 {
		t.Fatalf("port = %d", s1.AgentPort)
	}
	s2, _, err := a.CreateSession("demo", "two", "opencode", "", "", true, false)
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

	s, _, err := a.CreateSession("notes", "todo", "", "", "", true, false)
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
	if _, _, err := a.CreateSession("demo", "feat", "clude", "", "", true, false); err == nil {
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
	if _, _, err := a.CreateSession("demo", "feat", "", "", "", true, false); err == nil {
		t.Fatal("bogus project-level agent must be rejected")
	}
}

func TestCreateSessionErrors(t *testing.T) {
	a, git, tm, term := newTestApp(t, gitProject("/repo"))

	if _, _, err := a.CreateSession("nope", "x", "", "", "", true, false); err == nil {
		t.Fatal("unknown project must fail")
	}
	if _, _, err := a.CreateSession("demo", "", "", "", "", true, false); err == nil {
		t.Fatal("empty name+branch must fail")
	}

	// git worktree add fails
	noBranch(git, "bad")
	git.failOn["worktree add "+filepath.Join(a.WorktreeRoot, "demo", "bad")+" -b bad origin/main"] = true
	if _, _, err := a.CreateSession("demo", "bad", "", "", "", true, false); err == nil || !strings.Contains(err.Error(), "git worktree add") {
		t.Fatalf("err = %v", err)
	}

	// tmux new-session fails
	noBranch(git, "tmuxfail")
	tm.failOn["new-session -d -s "+TmuxSessionName("demo:tmuxfail", "tmuxfail")+" -c "+filepath.Join(a.WorktreeRoot, "demo", "tmuxfail")+" -n tmuxfail"] = true
	if _, _, err := a.CreateSession("demo", "tmuxfail", "", "", "", true, false); err == nil || !strings.Contains(err.Error(), "tmux new-session") {
		t.Fatalf("err = %v", err)
	}

	// terminal open fails: the worktree and tmux session already exist by
	// this point, so CreateSession degrades to a manual-attach hint instead
	// of failing and stranding them outside the store.
	noBranch(git, "termfail")
	termfailTn := TmuxSessionName("demo:termfail", "termfail")
	tm.out["list-panes -t ="+termfailTn+": -F #{pane_id}"] = "%0\n"
	term.err = errors.New("no terminal")
	s, hint, err := a.CreateSession("demo", "termfail", "", "", "", true, false)
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
	_, hint, err = a.CreateSession("demo", "storefail", "", "", "", true, false)
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
	// codexhook.EnsureHooks (invoked via repairNeedsInputHooks, since this
	// session's agent is codex) installs into the real os.UserHomeDir() by
	// design (see its doc comment) — newTestApp already sandboxes HOME.
	// Pre-install codex's hooks so repairNeedsInputHooks's call is a no-op
	// (changed=false): this test is about alive-session reuse behavior, not
	// about the hooks-hint text (covered by TestOpenSessionRepairsMissingCodexHooks),
	// so this keeps its hint assertion focused on what OpenSession actually
	// returned for terminal reuse.
	home, _ := os.UserHomeDir()
	if _, err := codexhook.EnsureHooks(home); err != nil {
		t.Fatal(err)
	}

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

func TestOpenSessionStampsLastOpened(t *testing.T) {
	a, _, tm, term := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	if _, err := codexhook.EnsureHooks(home); err != nil {
		t.Fatal(err)
	}
	term.hint = "run: tmux attach -t moomux-feat"
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat",
		WorktreePath: "/wt/feat", Agent: "codex",
	})
	tm.out["list-panes -t =moomux-feat: -F #{pane_current_path}"] = "/wt/feat\n"

	before := time.Now()
	if _, err := a.OpenSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	sess, ok := a.Store.Get("demo:feat")
	if !ok {
		t.Fatal("session vanished")
	}
	if sess.LastOpened.Before(before) {
		t.Fatalf("LastOpened = %v, want at/after %v", sess.LastOpened, before)
	}
}

func TestOpenSessionOverSSHSkipsTerminalTab(t *testing.T) {
	a, _, tm, term := newTestApp(t, gitProject("/repo"))
	t.Setenv("SSH_TTY", "/dev/ttys001")
	// Pre-install codex's hooks so repairNeedsInputHooks's call is a no-op;
	// see TestOpenSessionAlive for why.
	home, _ := os.UserHomeDir()
	if _, err := codexhook.EnsureHooks(home); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat",
		WorktreePath: "/wt/feat", Agent: "codex",
	})
	tm.out["list-panes -t =moomux-feat: -F #{pane_current_path}"] = "/wt/feat\n"

	hint, err := a.OpenSession("demo:feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(term.calls) != 0 {
		t.Fatalf("must not open a terminal tab over SSH; calls = %v", term.calls)
	}
	if !tm.called("has-session") {
		t.Fatalf("tmux session must still be ensured over SSH; calls = %v", tm.calls)
	}
	if want := "tmux attach -t moomux-feat"; hint != want {
		t.Fatalf("hint = %q, want %q", hint, want)
	}
}

func TestOpenSessionReusesStoredItermTab(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	fakeTab := &fakeTabTerminal{}
	a.Terminal = fakeTab
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat",
		WorktreePath: "/wt/feat", TermTabID: "tab-42",
	})
	tm.out["list-panes -t =moomux-feat: -F #{pane_current_path}"] = "/wt/feat\n"

	if _, err := a.OpenSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	if fakeTab.gotTabID != "tab-42" {
		t.Fatalf("want stored tab id passed through, got %q", fakeTab.gotTabID)
	}
	s, _ := a.Store.Get("demo:feat")
	if s.TermTabID != "tab-42" {
		t.Fatalf("want tab id unchanged in store, got %q", s.TermTabID)
	}
}

func TestOpenSessionStoresNewItermTabWhenOldOneGone(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	fakeTab := &fakeTabTerminal{newTabID: "tab-99"}
	a.Terminal = fakeTab
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat",
		WorktreePath: "/wt/feat", TermTabID: "tab-42",
	})
	tm.out["list-panes -t =moomux-feat: -F #{pane_current_path}"] = "/wt/feat\n"

	if _, err := a.OpenSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	s, _ := a.Store.Get("demo:feat")
	if s.TermTabID != "tab-99" {
		t.Fatalf("want new tab id persisted, got %q", s.TermTabID)
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
	// This session's agent defaults to claude, so OpenSession's needs-input
	// hook repair (see repairNeedsInputHooks) will run and write to the
	// sandboxed HOME's ~/.claude/settings.json (see newTestApp) — harmless
	// here since this test only cares about tmux recreation.
	wt := filepath.Join(t.TempDir(), "feat")
	_ = a.Store.Put(session.Session{ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat", WorktreePath: wt})
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
	if !tm.called("new-session -d -s " + tn + " -c " + wt) {
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

func TestOpenSessionDeadRecreatesWithDangerousFlag(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	tn := TmuxSessionName("demo:c", "c")
	_ = a.Store.Put(session.Session{
		ID: "demo:c", Project: "demo", Name: "c", TmuxSession: tn,
		WorktreePath: "/wt/c", Agent: "codex", Dangerous: true,
	})
	tm.failOn["has-session -t ="+tn] = true
	tm.out["list-panes -t ="+tn+": -F #{pane_id}"] = "%0\n"

	if _, err := a.OpenSession("demo:c"); err != nil {
		t.Fatal(err)
	}
	if !tm.called("send-keys -t %0 codex --yolo Enter") {
		t.Fatalf("expected dangerous-flag relaunch; calls = %v", tm.calls)
	}
}

func TestOpenSessionRepairsMissingClaudeHooks(t *testing.T) {
	// Claude hooks install globally (see claudehook.EnsureHooksInstalled's
	// doc comment), not per-worktree — newTestApp already sandboxes HOME so
	// this doesn't touch the real developer's ~/.claude/settings.json.
	a, _, tm, term := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	term.hint = "run: tmux attach -t moomux-feat"
	wt := filepath.Join(t.TempDir(), "feat")
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat",
		WorktreePath: wt, Agent: "claude",
	})
	tm.out["list-panes -t =moomux-feat: -F #{pane_current_path}"] = wt + "\n"

	// Session predates the needs-input feature: no ~/.claude/settings.json yet.
	if _, err := a.OpenSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("expected OpenSession to backfill ~/.claude/settings.json: %v", err)
	}
	if !strings.Contains(string(data), "moomux hook claude set") {
		t.Fatalf("settings.json missing moomux hooks: %s", data)
	}
}

func TestOpenSessionRepairsMissingCodexHooks(t *testing.T) {
	// Codex hooks install globally (see codexhook.EnsureHooks's doc comment),
	// not per-worktree — newTestApp already sandboxes HOME so this doesn't
	// touch the real developer's ~/.codex/hooks.json.
	a, _, tm, term := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	term.hint = "run: tmux attach -t moomux-feat"
	wt := filepath.Join(t.TempDir(), "feat")
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat",
		WorktreePath: wt, Agent: "codex",
	})
	tm.out["list-panes -t =moomux-feat: -F #{pane_current_path}"] = wt + "\n"

	// Predates the needs-input feature: no ~/.codex/hooks.json yet.
	if _, err := a.OpenSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("expected OpenSession to backfill ~/.codex/hooks.json: %v", err)
	}
	if !strings.Contains(string(data), "moomux hook codex set") {
		t.Fatalf("hooks.json missing moomux hooks: %s", data)
	}
}

func TestOpenSessionRepairsMissingKillCommand(t *testing.T) {
	a, _, tm, term := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	term.hint = "run: tmux attach -t moomux-feat"
	wt := filepath.Join(t.TempDir(), "feat")
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat",
		WorktreePath: wt, Agent: "claude",
	})
	tm.out["list-panes -t =moomux-feat: -F #{pane_current_path}"] = wt + "\n"

	// Session predates the /kill feature: no ~/.claude/commands/kill.md yet.
	if _, err := a.OpenSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "commands", "kill.md")); err != nil {
		t.Fatalf("expected OpenSession to backfill ~/.claude/commands/kill.md: %v", err)
	}
}

func TestOpenSessionRepairsMissingKillPrompt(t *testing.T) {
	a, _, tm, term := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	term.hint = "run: tmux attach -t moomux-feat"
	wt := filepath.Join(t.TempDir(), "feat")
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", TmuxSession: "moomux-feat",
		WorktreePath: wt, Agent: "codex",
	})
	tm.out["list-panes -t =moomux-feat: -F #{pane_current_path}"] = wt + "\n"

	// Session predates the /kill feature: no ~/.codex/prompts/kill.md yet.
	if _, err := a.OpenSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "prompts", "kill.md")); err != nil {
		t.Fatalf("expected OpenSession to backfill ~/.codex/prompts/kill.md: %v", err)
	}
}

func TestOpenSessionSkipsHookRepairForOtherAgents(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	home, _ := os.UserHomeDir()
	wt := filepath.Join(t.TempDir(), "oc")
	_ = a.Store.Put(session.Session{
		ID: "demo:oc", Project: "demo", Name: "oc", TmuxSession: "moomux-oc",
		WorktreePath: wt, Agent: "opencode",
	})
	tm.out["list-panes -t =moomux-oc: -F #{pane_current_path}"] = wt + "\n"

	if _, err := a.OpenSession("demo:oc"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no ~/.claude/settings.json for a non-claude agent, stat err = %v", err)
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

func TestKillTmuxClosesTerminalTab(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	fakeTerm := &fakeCloseTabTerminal{}
	a.Terminal = fakeTerm
	_ = a.Store.Put(session.Session{
		ID: "demo:a", Project: "demo", Name: "a", TmuxSession: "moomux-a", TermTabID: "tab-7",
	})

	if err := a.KillTmux("demo:a"); err != nil {
		t.Fatal(err)
	}
	if len(fakeTerm.closed) != 1 || fakeTerm.closed[0] != "tab-7" {
		t.Fatalf("want tab-7 closed, got %v", fakeTerm.closed)
	}
	s, _ := a.Store.Get("demo:a")
	if s.TermTabID != "" {
		t.Fatalf("want tab id cleared after close, got %q", s.TermTabID)
	}
	if !tm.called("kill-session -t =moomux-a") {
		t.Fatalf("calls = %v", tm.calls)
	}
}

func TestKillTmuxSkipsTabCloseWhenNoTabRecorded(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	fakeTerm := &fakeCloseTabTerminal{}
	a.Terminal = fakeTerm
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", TmuxSession: "moomux-a"})

	if err := a.KillTmux("demo:a"); err != nil {
		t.Fatal(err)
	}
	if len(fakeTerm.closed) != 0 {
		t.Fatalf("want no close attempt without a recorded tab, got %v", fakeTerm.closed)
	}
}

func TestReorderSessionsPersistsGivenOrder(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", Order: 1})
	_ = a.Store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", Order: 2})

	if err := a.ReorderSessions([]string{"demo:b", "demo:a"}); err != nil {
		t.Fatal(err)
	}
	if got := a.Store.ByProject("demo"); got[0].ID != "demo:b" || got[1].ID != "demo:a" {
		t.Fatalf("order = %v, %v", got[0].ID, got[1].ID)
	}
}

// TestReorderSessionsSkipsUnknownIDs guards against an unknown id (e.g. a
// session deleted by another moomux process between the TUI reading its
// displayed order and this call landing) aborting the whole reorder — it
// should simply be dropped from the persisted order rather than erroring.
func TestReorderSessionsSkipsUnknownIDs(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", Order: 1})
	_ = a.Store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", Order: 2})

	if err := a.ReorderSessions([]string{"demo:b", "demo:nope", "demo:a"}); err != nil {
		t.Fatal(err)
	}
	if got := a.Store.ByProject("demo"); got[0].ID != "demo:b" || got[1].ID != "demo:a" {
		t.Fatalf("order = %v, %v", got[0].ID, got[1].ID)
	}
}

// TestReorderSessionsUsesCallerOrderNotStoreOrder is a regression test for
// the bug where MoveSession (ReorderSessions' predecessor) recomputed peers
// via Store.ByProject (sorted by Order) instead of using the caller-supplied
// order. That diverges from Store order whenever the TUI's displayed list
// does — e.g. a session floats to the top of the list for having a live
// tmux window, state tracked entirely client-side and invisible to the
// store — so reordering against ByProject's own recomputed peers either
// silently no-ops or reorders a pair the user never saw adjacent on screen.
// Here the caller's order is the reverse of the sessions' stored Order,
// simulating exactly that divergence, and the persisted order must follow
// it, not Order.
func TestReorderSessionsUsesCallerOrderNotStoreOrder(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", Order: 1})
	_ = a.Store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", Order: 2})
	_ = a.Store.Put(session.Session{ID: "demo:c", Project: "demo", Name: "c", Order: 3})

	// Store order is a, b, c — but the caller (simulating "b" floated to the
	// top for being tmux-alive, then "a" moved up past it) displays a, b, c.
	if err := a.ReorderSessions([]string{"demo:a", "demo:b", "demo:c"}); err != nil {
		t.Fatal(err)
	}
	got := a.Store.ByProject("demo")
	want := []string{"demo:a", "demo:b", "demo:c"}
	for i, w := range want {
		if got[i].ID != w {
			t.Fatalf("order[%d] = %s, want %s (full: %v)", i, got[i].ID, w, got)
		}
	}
}

// TestReorderSessionsLeavesArchivedSessionsUntouched is a regression test
// for the bug where MoveSession's ByProject-derived peers included archived
// sessions (ByProject never filters them) while the TUI's displayed order
// never does — a mismatch that could shift an archived session's Order as a
// side effect of reordering the active list. Passing only the active ids
// (as the TUI does) must never touch the archived one.
func TestReorderSessionsLeavesArchivedSessionsUntouched(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", Order: 1})
	_ = a.Store.Put(session.Session{ID: "demo:archived", Project: "demo", Name: "old", Order: 2, Archived: true})
	_ = a.Store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", Order: 3})

	if err := a.ReorderSessions([]string{"demo:b", "demo:a"}); err != nil {
		t.Fatal(err)
	}
	archived, ok := a.Store.Get("demo:archived")
	if !ok || archived.Order != 2 {
		t.Fatalf("archived session's Order changed: %+v", archived)
	}
}

// TestCreateFolderLandsAfterEverythingElse guards the Folders overlay's "n"
// (new) action: a brand new folder must get an Order past every existing
// top-level session and folder in the project, so it doesn't misleadingly
// sort to the very top (Order's zero value sorts first).
func TestCreateFolderLandsAfterEverythingElse(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", Order: 1})
	_ = a.Store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", Order: 5})
	p := a.Cfg.Projects["demo"]
	p.Folders = map[string]config.FolderMeta{"existing": {Order: 3}}
	a.Cfg.Projects["demo"] = p
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		t.Fatal(err)
	}

	if err := a.CreateFolder("demo", "fresh"); err != nil {
		t.Fatal(err)
	}
	if err := config.Reload(a.CfgPath, a.Cfg); err != nil {
		t.Fatal(err)
	}
	meta, ok := a.Cfg.Projects["demo"].Folders["fresh"]
	if !ok {
		t.Fatal("expected folder \"fresh\" to be created")
	}
	if meta.Order <= 5 {
		t.Fatalf("new folder's Order = %d, want > 5 (past session b and folder existing)", meta.Order)
	}
	if meta.Collapsed {
		t.Fatal("expected a newly created folder to start expanded")
	}
}

func TestCreateFolderRejectsDuplicateName(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	if err := a.CreateFolder("demo", "dup"); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateFolder("demo", "dup"); err == nil {
		t.Fatal("expected an error creating a folder that already exists")
	}
}

func TestSetSessionFolderCreatesFolderOnFirstUse(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", Order: 3})

	got, err := a.SetSessionFolder("demo:a", "auth")
	if err != nil {
		t.Fatal(err)
	}
	if got.Folder != "auth" {
		t.Fatalf("session.Folder = %q, want auth", got.Folder)
	}
	meta, ok := a.Cfg.Projects["demo"].Folders["auth"]
	if !ok {
		t.Fatal("expected folder \"auth\" to be created")
	}
	if meta.Order != 3 {
		t.Fatalf("new folder's Order = %d, want 3 (anchored on the session it was created for)", meta.Order)
	}

	// Filing a second session into the same (now-existing) folder must not
	// touch the folder's Order again.
	_ = a.Store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", Order: 9})
	if _, err := a.SetSessionFolder("demo:b", "auth"); err != nil {
		t.Fatal(err)
	}
	if got := a.Cfg.Projects["demo"].Folders["auth"].Order; got != 3 {
		t.Fatalf("existing folder's Order changed to %d, want unchanged 3", got)
	}
}

func TestSetSessionFolderEmptyRemovesFromFolder(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", Folder: "auth"})

	got, err := a.SetSessionFolder("demo:a", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Folder != "" {
		t.Fatalf("session.Folder = %q, want empty (top-level)", got.Folder)
	}
}

func TestRenameFolderUpdatesMembersAndRejectsCollision(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", Folder: "old"})
	_ = a.Store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", Folder: "other"})
	p := a.Cfg.Projects["demo"]
	p.Folders = map[string]config.FolderMeta{"old": {Order: 1}, "other": {Order: 2}}
	a.Cfg.Projects["demo"] = p
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		t.Fatal(err)
	}

	if err := a.RenameFolder("demo", "old", "new"); err != nil {
		t.Fatal(err)
	}
	if _, exists := a.Cfg.Projects["demo"].Folders["old"]; exists {
		t.Fatal("old folder name should no longer exist")
	}
	if _, exists := a.Cfg.Projects["demo"].Folders["new"]; !exists {
		t.Fatal("expected folder \"new\" to exist")
	}
	member, ok := a.Store.Get("demo:a")
	if !ok || member.Folder != "new" {
		t.Fatalf("member session.Folder = %+v, want \"new\"", member)
	}
	other, ok := a.Store.Get("demo:b")
	if !ok || other.Folder != "other" {
		t.Fatalf("unrelated session's folder changed: %+v", other)
	}

	if err := a.RenameFolder("demo", "new", "other"); err == nil {
		t.Fatal("expected an error renaming onto an existing folder name")
	}
}

func TestSetFolderCollapsedPersists(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	p := a.Cfg.Projects["demo"]
	p.Folders = map[string]config.FolderMeta{"auth": {}}
	a.Cfg.Projects["demo"] = p
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		t.Fatal(err)
	}

	if err := a.SetFolderCollapsed("demo", "auth", true); err != nil {
		t.Fatal(err)
	}
	if !a.Cfg.Projects["demo"].Folders["auth"].Collapsed {
		t.Fatal("expected auth folder to be collapsed")
	}
	if err := config.Reload(a.CfgPath, a.Cfg); err != nil {
		t.Fatal(err)
	}
	if !a.Cfg.Projects["demo"].Folders["auth"].Collapsed {
		t.Fatal("collapsed state did not persist to disk")
	}
}

func TestDeleteFolderUnparentsMembers(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", Folder: "auth"})
	_ = a.Store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", Folder: "other"})
	p := a.Cfg.Projects["demo"]
	p.Folders = map[string]config.FolderMeta{"auth": {}, "other": {}}
	a.Cfg.Projects["demo"] = p
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		t.Fatal(err)
	}

	if err := a.DeleteFolder("demo", "auth"); err != nil {
		t.Fatal(err)
	}
	if _, exists := a.Cfg.Projects["demo"].Folders["auth"]; exists {
		t.Fatal("expected auth folder to be deleted")
	}
	member, ok := a.Store.Get("demo:a")
	if !ok || member.Folder != "" {
		t.Fatalf("member session should be un-parented to top-level, got %+v", member)
	}
	other, ok := a.Store.Get("demo:b")
	if !ok || other.Folder != "other" {
		t.Fatalf("unrelated session's folder changed: %+v", other)
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

func TestSessionForPath(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	wt := filepath.Join(a.WorktreeRoot, "a")
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", WorktreePath: wt})

	if s, ok := a.SessionForPath(wt); !ok || s.ID != "demo:a" {
		t.Fatalf("exact match: s=%+v ok=%v", s, ok)
	}
	if s, ok := a.SessionForPath(filepath.Join(wt, "sub", "dir")); !ok || s.ID != "demo:a" {
		t.Fatalf("subdirectory match: s=%+v ok=%v", s, ok)
	}
	if _, ok := a.SessionForPath(filepath.Join(a.WorktreeRoot, "b")); ok {
		t.Fatal("sibling worktree must not match")
	}
	if _, ok := a.SessionForPath(filepath.Dir(a.WorktreeRoot)); ok {
		t.Fatal("worktree's own parent must not match")
	}
}

func TestSessionForTmuxName(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	_ = a.Store.Put(session.Session{ID: "demo:a", Project: "demo", Name: "a", TmuxSession: "moomux-a-1234"})
	_ = a.Store.Put(session.Session{ID: "demo:b", Project: "demo", Name: "b", TmuxSession: "moomux-b-5678"})

	if s, ok := a.SessionForTmuxName("moomux-b-5678"); !ok || s.ID != "demo:b" {
		t.Fatalf("s=%+v ok=%v", s, ok)
	}
	if _, ok := a.SessionForTmuxName("moomux-nonexistent"); ok {
		t.Fatal("unknown tmux session must not match")
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

func TestSetAutoSubmitDefault(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{
		"demo": {Kind: "git", Repo: t.TempDir(), Agent: "claude"},
	})

	if err := a.SetAutoSubmitDefault(true); err != nil {
		t.Fatal(err)
	}
	if !a.Cfg.AutoSubmitDefault {
		t.Fatal("AutoSubmitDefault not set in memory")
	}

	loaded, err := config.Load(a.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.AutoSubmitDefault {
		t.Fatal("AutoSubmitDefault not persisted")
	}
}

func TestSetSortRecentFirst(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{
		"demo": {Kind: "git", Repo: t.TempDir(), Agent: "claude"},
	})

	if err := a.SetSortRecentFirst(true); err != nil {
		t.Fatal(err)
	}
	if !a.Cfg.SortRecentFirst {
		t.Fatal("SortRecentFirst not set in memory")
	}

	loaded, err := config.Load(a.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.SortRecentFirst {
		t.Fatal("SortRecentFirst not persisted")
	}
}

func TestSetAutoTmux(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{
		"demo": {Kind: "git", Repo: t.TempDir(), Agent: "claude"},
	})

	if err := a.SetAutoTmux(true); err != nil {
		t.Fatal(err)
	}
	if !a.Cfg.AutoTmux {
		t.Fatal("AutoTmux not set in memory")
	}

	loaded, err := config.Load(a.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.AutoTmux {
		t.Fatal("AutoTmux not persisted")
	}
}

func TestSessionsSortsByLastOpenedWhenSortRecentFirstIsOn(t *testing.T) {
	a, _, _, _ := newTestApp(t, map[string]config.Project{
		"demo": {Kind: "git", Repo: t.TempDir(), Agent: "claude"},
	})
	t0 := time.Now()
	_ = a.Store.Put(session.Session{ID: "opened-earlier", Project: "demo", CreatedAt: t0.Add(-time.Hour), LastOpened: t0.Add(-time.Minute)})
	_ = a.Store.Put(session.Session{ID: "opened-later", Project: "demo", CreatedAt: t0.Add(-2 * time.Hour), LastOpened: t0})

	// Default (manual order unset on both): falls back to CreatedAt desc.
	if got := a.Sessions(); got[0].ID != "opened-earlier" {
		t.Fatalf("expected manual-order default to sort by CreatedAt desc, got %s first", got[0].ID)
	}

	if err := a.SetSortRecentFirst(true); err != nil {
		t.Fatal(err)
	}
	if got := a.Sessions(); got[0].ID != "opened-later" {
		t.Fatalf("expected recent-first sort to put most-recently-opened first, got %s first", got[0].ID)
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
	if got := a.Cfg.Projects["demo"]; !reflect.DeepEqual(got, original) {
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

// TestSessionsPicksUpExternallySpawnedSession covers `moomux spawn`: a
// second process shares the same sessions.json and writes a new session via
// its own *Store while this App's Store is already loaded in memory.
// Sessions() must see it without requiring a mutating call (archive,
// delete, reorder) on this App's Store first.
func TestSessionsPicksUpExternallySpawnedSession(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))

	other := &session.Store{Path: a.Store.Path}
	if err := other.Load(); err != nil {
		t.Fatal(err)
	}
	spawned := session.Session{ID: "demo:spawned", Project: "demo", Name: "spawned"}
	if err := other.Put(spawned); err != nil {
		t.Fatal(err)
	}

	got := a.Sessions()
	found := false
	for _, s := range got {
		if s.ID == spawned.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Sessions() = %v, want it to include externally spawned session %q", got, spawned.ID)
	}
}

func TestSetSessionStatusTitle(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	s := session.Session{ID: "demo:a", Project: "demo", Name: "a", TmuxSession: "moomux-a"}
	if err := a.Store.Put(s); err != nil {
		t.Fatal(err)
	}
	tm.calls = nil

	if err := a.SetSessionStatusTitle(s.ID, watcher.Working); err != nil {
		t.Fatal(err)
	}
	wantCalls := [][]string{
		{"display-message", "-p", "-t", "=moomux-a:", "#{window_name}"},
		{"rename-window", "-t", "=moomux-a:", "● a"},
	}
	if !reflect.DeepEqual(tm.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", tm.calls, wantCalls)
	}

	if err := a.SetSessionStatusTitle("demo:missing", watcher.Working); err != nil {
		t.Fatalf("unknown session should be a no-op, got err %v", err)
	}
}

// TestSetSessionStatusTitlePreservesUserRename verifies a status update only
// swaps the leading glyph and leaves a name the user typed directly via
// tmux's own rename-window (Ctrl-B ,) untouched, instead of clobbering it
// with the session's stored name.
func TestSetSessionStatusTitlePreservesUserRename(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	s := session.Session{ID: "demo:a", Project: "demo", Name: "a", TmuxSession: "moomux-a"}
	if err := a.Store.Put(s); err != nil {
		t.Fatal(err)
	}
	tm.out = map[string]string{
		"display-message -p -t =moomux-a: #{window_name}": "● my custom name",
	}
	tm.calls = nil

	if err := a.SetSessionStatusTitle(s.ID, watcher.NeedsInput); err != nil {
		t.Fatal(err)
	}
	want := []string{"rename-window", "-t", "=moomux-a:", "⚠ my custom name"}
	if len(tm.calls) != 2 || !reflect.DeepEqual(tm.calls[1], want) {
		t.Fatalf("calls = %v, want rename call %v", tm.calls, want)
	}
}

// TestRenameSession verifies a rename updates the display name, the live
// tmux session, and the window title, while leaving ID/worktree/branch
// alone — the failure mode without the fix is the tmux session and its
// window title going stale, out of sync with the new stored name.
func TestRenameSession(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	s := session.Session{
		ID: "demo:a", Project: "demo", Name: "a",
		TmuxSession: "moomux-a", WorktreePath: "/wt/a", Branch: "a",
	}
	if err := a.Store.Put(s); err != nil {
		t.Fatal(err)
	}
	tm.out["display-message -p -t =moomux-a: #{window_name}"] = "a"
	tm.calls = nil

	got, err := a.RenameSession(s.ID, "b")
	if err != nil {
		t.Fatal(err)
	}
	wantTmux := TmuxSessionName(s.ID, "b")
	if got.Name != "b" || got.ID != "demo:a" || got.TmuxSession != wantTmux ||
		got.WorktreePath != "/wt/a" || got.Branch != "a" {
		t.Fatalf("session = %+v", got)
	}
	wantCalls := [][]string{
		{"has-session", "-t", "=moomux-a"},
		{"display-message", "-p", "-t", "=moomux-a:", "#{window_name}"},
		{"rename-window", "-t", "=moomux-a:", "b"},
		{"rename-session", "-t", "=moomux-a", wantTmux},
	}
	if !reflect.DeepEqual(tm.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", tm.calls, wantCalls)
	}

	stored, ok := a.Store.Get(s.ID)
	if !ok || stored.Name != "b" || stored.TmuxSession != wantTmux {
		t.Fatalf("stored session = %+v, ok=%v", stored, ok)
	}
}

// TestRenameSessionPreservesUserWindowRename mirrors
// TestSetSessionStatusTitlePreservesUserRename: a window name the user set
// directly (not the plain session name) must not be clobbered by a rename.
func TestRenameSessionPreservesUserWindowRename(t *testing.T) {
	a, _, tm, _ := newTestApp(t, gitProject("/repo"))
	s := session.Session{ID: "demo:a", Project: "demo", Name: "a", TmuxSession: "moomux-a"}
	if err := a.Store.Put(s); err != nil {
		t.Fatal(err)
	}
	tm.out["display-message -p -t =moomux-a: #{window_name}"] = "my custom name"
	tm.calls = nil

	if _, err := a.RenameSession(s.ID, "b"); err != nil {
		t.Fatal(err)
	}
	for _, c := range tm.calls {
		if len(c) > 0 && c[0] == "rename-window" {
			t.Fatalf("must not overwrite a manually renamed window, got %v", c)
		}
	}
}

func TestRenameSessionRejectsCollision(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	for _, name := range []string{"a", "b"} {
		if err := a.Store.Put(session.Session{ID: "demo:" + name, Project: "demo", Name: name}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := a.RenameSession("demo:a", "b"); err == nil {
		t.Fatal("renaming onto an existing session's name must fail")
	}
}

func TestRenameSessionUnknownID(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	if _, err := a.RenameSession("demo:missing", "b"); err == nil {
		t.Fatal("unknown session must fail")
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

	got, err := a.SetSessionAgent(original.ID, "codex", false)
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

	if _, err := a.SetSessionAgent("demo:missing", "codex", false); err == nil {
		t.Fatal("unknown session must fail")
	}
	if _, err := a.SetSessionAgent(original.ID, "other", false); err == nil {
		t.Fatal("unknown agent must fail")
	}

	a.Store.Path = t.TempDir()
	if _, err := a.SetSessionAgent(original.ID, "codex", false); err == nil {
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

	if _, err := a.DeleteSession("demo:feat"); err != nil {
		t.Fatal(err)
	}
	if !tm.called("kill-session -t =moomux-feat") {
		t.Fatalf("tmux calls = %v", tm.calls)
	}
	var sawRemove, sawBranchDelete bool
	for _, c := range git.calls {
		joined := strings.Join(c, " ")
		if joined == "@/repo worktree remove "+wt+" --force --force" {
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

	if _, err := a.DeleteSession("demo:feat"); err != nil {
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
	if _, _, err := a.CreateSession("demo", "feat", "", "", "", true, false); err != nil {
		t.Fatal(err)
	}
	_, _, err := a.CreateSession("demo", "feat", "", "", "", true, false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
}

// TestCreateSessionTrustsClaudeWorktree guards against the "Do you trust the
// files in this folder?" dialog Claude Code shows on first launch in a
// directory it hasn't seen — every moomux session runs in a brand-new
// worktree, so without pre-trusting it, that dialog eats the agent's first
// real input (including StartFirstPrompt's typed prompt) with nobody there
// to click through it.
func TestCreateSessionTrustsClaudeWorktree(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	tm.out["list-panes -t =moomux-feat: -F #{pane_id}"] = "%0\n"
	noBranch(git, "feat")

	s, _, err := a.CreateSession("demo", "feat", "claude", "", "", true, false)
	if err != nil {
		t.Fatal(err)
	}

	home := os.Getenv("HOME")
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read .claude.json: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	// Claude Code's own trust checks read root.projects[dir], never root[dir]
	// directly — a top-level entry looks plausible but is silently ignored.
	projects, ok := root["projects"].(map[string]any)
	if !ok {
		t.Fatalf("no projects object: %v", root)
	}
	entry, ok := projects[s.WorktreePath].(map[string]any)
	if !ok {
		t.Fatalf("no projects entry for worktree %q: %v", s.WorktreePath, projects)
	}
	if entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("hasTrustDialogAccepted = %v, want true", entry["hasTrustDialogAccepted"])
	}
}

// TestCreateSessionDoesNotTrustNonClaudeAgent guards the flip side: writing
// into ~/.claude.json for a codex/opencode session would be a no-op for that
// agent but still an unnecessary write nobody asked for.
func TestCreateSessionDoesNotTrustNonClaudeAgent(t *testing.T) {
	a, git, tm, _ := newTestApp(t, gitProject("/repo"))
	tm.out["list-panes -t =moomux-feat: -F #{pane_id}"] = "%0\n"
	noBranch(git, "feat")

	if _, _, err := a.CreateSession("demo", "feat", "codex", "", "", true, false); err != nil {
		t.Fatal(err)
	}

	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf(".claude.json should not exist for a non-claude session, stat err = %v", err)
	}
}

// A worktree-delete userscript's stdout should reach the caller as a hint,
// the same way a worktree-create script's output does for CreateSession.
func TestDeleteSessionSurfacesUserscriptOutput(t *testing.T) {
	a, _, _, _ := newTestApp(t, gitProject("/repo"))
	wt := filepath.Join(a.WorktreeRoot, "demo", "feat")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Put(session.Session{
		ID: "demo:feat", Project: "demo", Name: "feat", Branch: "feat", NewBranch: true,
		TmuxSession: "moomux-feat", WorktreePath: wt,
	})

	home := os.Getenv("HOME")
	scriptDir := filepath.Join(home, ".config", "moomux", "userscripts", "worktree-delete")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(scriptDir, "10-hello.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho bye from script\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	hint, err := a.DeleteSession("demo:feat")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hint, "bye from script") {
		t.Fatalf("hint = %q, want it to contain script output", hint)
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

	if _, err := a.DeleteSession("demo:feat"); err != nil {
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

	if _, err := a.DeleteSession("gone:x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir not removed: %v", err)
	}
	if len(git.calls) != 0 {
		t.Fatalf("git calls = %v", git.calls)
	}
	if _, err := a.DeleteSession("gone:nope"); err == nil {
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

	if _, err := a.DeleteSession("gone:x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("user's project folder was deleted: %v", err)
	}
	if _, ok := a.Store.Get("gone:x"); ok {
		t.Fatal("store entry should still be deleted")
	}
}

func TestStartFirstPromptWaitsForPaneThenSendsLiteralTextThenSeparateEnter(t *testing.T) {
	a, _, tm, _ := newTestApp(t, map[string]config.Project{})
	// A transition from the pre-launch shell to the agent's idle screen —
	// see waitForPaneReady's doc comment for why a constant value here
	// wouldn't actually exercise the readiness wait (it would either look
	// "stable" from the very first poll, or, with the fix in place, run out
	// the full paneChangeTimeout waiting for a change that never comes).
	tm.seq = map[string][]string{
		"capture-pane -p -t =demo:x:": {"$ claude", "agent-idle", "agent-idle"},
	}

	if err := a.StartFirstPrompt("demo:x", "do the thing", true); err != nil {
		t.Fatal(err)
	}

	if !tm.called("capture-pane -p -t =demo:x:") {
		t.Fatalf("did not poll pane readiness before sending: %v", tm.calls)
	}
	// Text and Enter must be two separate send-keys calls — bundling them
	// into one is what a terminal-raw-mode TUI's paste detection swallows
	// (see Client.SendLiteral's doc comment).
	if tm.called("send-keys -t =demo:x: do the thing Enter") {
		t.Fatalf("text and Enter must not be sent in the same call: %v", tm.calls)
	}
	if !tm.called("send-keys -t =demo:x: -l -- do the thing") {
		t.Fatalf("did not type the prompt as a literal, Enter-less call: %v", tm.calls)
	}
	if !tm.called("send-keys -t =demo:x: Enter") {
		t.Fatalf("did not send a separate Enter to actually start the work: %v", tm.calls)
	}
}

// TestStartFirstPromptWaitsForActualPaneChangeBeforeStabilizing guards the
// exact bug reported in practice: a pane still showing the idle shell prompt
// right after the launch command was typed looks just as "stable" (same
// content on two consecutive polls) as an idle agent input box does, so a
// naive stability check fires immediately and types the prompt onto the
// shell before the agent has even opened. waitForPaneReady must wait for the
// pane to visibly change at least once before it starts checking for
// stability.
func TestStartFirstPromptWaitsForActualPaneChangeBeforeStabilizing(t *testing.T) {
	a, _, tm, _ := newTestApp(t, map[string]config.Project{})
	tm.seq = map[string][]string{
		"capture-pane -p -t =demo:x:": {
			"$ claude", "$ claude", "$ claude", // idle shell, right after launch was typed
			"claude ready>", "claude ready>", "claude ready>", // agent took over and is idle
		},
	}

	if err := a.StartFirstPrompt("demo:x", "do the thing", true); err != nil {
		t.Fatal(err)
	}

	sendIdx := -1
	captureBeforeSend := 0
	for i, c := range tm.calls {
		joined := strings.Join(c, " ")
		if sendIdx == -1 && strings.HasPrefix(joined, "capture-pane") {
			captureBeforeSend++
		}
		if strings.HasPrefix(joined, "send-keys -t =demo:x: -l --") {
			sendIdx = i
			break
		}
	}
	if sendIdx == -1 {
		t.Fatalf("never sent the prompt: %v", tm.calls)
	}
	// A naive "stable after 2 identical polls" check fires after just 2-3
	// captures of the still-idle shell, before the agent ever rendered.
	// Requiring more captures than that proves the pane's actual change was
	// waited for first.
	if captureBeforeSend < 4 {
		t.Fatalf("sent after only %d capture-pane polls — looks like it fired on the pre-launch shell, not the agent: %v", captureBeforeSend, tm.calls)
	}
}

// TestStartFirstPromptWaitsForPaneToSettleAfterTypingBeforePressingEnter
// guards the same class of race waitForPaneReady already guards against
// before typing, but on the other side of SendLiteral: a CLI that's still
// re-rendering the just-typed text (e.g. collapsing a multi-line paste, or
// just a slower machine) can still be changing when a fixed, unconditional
// delay elapses, and pressing Enter into that mid-render state is exactly
// what swallows it instead of submitting. Enter must wait for the pane to
// actually stop changing after the prompt was typed, the same way typing
// waits for the pane to start changing after the agent was launched.
func TestStartFirstPromptWaitsForPaneToSettleAfterTypingBeforePressingEnter(t *testing.T) {
	a, _, tm, _ := newTestApp(t, map[string]config.Project{})
	tm.seq = map[string][]string{
		"capture-pane -p -t =demo:x:": {
			"$ claude", "agent-idle", "agent-idle", // pre-type: change then stable
			"agent-idle: settling", "agent-idle: settled", "agent-idle: settled", // post-type: still re-rendering, then stable
		},
	}

	if err := a.StartFirstPrompt("demo:x", "do the thing", true); err != nil {
		t.Fatal(err)
	}

	sendIdx, enterIdx, capturesAfterSend := -1, -1, 0
	for i, c := range tm.calls {
		joined := strings.Join(c, " ")
		switch {
		case strings.HasPrefix(joined, "send-keys -t =demo:x: -l --"):
			sendIdx = i
		case joined == "send-keys -t =demo:x: Enter":
			enterIdx = i
		case sendIdx != -1 && enterIdx == -1 && strings.HasPrefix(joined, "capture-pane"):
			capturesAfterSend++
		}
	}
	if sendIdx == -1 {
		t.Fatalf("never typed the prompt: %v", tm.calls)
	}
	if enterIdx == -1 {
		t.Fatalf("never pressed Enter: %v", tm.calls)
	}
	if capturesAfterSend < 2 {
		t.Fatalf("pressed Enter after only %d capture-pane polls following the typed text — looks like a blind sleep rather than waiting for the pane to actually settle: %v", capturesAfterSend, tm.calls)
	}
}

// TestStartFirstPromptRetriesEnterWhenPromptStillShowing guards the bug
// reported in practice on Claude specifically: waitForPaneReady's "stable for
// two polls" check can latch onto an early plateau in a multi-phase startup
// (splash screen frozen momentarily before the real input box renders)
// rather than the agent's actual final idle state. When that happens, the
// first Enter press can land in the narrow window where the agent's paste
// heuristic swallows it, leaving the typed prompt still sitting in the pane
// un-submitted. StartFirstPrompt must notice that and press Enter again
// rather than silently leaving the prompt untouched.
func TestStartFirstPromptRetriesEnterWhenPromptStillShowing(t *testing.T) {
	a, _, tm, _ := newTestApp(t, map[string]config.Project{})
	tm.seq = map[string][]string{
		"capture-pane -p -t =demo:x:": {
			"$ claude", "agent-idle", "agent-idle", // pre-type: change then stable
			"agent-idle: do the thing", "agent-idle: do the thing", "agent-idle: do the thing", // consumed by waitForPaneReady's own stability check
			"agent-idle: do the thing", "agent-idle: do the thing", "agent-idle: do the thing", // post-type: settled
			// first Enter: swallowed — prompt is still sitting there for every
			// confirm poll, exhausting the attempt.
			"agent-idle: do the thing", "agent-idle: do the thing", "agent-idle: do the thing",
			"agent-idle: do the thing", "agent-idle: do the thing",
			// second Enter: actually submits.
			"agent-idle",
		},
	}

	if err := a.StartFirstPrompt("demo:x", "do the thing", true); err != nil {
		t.Fatal(err)
	}

	enterCount := 0
	for _, c := range tm.calls {
		if strings.Join(c, " ") == "send-keys -t =demo:x: Enter" {
			enterCount++
		}
	}
	if enterCount < 2 {
		t.Fatalf("prompt was still showing after the first Enter but no retry was sent: %v", tm.calls)
	}
}

func TestStartFirstPromptNoopOnEmptyPrompt(t *testing.T) {
	a, _, tm, _ := newTestApp(t, map[string]config.Project{})
	if err := a.StartFirstPrompt("demo:x", "", true); err != nil {
		t.Fatal(err)
	}
	if len(tm.calls) != 0 {
		t.Fatalf("empty prompt should be a no-op: %v", tm.calls)
	}
}

// TestStartFirstPromptSkipsEnterWhenAutoSubmitFalse guards the auto-submit
// toggle's off state: the prompt still gets typed into the pane so the user
// can review it, but Enter must never be pressed on their behalf.
func TestStartFirstPromptSkipsEnterWhenAutoSubmitFalse(t *testing.T) {
	a, _, tm, _ := newTestApp(t, map[string]config.Project{})
	tm.seq = map[string][]string{
		"capture-pane -p -t =demo:x:": {"$ claude", "agent-idle", "agent-idle"},
	}

	if err := a.StartFirstPrompt("demo:x", "do the thing", false); err != nil {
		t.Fatal(err)
	}

	if !tm.called("send-keys -t =demo:x: -l -- do the thing") {
		t.Fatalf("did not type the prompt: %v", tm.calls)
	}
	if tm.called("send-keys -t =demo:x: Enter") {
		t.Fatalf("Enter must not be pressed when autoSubmit is false: %v", tm.calls)
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
