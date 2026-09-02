// Package config loads and writes moomux's TOML configuration.
package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/erickgnclvs/moomux/internal/atomicfile"
)

type Project struct {
	Kind         string `toml:"kind,omitempty"` // "git" (default) or "plain"
	Repo         string `toml:"repo"`
	BranchPrefix string `toml:"branch_prefix,omitempty"`
	BaseBranch   string `toml:"base_branch,omitempty"`
	Agent        string `toml:"agent,omitempty"` // "claude" (default), "codex", "opencode"
	// Dangerous, when true, runs Agent with its permission-skipping flag
	// (claude: --dangerously-skip-permissions, codex: --yolo); no-op for
	// opencode. Applies as the default for new sessions of this project.
	Dangerous bool `toml:"dangerous,omitempty"`
	// PromptAgent, when true, skips preselecting Agent as the default in the
	// new-session form — the user must explicitly pick an agent before the
	// form can be submitted.
	PromptAgent bool `toml:"prompt_agent,omitempty"`
	// NoWorktree, when true on a "git" project, keeps every session in Repo
	// itself instead of giving each one its own worktree/branch — the same
	// single-folder behavior as a "plain" project, but for a real git repo.
	NoWorktree bool `toml:"no_worktree,omitempty"`
	// Emoji is a short, user-set glyph shown in place of the project name in
	// compact views (e.g. the all-projects session list). Empty means no
	// glyph has been chosen — callers fall back to a deterministic pick.
	Emoji string `toml:"emoji,omitempty"`
}

func (p Project) IsPlain() bool { return p.Kind == "plain" }

// ProjectEmojiPalette is the fallback set for projects that haven't chosen
// their own emoji (Project.Emoji) — picked deterministically per project
// name so the same project always gets the same glyph.
var ProjectEmojiPalette = []string{"🐙", "🦊", "🚀", "🔥", "🌊", "🍀", "⚡", "🎯", "🐝", "🦉"}

// ProjectEmoji returns project's configured emoji, falling back to a
// deterministic pick from ProjectEmojiPalette if none is set.
func (c *Config) ProjectEmoji(project string) string {
	if e := c.Projects[project].Emoji; e != "" {
		return e
	}
	var h int
	for _, r := range project {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return ProjectEmojiPalette[h%len(ProjectEmojiPalette)]
}

// UsesWorktree reports whether sessions for this project should each get
// their own git worktree. False for plain projects and for git projects
// that opted out via NoWorktree — both cases run every session directly in
// p.Repo.
func (p Project) UsesWorktree() bool { return !p.IsPlain() && !p.NoWorktree }

// OrderedProjectNames returns configured project names in the user's manual
// order (c.Order), followed by any names not yet in that order (new
// projects, or configs from before manual ordering existed) sorted
// alphabetically.
func (c *Config) OrderedProjectNames() []string {
	seen := make(map[string]bool, len(c.Projects))
	out := make([]string, 0, len(c.Projects))
	for _, name := range c.Order {
		if _, ok := c.Projects[name]; ok && !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	rest := make([]string, 0, len(c.Projects)-len(out))
	for name := range c.Projects {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// AgentName returns the effective agent name, defaulting to "claude".
func (p Project) AgentName() string {
	if p.Agent == "" {
		return "claude"
	}
	return p.Agent
}

type Config struct {
	Projects map[string]Project `toml:"projects"`
	// Order is the user's manual project ordering (front-to-back). Names not
	// listed here (new projects, or configs written before this existed)
	// sort alphabetically after the ordered ones.
	Order []string `toml:"order,omitempty"`
	// TmuxSetupAsked marks that the user has already been asked whether to
	// add moomux's recommended ~/.tmux.conf settings, so the prompt only
	// ever runs once regardless of their answer.
	TmuxSetupAsked bool `toml:"tmux_setup_asked,omitempty"`
	// AutoTmux, when true, relaunches moomux inside a dedicated tmux
	// session ("moomux") on startup if it isn't already running inside one.
	AutoTmux bool `toml:"auto_tmux,omitempty"`
	// AutoTmuxAsked marks that the user has already been asked whether to
	// enable AutoTmux, so the prompt only ever runs once.
	AutoTmuxAsked bool `toml:"auto_tmux_asked,omitempty"`
	// Theme selects the color palette ("default", "terminal", "gruvbox",
	// "catppuccin"); empty (or unrecognized) means "default".
	Theme string `toml:"theme,omitempty"`
	// Appearance forces which half of every theme's light/dark color pairs
	// renders ("light" or "dark"); empty means auto-detect from the terminal.
	Appearance string `toml:"appearance,omitempty"`
	// AutoSubmitDefault is the initial state of the new-session form's
	// auto-submit toggle, remembered from whatever the user last set it to.
	AutoSubmitDefault bool `toml:"auto_submit_default,omitempty"`
	// SortRecentFirst, when true, lists sessions most-recently-opened first
	// instead of the manual Order set by shift+↑↓. The two don't compose —
	// manual reordering is disabled while this is on, since every open would
	// otherwise undo it.
	SortRecentFirst bool `toml:"sort_recent_first,omitempty"`
	// CompactDetail, when true, trims the detail panel to the fields most
	// useful at a glance — dropping project/agent/ticket/worktree/created,
	// shrinking the cowsay art down to a one-line quip and face (or omitting
	// it entirely on narrow layouts, where the header already shows one),
	// and shortening the PR link to just its number (e.g. "#5478") — so the
	// panel stays short even when a session has both a ticket and a PR
	// attached. pr status (merged/CI state) is left alone either way.
	CompactDetail bool `toml:"compact_detail,omitempty"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{Projects: map[string]Project{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return nil, err
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.Projects == nil {
		cfg.Projects = map[string]Project{}
	}
	for k, p := range cfg.Projects {
		p.Repo = ExpandHome(p.Repo)
		cfg.Projects[k] = p
	}
	return cfg, nil
}

// Reload re-reads path and overwrites cfg's fields in place — same
// *Config pointer, refreshed contents. Every App method that mutates the
// config calls this first so its write lands on top of whatever another
// moomux process (e.g. a second TUI, or `moomux spawn`) has saved since
// cfg was last loaded, instead of clobbering it with a stale in-memory
// snapshot (the same fix session.Store.reloadLocked applies to sessions).
// The pointer must stay the same because the TUI holds this exact *Config
// and reads its fields directly, not through App's accessors.
func Reload(path string, cfg *Config) error {
	fresh, err := Load(path)
	if err != nil {
		return err
	}
	*cfg = *fresh
	return nil
}

func Save(path string, cfg *Config) error {
	// Encode into a buffer, then write atomically: encoding straight into
	// the live file truncates it first, so a crash or full disk mid-encode
	// destroys every project definition.
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	return atomicfile.Write(path, buf.Bytes(), 0o644)
}

func DefaultPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "moomux", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "moomux", "config.toml")
}

// ExpandHome expands a leading "~" to the user's home directory. Only a bare
// "~" or "~/..." is home-relative; "~foo" (another user's home in shell
// syntax) is left untouched rather than wrongly resolved against the
// current user's home. Load already applies this to every stored Repo;
// callers validating freshly typed input before it's ever saved (e.g.
// app.validateProject) need it too.
func ExpandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
