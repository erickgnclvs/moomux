# Custom pane layouts

By default every session is a single tmux window split into two panes (see
the main [README](../README.md#session-layout)). A project can override this
per worktree by dropping a `.moomux-panes.toml` file at the worktree root —
moomux checks for it each time a session's tmux window is (re)created and
falls back to the default layout if the file is missing or invalid — two
panes, or the agent alone when the "terminal pane" setting is off.

## Shape

The file is a list of windows, each with an optional `name` and a tree of
panes:

```toml
[[windows]]
name = "main"        # optional; tmux auto-names the window when omitted
direction = "row"    # "row": side-by-side columns, "col": stacked rows

  [[windows.children]]
  size = "60%"        # this child's share of the window; omit to split evenly
  agent = true         # exactly one pane across the whole file must be the agent

  [[windows.children]]
  size = "40%"
  cmd = "npm run dev"  # shell command to run in this pane
```

A pane entry is either a **split** (`direction` + at least two `children`) or
a **leaf** (`agent = true`, or `cmd = "..."`, or neither for a bare shell).
Splits nest arbitrarily, so a child can itself be a `direction` + `children`
to build a full grid — a row can contain a column, which can contain another
row, and so on.

Whichever window contains `agent = true` always gets focus when the session
is created, and always takes moomux's own session display name — any `name`
set on that window in the file is ignored. Every other window uses its own
`name`, falling back to tmux's default numbering when omitted.

## Examples

**Agent on the right, dev server and log tail stacked on the left:**

```toml
[[windows]]
direction = "row"

  [[windows.children]]
  direction = "col"
  size = "50%"

    [[windows.children.children]]
    cmd = "npm run dev"

    [[windows.children.children]]
    cmd = "tail -f logs/dev.log"

  [[windows.children]]
  size = "50%"
  agent = true
```

**Multiple windows — the agent's own window, plus a separate logs window:**

```toml
[[windows]]
agent = true

[[windows]]
name = "logs"
direction = "col"

  [[windows.children]]
  cmd = "tail -f logs/dev.log"

  [[windows.children]]
  cmd = "docker compose logs -f"
```

**2x2 grid, agent bottom-left:**

```toml
[[windows]]
direction = "row"

  [[windows.children]]
  direction = "col"
  size = "50%"

    [[windows.children.children]]
    cmd = "top-left"

    [[windows.children.children]]
    agent = true

  [[windows.children]]
  size = "50%"
  cmd = "right-pane"
```
