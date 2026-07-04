
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

A TUI for managing [Claude Code](https://claude.com/claude-code) agent sessions across git worktrees. Creates a worktree + branch, starts a tmux session, launches `claude`, and opens a terminal tab — all in one keypress. Single Go binary, no daemon.

## Demo

https://github.com/user-attachments/assets/6a3aec4e-6c30-4cdf-89fa-fdadf02c6f3a

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

## Build

```bash
git clone https://github.com/erickgnclvs/moomux && cd moomux

make build    # compile ./moomux
make test     # go test ./... -race -count=1
make install  # build + copy to $PREFIX/bin (default ~/.local/bin)
make run      # build + run
make clean    # remove the built binary
```

Requires Go, plus `tmux` and `git` (checked by `make install`/`make run` via `check-deps`).

Or build and run directly with `go` instead of `make`:

```bash
go build -o moomux . && ./moomux
```

## Run

```bash
moomux
```

Keys: `n` new · `enter` open · `x` kill · `d` delete · `tab` switch project · `q` quit
