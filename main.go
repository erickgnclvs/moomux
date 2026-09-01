package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/erickgnclvs/moomux/internal/app"
	"github.com/erickgnclvs/moomux/internal/codexhook"
	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/ipc"
	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/terminal"
	"github.com/erickgnclvs/moomux/internal/tmux"
	"github.com/erickgnclvs/moomux/internal/tmuxconf"
	"github.com/erickgnclvs/moomux/internal/tui"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// set by goreleaser via ldflags
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func printUsage() {
	fmt.Println(`moomux manages Claude Code / codex / opencode sessions across git worktrees.

Usage:
  moomux            Launch the interactive TUI.
  moomux spawn ...   Create a session non-interactively and hand it a prompt.
                     Run 'moomux spawn -h' for its flags.
  moomux tag ...    Tag the session whose worktree you're currently in with
                     a ticket and/or PR URL, or with no flags print its
                     current ticket/PR. Run 'moomux tag -h' for its flags.
  moomux park       Stop tmux and close the terminal tab for the session
                     whose worktree you're currently in, keeping its
                     worktree/branch (same as pressing 'x' in the TUI).
                     Backs the /kill slash command inside Claude Code.
  moomux reseed     Re-run the worktree-create userscripts for the session
                     whose worktree you're currently in, with MOOMUX_FORCE=1
                     so they redo setup they'd otherwise skip as already
                     done. Backs the /reseed slash command inside Claude Code.
  moomux serve      Run the orchestration core headless on a unix socket,
                     for another front end to drive. 'moomux serve -h'.
  moomux ui ...     Run the TUI against a running 'moomux serve' instead of
                     its own core. Run 'moomux ui -h' for its flags.
  moomux --version  Print the version.
  moomux --help     Show this message.`)
}

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "-h" || os.Args[1] == "help") {
		printUsage()
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("moomux %s (%s) built %s\n", version, commit, date)
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "hook" {
		if err := runHook(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "moomux:", err)
			os.Exit(1)
		}
		return
	}
	if err := checkDeps(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) >= 2 && os.Args[1] == "spawn" {
		if err := runSpawn(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "moomux:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "tag" {
		if err := runTag(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "moomux:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "park" {
		if err := runPark(); err != nil {
			fmt.Fprintln(os.Stderr, "moomux:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "__park-detached" {
		if err := runParkDetached(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "moomux:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "__park-worker" {
		if err := runParkWorker(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "moomux:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "serve" {
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "moomux:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "ui" {
		if err := runRemote(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "moomux:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "reseed" {
		if err := runReseed(); err != nil {
			fmt.Fprintln(os.Stderr, "moomux:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "moomux:", err)
		os.Exit(1)
	}
}

// checkDeps verifies the external binaries moomux shells out to are on
// $PATH. `make install`/`make run` check tmux/git via check-deps, but
// Homebrew and `go install` users bypass the Makefile entirely, so we check
// again here and fail with an actionable message instead of a raw exec error
// the first time a tmux/git call happens.
func checkDeps() error {
	var missing []string
	for _, bin := range []string{"tmux", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"moomux: missing required dependencies: %s\n\nInstall with:\n  macOS:  brew install %s\n  Ubuntu: sudo apt install %s\n  Fedora: sudo dnf install %s",
		strings.Join(missing, ", "), strings.Join(missing, " "), strings.Join(missing, " "), strings.Join(missing, " "),
	)
}

// saveConfig persists cfg, logging (rather than silently swallowing) any
// failure — cfg.TmuxSetupAsked/AutoTmuxAsked being lost here would reopen
// the first-run prompts on every subsequent launch.
func saveConfig(cfgPath string, cfg *config.Config) {
	if err := config.Save(cfgPath, cfg); err != nil {
		slog.Error("config save failed", "path", cfgPath, "err", err)
	}
}

// promptTmuxSetup offers, on first run only, to append moomux's recommended
// settings (mouse support, passthrough, scrollback, 1-indexed panes — see
// README.md) to ~/.tmux.conf. It always marks cfg.TmuxSetupAsked so this
// never runs again regardless of the answer, and saves that immediately so a
// crash or Ctrl-C right after can't reopen the prompt on every launch.
// Skipped entirely on a non-interactive stdin (e.g. CI, piped input), which
// is treated the same as declining.
func promptTmuxSetup(stdin *bufio.Reader, cfg *config.Config, cfgPath string) {
	path := tmuxconf.Path()
	cfg.TmuxSetupAsked = true

	if tmuxconf.AlreadyApplied(path) {
		saveConfig(cfgPath, cfg)
		return
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		saveConfig(cfgPath, cfg)
		return
	}

	fmt.Printf("moomux launches plain tmux sessions, so mouse support, clickable/scrollable\n")
	fmt.Printf("panes, and a larger scrollback come from your own tmux config. Add this to\n%s?\n", path)
	fmt.Print(tmuxconf.Snippet)
	fmt.Print("Add it now? [y/N] ")

	answer := ""
	if line, err := stdin.ReadString('\n'); err == nil {
		answer = strings.ToLower(strings.TrimSpace(line))
	}

	if answer == "y" || answer == "yes" {
		if err := tmuxconf.Apply(path); err != nil {
			fmt.Fprintln(os.Stderr, "moomux: could not update", path+":", err)
		} else {
			fmt.Println("Added — reload an open tmux session with: tmux source-file", path)
		}
	} else {
		fmt.Println("Skipped. Add it later from README.md's \"Recommended tmux config\" section.")
	}
	fmt.Println()

	saveConfig(cfgPath, cfg)
}

// promptAutoTmux offers, on first run only, to always relaunch moomux
// inside a dedicated tmux session ("moomux") from now on. Like
// promptTmuxSetup, it always marks cfg.AutoTmuxAsked and saves immediately
// so the prompt never reappears, and is skipped on a non-interactive stdin.
func promptAutoTmux(stdin *bufio.Reader, cfg *config.Config, cfgPath string) {
	cfg.AutoTmuxAsked = true

	if !isatty.IsTerminal(os.Stdin.Fd()) {
		saveConfig(cfgPath, cfg)
		return
	}

	fmt.Println("moomux can always start inside its own tmux session (attaching to one named")
	fmt.Println("\"moomux\" if it already exists), so it survives closing your terminal.")
	fmt.Print("Always start moomux inside tmux? [y/N] ")

	answer := ""
	if line, err := stdin.ReadString('\n'); err == nil {
		answer = strings.ToLower(strings.TrimSpace(line))
	}

	if answer == "y" || answer == "yes" {
		cfg.AutoTmux = true
		fmt.Println("Enabled. Change any time by editing auto_tmux in", cfgPath)
	} else {
		fmt.Println("Skipped. Enable later by setting auto_tmux = true in", cfgPath)
	}
	fmt.Println()

	saveConfig(cfgPath, cfg)
}

// relaunchInTmux replaces the current process with `tmux new-session -A -s
// moomux <self>`, attaching to an existing "moomux" session or creating one.
// checkDeps has already verified tmux is on $PATH by the time this runs.
func relaunchInTmux() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	return syscall.Exec(tmuxBin, []string{"tmux", "new-session", "-A", "-s", "moomux", self}, os.Environ())
}

// newApp loads config/session state and wires up an App, with logging
// pointed at moomux.log. Shared by the TUI (run) and the non-interactive
// spawn subcommand — neither the tmux-setup/auto-tmux first-run prompts nor
// relaunchInTmux belong in the non-interactive path, so those stay in run().
func newApp() (*app.App, error) {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", cfgPath, err)
	}

	store := &session.Store{Path: session.DefaultPath()}
	if err := store.Load(); err != nil {
		return nil, fmt.Errorf("load sessions: %w", err)
	}

	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".local", "share", "moomux")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "moomux.log")
	if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(lf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	}

	tmuxClient := tmux.New()
	// moomux itself commonly runs inside a long-lived "moomux" tmux session
	// (see relaunchInTmux); without this, that session's copy of
	// MOSHI_CLIENT can get stuck at whatever it was on first attach and
	// never reflect a later, different connection. Best-effort: a failure
	// here shouldn't block startup.
	if err := tmuxClient.EnsureEnvRefresh(); err != nil {
		slog.Warn("tmux EnsureEnvRefresh failed", "err", err)
	}

	a := &app.App{
		Cfg:          cfg,
		CfgPath:      cfgPath,
		Store:        store,
		Tmux:         tmuxClient,
		Terminal:     terminal.Detect(),
		Git:          gitwt.New(),
		PR:           prstatus.New(),
		WorktreeRoot: app.WorktreeRootDefault(),
	}
	a.InstallKnownCommands()
	return a, nil
}

// runSpawn implements `moomux spawn`: create a session (worktree + tmux +
// agent, same as the TUI's "new session" action) and, if -prompt is given,
// type it into the agent's pane as its first task. Fire-and-forget — it
// prints the new tmux session name and exits without waiting on the agent.
func runSpawn(args []string) error {
	fs := flag.NewFlagSet("spawn", flag.ExitOnError)
	project := fs.String("project", "", "project name (required; run -list to see configured projects)")
	name := fs.String("name", "", "session name (derived from -branch if omitted)")
	agent := fs.String("agent", "", "agent override (claude, codex, opencode)")
	model := fs.String("model", "", "model override, passed to the agent as --model (e.g. sonnet, opus, haiku)")
	thinking := fs.String("thinking", "", "thinking/reasoning level: for codex, a real -c model_reasoning_effort value (minimal, low, medium, high, xhigh); for claude/opencode, a phrase prepended to -prompt (e.g. think, think hard, ultrathink) — no effect there without -prompt")
	dangerous := fs.Bool("dangerous", false, "run the agent with its permission-skipping flag (claude: --dangerously-skip-permissions, codex: --yolo)")
	branch := fs.String("branch", "", "existing branch to check out, instead of creating a new one")
	ticket := fs.String("ticket", "", "ticket URL to attach to the session")
	prompt := fs.String("prompt", "", "initial prompt to type into the new session's agent pane")
	list := fs.Bool("list", false, "list configured project names and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *list {
		a, err := newApp()
		if err != nil {
			return err
		}
		for _, name := range a.Cfg.OrderedProjectNames() {
			fmt.Println(name)
		}
		return nil
	}

	if *project == "" {
		return fmt.Errorf("spawn: -project is required (run 'moomux spawn -list' to see configured projects)")
	}

	a, err := newApp()
	if err != nil {
		return err
	}

	s, hint, err := a.CreateSession(*project, *name, *agent, *branch, *ticket, true, *dangerous, "", *model, *thinking)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	if hint != "" {
		fmt.Println(hint)
	}
	fmt.Println(s.TmuxSession)

	if *prompt != "" {
		firstPrompt := *prompt
		// codex's thinking level is a real -c model_reasoning_effort flag,
		// already applied to the launch command above; claude/opencode have
		// no such flag, so lean on the same magic words a user would type
		// into the prompt by hand.
		if *agent != "codex" && *thinking != "" && *thinking != "default" {
			firstPrompt = *thinking + ": " + firstPrompt
		}
		// StartFirstPrompt waits for the agent to actually be ready for
		// input (rather than a fixed delay) and errors out — instead of
		// silently returning success — if the pane looks stuck on something
		// else, e.g. an interactive SSH passphrase prompt from a
		// worktree-create userscript or the agent's own launch command.
		if err := a.StartFirstPrompt(s.TmuxSession, firstPrompt, true); err != nil {
			return fmt.Errorf("send prompt: %w (session %s was created but may need manual attention: tmux attach -t %s)", err, s.TmuxSession, s.TmuxSession)
		}
	}
	return nil
}

// runTag implements `moomux tag`: attach a ticket and/or PR URL to whichever
// moomux session's worktree the current directory is inside (or a
// subdirectory of), so it can be run from an agent's own pane right after
// opening a PR. Flags left unset keep the session's existing value rather
// than clearing it. Called with neither flag set, it just prints the
// session's current ticket/PR instead of erroring, so an agent can check
// what's already tracked before deciding whether it needs to go find a link.
func runTag(args []string) error {
	fs := flag.NewFlagSet("tag", flag.ExitOnError)
	ticket := fs.String("ticket", "", "ticket URL to attach (kept as-is if omitted)")
	pr := fs.String("pr", "", "pull request URL to attach (kept as-is if omitted)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	a, err := newApp()
	if err != nil {
		return err
	}
	s, err := currentSession(a)
	if err != nil {
		return fmt.Errorf("tag: %w", err)
	}

	if *ticket == "" && *pr == "" {
		fmt.Printf("ticket: %s\npr: %s\n", orNone(s.Ticket), orNone(s.PR))
		return nil
	}

	if *ticket == "" {
		*ticket = s.Ticket
	}
	if *pr == "" {
		*pr = s.PR
	}
	if _, err := a.SetSessionTags(s.ID, *ticket, *pr); err != nil {
		return fmt.Errorf("tag: %w", err)
	}
	fmt.Println("tagged " + s.Name)
	return nil
}

// orNone renders an empty tag value as "none" for `moomux tag`'s status
// output, rather than a blank line that reads as an error.
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// runPark implements `moomux park`: stop the tmux session and close its
// terminal tab (if any) for whichever moomux session we're currently
// running in — the CLI backend for the /kill slash command
// (internal/claudehook.EnsureKillCommandInstalled), invoked from the
// agent's own pane. Like `moomux tag`, it identifies "the session we're in"
// via currentSession rather than taking an explicit ID. The worktree,
// branch, and moomux list entry are left intact so the session can be
// reopened later — see App.KillTmux's doc comment.
//
// Closing the current terminal tab terminates the process group that invoked
// this command before it can kill tmux. Run the actual park operation in a
// new session so it survives the tab closing long enough to finish.
func runPark() error {
	a, err := newApp()
	if err != nil {
		return err
	}
	s, err := currentSession(a)
	if err != nil {
		return fmt.Errorf("park: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("park: resolve executable: %w", err)
	}
	cmd := newParkHelperCommand(executable, "__park-detached", s.ID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("park: %w", err)
	}
	fmt.Println("parked " + s.Name)
	return nil
}

// newParkHelperCommand leaves Stdin/Stdout/Stderr unset (-> /dev/null, see
// os/exec's Cmd docs) rather than inheriting this process's own: when the
// caller is a slash command like /kill, this process's stdout is a pipe the
// host CLI reads to capture command output, not the pane's tty. Inheriting
// that pipe into a detached helper ties its lifetime to the pipe staying
// open, which defeats the whole point of detaching it from the pane it's
// about to kill — the read side blocking on/timing out waiting for EOF can
// tear the helper down before it reaches CloseTab.
func newParkHelperCommand(executable, subcommand, id string) *exec.Cmd {
	cmd := exec.Command(executable, subcommand, id)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// runParkDetached is the first of two detachment layers and deliberately
// does no park work itself. Setsid shields it from the SIGHUP a killed tmux
// session's pty delivers to the pane's foreground process group, but not
// from a supervisor that reaps its own descendants by walking the process
// tree rather than relying on that signal — e.g. a host CLI like Claude
// Code cleaning up whatever /kill spawned once the shell command that ran
// it is gone. Setsid changes this process's session, not its place in that
// tree: it's still a child of `moomux park`, which is still a child of the
// pane being killed.
//
// So instead of parking here, this starts a second Setsid'd child
// (__park-worker) with Start (not Run) and returns immediately without
// waiting on it. The moment this process exits, the OS reparents that
// child away to init/launchd — off the tree entirely — before the
// supervisor's cleanup has a real chance to reach it.
func runParkDetached(id string) error {
	executable, err := os.Executable()
	if err != nil {
		slog.Error("park-detached: resolve executable failed", "id", id, "err", err)
		return err
	}
	if err := newParkHelperCommand(executable, "__park-worker", id).Start(); err != nil {
		slog.Error("park-detached: start worker failed", "id", id, "err", err)
		return err
	}
	return nil
}

// runParkWorker does the actual park operation — see runParkDetached's doc
// comment for why it runs a step further removed from `moomux park` than
// the process that started it. Its stderr is /dev/null too (same reasoning
// as runPark), so a failure here is otherwise invisible — log it.
func runParkWorker(id string) error {
	a, err := newApp()
	if err != nil {
		slog.Error("park-worker: load app failed", "id", id, "err", err)
		return err
	}
	if err := a.KillTmux(id); err != nil {
		slog.Error("park-worker: kill tmux failed", "id", id, "err", err)
		return fmt.Errorf("park: %w", err)
	}
	return nil
}

// runReseed implements `moomux reseed`: re-run the worktree-create
// userscripts for whichever moomux session we're currently running in, with
// MOOMUX_FORCE=1 so they redo setup they'd otherwise skip as already done
// (e.g. wt-moomux-hook.sh passing --force through to wt-seed-env.sh to
// re-copy template files after they changed). Warnings a script prints
// (including a normal "skipping ..." kind of notice) are surfaced the same
// way session creation surfaces them: printed, not treated as failure.
func runReseed() error {
	a, err := newApp()
	if err != nil {
		return err
	}
	s, err := currentSession(a)
	if err != nil {
		return fmt.Errorf("reseed: %w", err)
	}
	for _, w := range a.ReseedWorktree(s) {
		fmt.Println(w)
	}
	fmt.Println("reseeded " + s.Name)
	return nil
}

// currentSession identifies "the moomux session this process is running
// in" for the park/tag CLI backends. It prefers the tmux session the
// caller's pane actually belongs to (via $TMUX, resolved by
// Tmux.CurrentSessionName) over cwd: an agent that `cd`s into a sibling
// worktree earlier in the conversation — to read a file, say — leaves cwd
// pointing at that *other* session's directory, which previously made
// SessionForPath silently park or tag the wrong session. The tmux session a
// pane belongs to can't change out from under it that way, so it's the more
// reliable signal. Falls back to cwd only when not running inside tmux at
// all (SessionForTmuxName has nothing to key off).
func currentSession(a *app.App) (session.Session, error) {
	if os.Getenv("TMUX") != "" {
		if name, err := a.Tmux.CurrentSessionName(); err == nil {
			if s, ok := a.SessionForTmuxName(name); ok {
				return s, nil
			}
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return session.Session{}, err
	}
	s, ok := a.SessionForPath(cwd)
	if !ok {
		return session.Session{}, fmt.Errorf("no moomux session found for %s (run this from inside a session's worktree)", cwd)
	}
	return s, nil
}

func run() error {
	a, err := newApp()
	if err != nil {
		return err
	}
	cfg, cfgPath := a.Cfg, a.CfgPath
	// Shared across both prompts: a fresh bufio.Reader per prompt can read
	// ahead past its own newline, silently discarding a fast/pasted second
	// answer meant for the next prompt.
	stdin := bufio.NewReader(os.Stdin)

	if !cfg.TmuxSetupAsked {
		promptTmuxSetup(stdin, cfg, cfgPath)
	}
	if !cfg.AutoTmuxAsked {
		promptAutoTmux(stdin, cfg, cfgPath)
	}
	if cfg.AutoTmux && os.Getenv("TMUX") == "" {
		if err := relaunchInTmux(); err != nil {
			fmt.Fprintln(os.Stderr, "moomux: could not start inside tmux:", err)
		}
	}

	home, _ := os.UserHomeDir()
	return runProgram(cfg, a, buildWatcher(home))
}

// runProgram drives the TUI against any backend + watcher, so the local
// (*app.App) path and the socket-backed (*ipc.Client) path share one setup.
func runProgram(cfg *config.Config, b tui.Backend, w watcher.Watcher) error {
	ctx, cancel := context.WithCancel(context.Background())
	statusCh := make(chan watcher.Snapshot, 4)
	go w.Run(ctx, statusCh)

	tui.ApplySettings(cfg)
	m := tui.New(cfg, b, statusCh, cancel)
	m.Version = version
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		cancel()
		return err
	}
	cancel()
	if m.Relaunch {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		return syscall.Exec(self, os.Args, os.Environ())
	}
	return nil
}

// runServe implements `moomux serve`: expose the orchestration core on a
// unix socket so a second front end (the TUI over -socket, or a native app)
// can drive it without linking any of this.
func runServe(args []string) error {
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	sock := fs.String("socket", ipc.DefaultSocket(home), "unix socket path to listen on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := newApp()
	if err != nil {
		return err
	}
	ln, err := ipc.Listen(*sock)
	if err != nil {
		return err
	}
	defer ln.Close()
	fmt.Fprintln(os.Stderr, "moomux: serving on", *sock)
	return (&ipc.Server{Backend: a, Config: a.ConfigSnapshot, Watcher: buildWatcher(home)}).Serve(ln)
}

// runRemote implements `moomux ui -socket`: the same TUI, driven entirely
// over the socket. Proves the boundary is complete — anything it can't do
// is a hole a native client would hit too.
func runRemote(args []string) error {
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	sock := fs.String("socket", ipc.DefaultSocket(home), "unix socket of a running `moomux serve`")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c := &ipc.Client{Socket: *sock}
	cfg, err := c.Config()
	if err != nil {
		return fmt.Errorf("connect %s: %w (is `moomux serve` running?)", *sock, err)
	}
	// The TUI reads projects and settings off this pointer for the rest of
	// the run; Bind lets the client refresh it in place after any change,
	// the way App mutating its own a.Cfg does locally.
	c.Bind(cfg)
	return runProgram(cfg, c, c)
}

func buildWatcher(home string) watcher.Watcher {
	return &watcher.MultiWatcher{Watchers: []watcher.Watcher{
		// Claude Code: JSON session files in ~/.claude/sessions/
		&watcher.DirWatcher{Dir: filepath.Join(home, ".claude", "sessions")},
		// Codex: activity tracked in SQLite DB (~/.codex/state_N.sqlite),
		// plus needs-input markers from internal/codexhook's installed hooks.
		&watcher.SQLiteWatcher{
			DB:        filepath.Join(home, ".codex", "state_*.sqlite"),
			Query:     "SELECT cwd, MAX(updated_at_ms) FROM threads GROUP BY cwd",
			MarkerDir: codexhook.MarkerDir(home),
		},
		// OpenCode: activity tracked in SQLite DB (~/.local/share/opencode/opencode.db)
		&watcher.SQLiteWatcher{
			DB:    filepath.Join(home, ".local", "share", "opencode", "opencode.db"),
			Query: "SELECT directory, MAX(time_updated) FROM session GROUP BY directory",
		},
	}}
}
