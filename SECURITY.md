# Security

## Reporting a vulnerability

Use GitHub's private vulnerability reporting: the **Security** tab of this
repository, then **Report a vulnerability**. Do not put exploit details in a
public issue. If that button is missing, open an issue that says only "security
contact requested", and a maintainer will reply with a private channel.

Only the latest release gets fixes.

## Trust model

moomux runs on your machine, as you. It starts agents in your worktrees, and an
agent can run any command you can. These are the boundaries that matter.

| Surface | Who can use it | Notes |
|---|---|---|
| Unix socket `~/.local/share/moomux/moomux.sock` | Processes running as your user | Mode 0600. This is the whole access control. Anyone who can open it can create sessions, run your userscripts, and start an agent with its permission-skipping flag. |
| Tailnet listener (`tailnet_listen = true` in `config.toml`) | Devices signed in to Tailscale as you | Off until you run `moomux pair` or set it by hand. Each connection is checked with `tailscale whois`. There is no per-device credential: any device signed in as you has the same power as the socket. Never point `tailscale serve` or Funnel at its ports. |
| Browser proxy on the tailnet | Same as the tailnet listener | HTTP `CONNECT` only. It refuses loopback and private targets that a web page could register by DNS rebinding. |
| Userscripts in `~/.config/moomux/userscripts/` | You | They run on every create and delete, with your permissions. moomux never loads scripts from a repository. |

`docs/wire-protocol.md` has the full detail on the socket and tailnet design.

### Things moomux changes outside its own directories

- It adds hooks and slash commands (`/kill`, `/tag`, `/spawn`, `/reseed`) to
  the settings of each agent you use.
- It marks each worktree it creates as trusted in `~/.claude.json`, so Claude
  Code does not ask for trust at launch. Only create sessions in repositories
  you already trust.
- It can add a block to `~/.tmux.conf` (only after asking).

### Files and permissions

`moomux.log`, `sessions.json` and `config.toml` may contain prompts, branch
names and ticket URLs. Treat them as private. Do not copy them into bug
reports without reading them first.
