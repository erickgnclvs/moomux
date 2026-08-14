
# moomux

```
 ________________________________
< cowsay goes ai agents and tmux >
 --------------------------------
        \   ^__^
         \  (oo)\_______
            (__)\       )\/\
                ||----w |
                ||     ||
```

A TUI for managing [Claude Code](https://claude.com/claude-code) agent sessions across git worktrees. Creates a worktree + branch, starts a tmux session, launches the session's agent (`claude`, `codex`, or `opencode`), and opens a terminal tab — all in one keypress. Single Go binary, no daemon.

## Session layout

Each session is a tmux window split into two panes, both in the worktree directory:

- **Left (~2/3 width)** — the agent (`claude`, `codex`, or `opencode`)
- **Right (~1/3 width)** — a plain shell, for `git`, tests, etc. alongside the agent

It's a regular tmux window, so regular tmux pane controls apply — mouse click/drag to switch panes or resize (mouse mode is on by default), or the usual prefix keys:

| Action | Keys |
|---|---|
| Switch pane | `Ctrl-b` then arrow key |
| Cycle panes | `Ctrl-b o` |
| Split pane vertically (side-by-side) | `Ctrl-b %` |
| Split pane horizontally (stacked) | `Ctrl-b "` |
| Zoom/unzoom pane | `Ctrl-b z` |
| Close pane | `Ctrl-b x` |

These are plain tmux, not a moomux feature — see `man tmux` for the full list.

### Remote and mobile use

For a phone-first setup—including Mosh, durable tmux sessions, mobile
approvals, notifications, voice prompting, and diff review—see
[Moshi with Claude Code](https://getmoshi.app/guides/claude-code).

Over SSH or Mosh, moomux may show an attach command instead of opening a new
terminal itself:

```bash
tmux attach -t moomux-<session>
```

On a narrow phone display, focus the agent pane and zoom it:

1. Press `Ctrl-b`, then `←` to select the agent pane on the left.
2. Press `Ctrl-b`, then `z` to make it fill the screen.
3. Press `Ctrl-b`, then `z` again to restore the agent + shell split.

`Ctrl-b o` cycles between panes, and `Ctrl-b d` detaches without stopping the
agent. Run `tmux ls` to find running session names before attaching again.

Tmux pane layout and zoom state belong to the shared window, not to an
individual client. If a phone and desktop are attached to the same session,
zooming or resizing from either device can affect what the other displays.

## Recommended tmux config

moomux launches plain tmux sessions, so tmux settings come from your own config. The first time you run `moomux`, it offers to add the block below to `~/.tmux.conf` for you (only asked once — say no and it won't ask again). To add it yourself instead:

```tmux
# Essential for Claude to avoid output breaking and desktop notification issues
set -g allow-passthrough on
set -s extended-keys on
set -as terminal-features 'xterm*:extkeys'

# Enable native mouse scrolling and selection
set -g mouse on

# Without this, tmux swallows OSC 52 clipboard writes from programs in its
# panes instead of forwarding them to your terminal, so copy-to-clipboard
# silently does nothing when you're attached over SSH or mosh (mosh isn't
# auto-detected — press R in moomux to force copy mode there)
set -g set-clipboard on

# Increase scrollback history for Claude's massive code generations
set -g history-limit 50000

# Start windows and panes at 1 instead of 0 for easier navigation
set -g base-index 1
set -g pane-base-index 1
```

Reload it in any running tmux session without restarting: press `Ctrl-b` then `:`, type `source-file ~/.tmux.conf`, and hit enter. Or from a shell: `tmux source-file ~/.tmux.conf`.

If moomux already added its block to your `~/.tmux.conf` before `set -g set-clipboard on` existed, it won't be re-offered (moomux only checks that the block is present, not that it's current) — add that line yourself and reload as above.

## Clicking ticket/PR links

Each session can be tagged with a ticket and/or PR link (`t`), shown as icons in the session list and as a row in the detail panel. Clicking either:

- **Locally** — opens the link in your default browser, as you'd expect.
- **Over SSH** — copies the link to your clipboard (via the OSC 52 escape sequence above) instead of opening it, since `open`/`xdg-open` would launch a browser on the remote machine rather than the one you're actually looking at.

SSH is auto-detected. Other transports (e.g. mosh) don't set anything moomux can detect, so press `R` to force copy mode on (or back off, toggling) — the current state is shown in the `?` help overlay.


## Install

```bash
# Homebrew (recommended)
brew tap erickgnclvs/moomux
brew install moomux

# Go
go install github.com/erickgnclvs/moomux@latest

# From source
git clone https://github.com/erickgnclvs/moomux && cd moomux && make install
```

Requires `tmux`, `git`, and `claude` on `$PATH`.

**Linux**: moomux detects the terminal it's running in from environment variables. It opens each session as a **new tab** in GNOME Terminal, Konsole, WezTerm, and kitty (kitty needs `allow_remote_control yes` + `listen_on unix:/tmp/kitty` in `kitty.conf`; without it you get a new window), and as a **new window** in Ghostty, Alacritty, foot, Tilix, and xterm. Other VTE-based terminals (Ptyxis, GNOME Console, Xfce Terminal, ...) open via `gnome-terminal` when it's installed. In anything else — including over SSH — moomux shows a `tmux attach -t <session>` hint instead of failing.

**Windows**: tmux has no native Windows build. Run moomux inside [WSL](https://learn.microsoft.com/windows/wsl/install) — the Linux binary above works as-is. In Windows Terminal, moomux opens a new tab and attaches automatically; in any other terminal it prints a `tmux attach -t <session>` hint instead.

## Build

```bash
git clone https://github.com/erickgnclvs/moomux && cd moomux

make build    # compile ./moomux
make test     # go test ./... -race -count=1
make test-e2e # go test -tags e2e ./e2e/... — real tmux sessions + git worktrees under a temp dir
make install  # build + copy to $PREFIX/bin (default ~/.local/bin)
make run      # build + run
make clean    # remove the built binary, and the copy make install left in $PREFIX/bin
```

Requires Go, plus `tmux` and `git` (checked by `make install`/`make run` via `check-deps`).

Or build and run directly with `go` instead of `make`:

```bash
go build -o moomux . && ./moomux
```

### Local development

If you installed moomux via Homebrew, `moomux` on your `$PATH` resolves to
Homebrew's bin dir (`/opt/homebrew/bin` on Apple Silicon, `/usr/local/bin` on
Intel Macs and Linuxbrew — run `brew --prefix` to check yours) — and that
typically comes *before* `~/.local/bin` in `$PATH`, so `make install` alone
won't make your local build the one that actually runs when you type
`moomux`, or the one tmux/hooks invoke as a subprocess.

To develop against your own build:

1. Make sure `~/.local/bin` comes before Homebrew's bin dir in `$PATH` — e.g.
   add `export PATH="$HOME/.local/bin:$PATH"` near the end of your shell
   profile (after any Homebrew `shellenv` line), so it wins unconditionally.
2. `make install` from your working copy.
3. Open a new shell (or `source` your profile / `hash -r`) so the `$PATH`
   change and shell command cache pick up the change, then confirm with
   `which moomux` and `moomux --version` that it now resolves to your build.

Run `make clean` to remove the installed dev build (`$PREFIX/bin/moomux`, in
addition to the local `./moomux` binary) and fall back to the Homebrew/Go
install again.

## Run

```bash
moomux
```

Keys: `?` help (full command list) · `n` new · `enter` open · `x` park · `d` delete · `a` archive/restore · `A` toggle archived view · `t` tag · `e` edit session · `E` edit project · `shift+↑`/`shift+↓` reorder · `tab` switch project · `q` quit

Press `?` at any time on the list screen to open a command palette with every keybinding grouped by category, so you don't have to memorize the footer.

### Tab titles

moomux drives each session's terminal tab title through its tmux window name: it enables tmux's `set-titles`/`set-titles-string` so the terminal continuously mirrors `#{window_name}`, and prefixes that name with a status glyph (`●` working, `⚠` needs input, `✓` done) as the session's state changes. Renaming the tmux window directly (`Ctrl-B ,`) sticks — status updates only swap the glyph and leave the rest of the name alone. Renaming the tab from the terminal's own UI (e.g. iTerm2's "Edit Tab Title") does not stick: tmux has no visibility into that rename and keeps re-pushing its own window name over it, independent of session status.

### Parking a session from inside Claude or Codex

Every Claude Code or Codex session moomux opens gets a personal `/kill` command installed automatically (`~/.claude/commands/kill.md`, or `~/.codex/prompts/kill.md`) — say `/kill` in the chat itself and it parks that session (stops its tmux session, closes its terminal tab) without switching back to the moomux list. Despite the name it's the same as pressing `x`, not `d`: the worktree, branch, and moomux list entry are kept, so the session can be reopened later.

Codex may invoke it as `/prompts:kill` instead of `/kill`, and requires restarting the session once after the prompt is first installed (or after a moomux upgrade changes it) before it shows up — type `/` to see what's actually available. Codex's custom prompts are also plain text, not an executed command: it hands Codex an instruction to run `moomux park` rather than running it directly the way Claude Code's version does, so it depends on Codex actually following through. opencode sessions have no equivalent yet — use `x` from the list.

The chorded keys have plain-letter alternates for keyboards that can't send modifier+special-key chords (mobile terminal clients, terminals without `extended-keys`): `K`/`J` reorder session, `H`/`L` reorder project, `[`/`]` switch project.

## Spawning a session from the CLI

`moomux spawn` creates a session non-interactively — no TUI, just a worktree + tmux session + agent, same as pressing `n` — and optionally types an initial prompt into the agent's pane. Useful for one agent to delegate a sub-task to a fresh session of its own, or for any script/automation:

```bash
moomux spawn -project <project> [-name <name>] [-agent claude|codex|opencode] \
  [-branch <existing-branch>] [-ticket <url>] [-prompt "<initial task>"]
```

It's fire-and-forget: prints the new tmux session's name and exits immediately, without waiting for the agent or reporting anything back. Run `moomux spawn -h` for the full flag list, or `moomux --help` for top-level usage.
