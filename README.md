# cjx-sandbox

[![CI](https://github.com/hornc-greedy/cjx-sandbox/actions/workflows/ci.yml/badge.svg)](https://github.com/hornc-greedy/cjx-sandbox/actions/workflows/ci.yml)
[![latest release](https://img.shields.io/github/v/release/hornc-greedy/cjx-sandbox)](https://github.com/hornc-greedy/cjx-sandbox/releases/latest)
![go version](https://img.shields.io/badge/go-1.27%2B-blue)
[![license](https://img.shields.io/github/license/hornc-greedy/cjx-sandbox)](LICENSE)

`cjx` runs a coding agent - Claude Code, OpenAI Codex or pi.dev - inside a Podman or Docker sandbox. The agent can read your project, write only when you say so, and reach the internet only on hosts you have allow-listed.

```
$ cjx
┃ cjx
┃ > run        Start the sandbox and run a coding agent
┃   status     Show sandbox image, volume, and session status
┃   config     View and edit the files under ~/.config/cjx
┃   update     Pull base images and rebuild the sandbox images
┃   prune      Remove images and offer to remove volumes that no agent maps to any more
┃   migrate    Copy the agent home volumes into the other container engine
┃   export     Write an agent's home volume to an archive
┃   import     Restore an agent's home volume from an archive
┃   uninstall  Remove the sandbox images, sessions, and (optionally) your saved agent data
┃   install    Copy the cjx binary into ~/.local/bin
┃   engine     podman
┃   Quit

↑ up • ↓ down • ← back • / filter • enter submit

┃ Select an agent
┃ > claude: Claude Code
┃   pi.dev: pi.dev
┃   codex: OpenAI Codex

┃ run options
┃ space answers the line under the cursor · enter runs run
┃ > [ ] --add-dir      Mount an extra host directory into the sandbox at the same path (repeatable)
┃   [ ] --no-firewall  Bypass the firewall and allow unrestricted network access
┃   [x] --rw           Mount the project directory read-write (default: read-only)
┃   [ ] --select       Re-prompt for agent and mode, ignoring the saved selection
┃   [ ] --shell        Start an interactive bash shell instead of the agent

Starting agent...
```

## Why use it

LLM-driven agents read your prompt, your repo, and the web - and then act on what they think you wanted. cjx puts a hard boundary between that "act" step and your laptop:

- **Project read-only by default.** The agent can read your code, but cannot rewrite a file unless you pass `--rw`.
- **Allow-listed network.** All outbound HTTPS traffic goes through an in-network [Squid](http://www.squid-cache.org/) proxy. Any host that isn't on the allowlist is rejected.
- **No host privileges.** Container runs unprivileged, dropped capabilities, read-only rootfs, capped CPU / memory / PIDs. Under rootless Podman the daemonless runtime itself has no privileges either; under Docker the daemon runs as root and anyone who can reach it can become root on the host, which cjx cannot take away.
- **Persistence is opt-in and explicit.** Agent home (OAuth tokens, history, caches) lives in a named container volume, not on your real filesystem.
- **`.env` and `secrets/` denied at the Claude layer.** The cjx-base image ships a Claude Code managed-settings policy (`/etc/claude-code/managed-settings.json`) that blocks reads of `.env`, `.env.*` and `secrets/**`, so credentials don't leak into the agent's context even when project read access is granted. The policy is Claude Code's own - codex has no read-deny to set, so it reads what the mount gives it.
- **Agents and modes are built in.** Your own go into `~/.config/cjx/agents.local.toml`.

It's a *practical* boundary, not a defence against a kernel exploit.

## Get started

### 1. Install a container engine

cjx drives [Podman 4+](https://podman.io/docs/installation) rootless or
[Docker](https://docs.docker.com/engine/install/) with a rootful daemon. **Podman
rootless is the recommended one**; see [Engine selection](#engine-selection) for
what differs.

- **Linux:** `sudo dnf install podman` / `sudo pacman -S podman` / `sudo apt install podman`
- **macOS (Apple Silicon):** `brew install podman`, then `podman machine init && podman machine start`
- **Windows:** Native Windows is not supported. cjx is untested on WSL2 but should work in theory: install podman inside the WSL distribution and keep project files inside the WSL filesystem rather than under `/mnt/c/...` for usable bind-mount performance. Reports welcome.

Verify with `podman info` (or `docker info`) - it should print without errors.

### 2. Install cjx

**Option A - pre-built binary.** Grab the latest tarball from the [releases page](https://github.com/hornc-greedy/cjx-sandbox/releases) (`cjx-installer-linux-amd64.tar.gz` or `cjx-installer-darwin-arm64.tar.gz`), then:

```bash
tar -xzf cjx-installer-linux-amd64.tar.gz
./cjx install
```

**Option B - build from source** (needs Go 1.27+):

```bash
git clone https://github.com/hornc-greedy/cjx-sandbox.git
cd cjx-sandbox
go build -o cjx ./cmd/cjx
./cjx install
```

This copies the binary to `~/.local/bin/cjx`. Images are built on the first `cjx run`.

`cjx install` prints the line below when `~/.local/bin` is not on your `PATH`. Add it to `~/.bashrc` or `~/.zshrc`:

```bash
export PATH="$HOME/.local/bin:$PATH"   # also append to ~/.bashrc or ~/.zshrc
```

### 3. Run an agent

```bash
cd ~/code/my-project
cjx run
```

cjx asks which agent to start, then brings it up. The answer is remembered, so
the next `cjx run` goes straight in, and `--select` brings the question back when
you want to switch. Running `cjx` with no arguments opens a menu that does the
same thing - see [Commands at a glance](#commands-at-a-glance).

The variables that name your terminal, such as `TERM_PROGRAM` and `COLORTERM`,
reach the sandbox, so an agent uses its colors and clickable links as it would
outside. Nothing else from your shell reaches a built-in agent.

### 4. Sign the agent in, once

Each agent signs in on its own first start, and the login lands in that mode's
home volume - every later run starts signed in.

**Claude Code** opens Anthropic's browser OAuth by itself and walks you through
it. `/login` inside Claude repeats it whenever you want.

**pi.dev** signs in with `/login`, which is also where you pick the provider. Its
browser callback cannot reach into the sandbox, so pi asks you to paste the final
redirect URL or the authorization code back into it. The sign-in link is long and
wraps over several lines. Ctrl+X copies it through your terminal's clipboard where
the terminal supports OSC 52. Otherwise copy it by hand and take out the spaces the
line breaks leave behind. The firewall lets the
subscriptions from OpenAI (ChatGPT), Anthropic (Claude) and GitHub Copilot
through. Another provider needs its hosts in the
[allowlist](#extend-the-allowlist).

**codex** signs in with a device code, since its browser callback cannot reach
into the sandbox either. Turn on device code login once in your ChatGPT security
settings, then start codex with `cjx run codex`, pick *Sign in with Device Code*,
open the link it prints in your browser and enter the code.

If device code login is not available to you, OpenAI's
[fallback](https://learn.chatgpt.com/docs/auth) is to sign in to codex on your
host and copy its `~/.codex/auth.json` over. A codex that keeps the login in your
OS keyring writes that file only with `cli_auth_credentials_store = "file"` in its
`config.toml`. `--add-dir` mounts your host `~/.codex` into the sandbox at the
same path, read-only:

```bash
cjx run codex --shell --add-dir ~/.codex
mkdir -p ~/.codex && cp /path/to/your/home/.codex/auth.json ~/.codex/
```

Inside the sandbox `~` is the agent's home, so the second line names your host
path in full. Type `exit` afterwards. From now on `cjx run codex` starts signed in.

That's it for getting started.

## Common run flags

| Flag | What it does |
|---|---|
| `--rw` | Mount your project read-write so the agent can edit files |
| `--shell` | Drop into bash inside the sandbox (handy to inspect the home volume) |
| `--no-firewall` | Disable the network sandbox; cjx asks you to confirm first |
| `--select` | Re-prompt for agent and mode instead of reusing your last selection |
| `--add-dir PATH` | Mount an extra host directory into the sandbox at the same path (repeatable), read-only unless `--rw` is set. For Claude it is also passed through as `--add-dir` so the agent works with it |

## Commands at a glance

Run `cjx` with no arguments on a terminal and it opens a menu. Pick a command,
tick its flags with space and run it with Enter. `run` asks for the agent first.
The `engine` line under the commands switches between the engines you have
installed, and cjx remembers the choice for later runs. Redirected or in CI,
`cjx` prints the command list instead.

| Command | When to use it |
|---|---|
| `cjx install` | Once - copies the binary to `~/.local/bin/cjx` and says so if that directory is not on your `PATH`. Images build lazily on first `cjx run`. |
| `cjx run [agent]` | Each time you want to start the agent. |
| `cjx update` | To pull newer base/firewall images and `--no-cache` rebuild every per-agent image you have built. |
| `cjx prune` | Remove per-agent images no agent maps to any more, then offer to remove home volumes nothing maps to either. |
| `cjx status` | Show whether base/firewall/per-agent images exist, list home-volumes and running sessions. |
| `cjx config` | Interactive menu for the allowlist and MCP servers, plus a view of the configuration cjx would load. |
| `cjx migrate --to <engine>` | Copy home volumes into the other engine so you keep your login when you switch; asks which ones. |
| `cjx export [volume]` | Write one agent home volume to a `.tar.gz` archive; with no argument it asks which volume. |
| `cjx import [archive]` | Restore a home volume from such an archive, asking first when the volume is already there. |
| `cjx uninstall` | First lets you tick which of cache-dir / volumes / config to delete as well, then removes session containers, networks, images and `~/.local/bin/cjx`. |

## Engines, volumes and archives

Which runtime cjx drives, what that means for your images and logins, and how to
carry a home volume to another engine, another machine or a backup.

### Engine selection

cjx uses Podman when it is installed and falls back to Docker. Override with
`--engine podman` / `--engine docker`, or set `CJX_ENGINE` in your shell.

Two Docker configurations are **refused**, not worked around:

- **rootless Docker** maps you to uid 0 inside the container and everything else
  into a subuid range, which is the reverse of what the sandbox needs.
- **`--userns-remap`** shifts every container uid into the `dockremap` range, so
  the agent cannot own the bind-mounted project.

Both print one line naming the reason and pointing at Podman.

**Images and volumes are per engine.** Podman and Docker keep separate stores, so
switching engines means a second set of images and a second set of home volumes.
`cjx update`, `cjx prune`, `cjx status` and `cjx uninstall` act only on the
engine they are run with, and each of them names that engine in its output.

**Switching in the menu holds.** Pick the `engine` line in the menu and cjx
remembers it: the next run uses that engine too, until you pick the other one
or name one with `--engine` or `CJX_ENGINE`, which both still win.

**A run brings its home along.** Starting an agent on an engine that has no home
volume for it yet, while the other engine does, copies that volume over first -
the same tar stream `cjx migrate` uses, for this one volume. So the agent is
signed in on both sides, whichever way you switch.

**`cjx migrate --to <engine>` carries your login across.** It lists the home
volumes of the engine that is not `--to` and copies the ticked ones into `--to`.
A volume the target already has is replaced after you confirm, and the source
volumes are never touched. From then on the two copies are independent, so a
login you renew under one engine reaches the other only with another `migrate`.

```bash
cjx migrate --to docker    # or: cjx migrate --to podman
```

**`cjx export` and `cjx import` move a volume through a file**, to another
machine or into a backup:

```bash
cjx export cjx-home-claude-subscription    # asks where to save cjx-home-claude-subscription.tar.gz
cjx import cjx-home-claude-subscription.tar.gz
```

Without arguments both ask, `export` for the volume, `import` for the archive.
`export` also asks for the directory to save into unless `--file` names the
archive. Redirected or in CI, it writes to the current directory rather than
asking.
`import` restores into the volume the archive is named after unless `--volume`
names another, and asks before it unpacks over an existing one. The archive's
files win, anything it does not carry stays. End a running session before
importing, since the agent has that volume mounted.

[CONTRIBUTING.md](CONTRIBUTING.md#editing-a-home-archive) shows how to change an
archive by hand before importing it.

## Customize

Everything user-facing lives in `~/.config/cjx/`:

```
~/.config/cjx/
|-- agents.local.toml       Optional: agents of your own, or more modes for a built-in one
|-- allowlist               Extra firewall hostnames beyond the agent defaults
\-- mcp-server/             Applied to agents with mcp = true
    |-- mcp.json            Optional: MCP servers (tools your agent can use)
    \-- Containerfile       Optional: extra container layer for those tools
```

`cjx config` edits the allowlist and `mcp.json` for you and validates what you type. A hostname with a port, a duplicate, or one that overlaps an entry Squid would see in the same session - your own or an agent built-in - is rejected before Squid ever sees it. `agents.local.toml` stays a hand-edited file.

cjx never overwrites these without asking. The agents themselves are built into cjx, so every release brings its changes along. `agents.local.toml` only adds to them. An `[[agent]]` with a new name is added whole, and for a built-in agent it takes `[[agent.mode]]` blocks only. The built-in modes stay as they are, so a mode of your own needs a name of its own and a home volume of its own. Install, hosts and limits of a built-in agent stay with the release, so an old copy of them can never hold back a fix.

### Add an agent

Add to `~/.config/cjx/agents.local.toml`. The block below is an illustrative template, not a working agent - replace the install URL and adjust env/allowlist for the real agent you want to add:

```toml
[[agent]]
name = "acme"                                               # used for image/volume names; must match [a-zA-Z0-9][a-zA-Z0-9_.-]*
display = "Acme Agent"                                      # human-readable label in the cjx run menu
binary = "acme"                                             # command run inside the container
install = "curl -fsSL https://example.invalid/install.sh | bash"  # replace with the agent's real installer
env_prefixes = ["ACME_"]                                    # host env vars with these prefixes are forwarded into the container
allow_hosts = ["api.example.invalid"]                       # firewall allowlist hosts (CONNECT-only, port 443)

  [[agent.mode]]
  name = "default"                                          # mode name (required)
  display = "Default"                                       # mode label
  volume = "acme-default"                                   # per-mode home volume (cjx-home-acme-default); required
```

The agent image is built lazily on the first `cjx run acme`. `cjx update` only rebuilds agents whose images already exist, so a never-run agent stays unbuilt until you run it.

### Add a mode to a built-in agent

Name the agent and give only its modes. The block below adds a second Claude Code login with a home volume of its own.

```toml
[[agent]]
name = "claude"

  [[agent.mode]]
  name = "Work"
  display = "Anthropic Subscription (work)"
  volume = "claude-work"
```

Keys cjx does not know are refused by name rather than ignored - a misspelled `allow_host` would otherwise leave you with a firewall that quietly blocks more than you meant.

### Extend the allowlist

Built-in hosts of the selected agent are merged in automatically. The `allowlist` file is for extras you actually need. Most setups need just the npm registry so `npx`-launched MCP servers can fetch their packages:

```
registry.npmjs.org
```

`*.example.com` matches subdomains and the apex. The firewall accepts `CONNECT` on port 443 only - no plain HTTP, no other ports.

### Add MCP servers

[MCP servers](https://modelcontextprotocol.io/) are tools your agent can call (file readers, browsers, shell wrappers ...). Drop a `mcp.json` into `~/.config/cjx/mcp-server/`:

```json
{
  "mcpServers": {
    "playwright": {
      "command": "npx",
      "args": ["@playwright/mcp", "--headless"]
    }
  }
}
```

The `mcp-server/` layer is opt-in per agent: set `mcp = true` on an agent (the built-in `claude` already does). Agents without it, such as pi.dev and codex, skip the layer entirely, so it's never built for them. For Claude Code the entrypoint adds the servers in `mcp.json` to `~/.claude.json` on every `cjx run` through `claude mcp add-json` (idempotent, preserves OAuth credentials). Another agent with `mcp = true` gets the container layer only. For browser-based MCP servers, also drop a `Containerfile` next to it that installs the runtime - see `examples/mcp-server/` for a ready-made Playwright + Chrome-DevTools setup. Copy both files into your `~/.config/cjx/mcp-server/` and run `cjx update`.

## Update and uninstall

`cjx update` pulls newer base images and rebuilds every agent image you have already built, without the layer cache, so each install step fetches the current agent release. Agents you have never run are skipped - no `cjx update` ever pre-bakes images for them. Each rebuild is announced as it starts, and the run closes with a summary naming what was rebuilt and what was skipped, so a long `--no-cache` run stays legible.

`mcp.json` edits don't need `cjx update` - the container entrypoint adds them to `~/.claude.json` on every `cjx run`. A server you delete from `mcp.json` stays in `~/.claude.json` until you remove it there with `claude mcp remove <name> --scope user`, from `cjx run --shell`.

`cjx uninstall` first offers a checklist of up to three things to delete as well, with nothing preselected:

- the cache directory - `~/.cache/cjx/`, on macOS `~/Library/Caches/cjx/` (firewall-session dirs, flock files and the remembered selection - fine to delete)
- the per-mode home volumes (your OAuth tokens, history, caches - leave them if you'll reinstall)
- the config directory `~/.config/cjx/` (your edits - leave it if you'll reinstall)

Then it removes every `cjx-firewall-*` and `cjx-session-*` container, every `cjx-net-*` network, every per-agent image (`cjx-agent-*`, `cjx-user-*`), the base image, the firewall image, what you ticked and the binary at `~/.local/bin/cjx`. Aborting the checklist removes nothing.

## How it works

Each `cjx run` starts the agent in a container built from four image layers, behind a Squid container of its own on a private network with no other way out. [CONTRIBUTING.md](CONTRIBUTING.md) explains the layers, the firewall and the proxy handling, and how to work on cjx itself.

## Troubleshooting

**`cjx run` fails with Squid log output.** Squid rejected something in your allowlist. The last 50 lines of its log are shown - fix the listed line in `~/.config/cjx/agents.local.toml` or `~/.config/cjx/allowlist` and run `cjx run` again.

**`Error: no podman machine is running`.** macOS only. The Podman VM is stopped; start it with `podman machine start`. cjx checks this before it touches anything else, so you get this line instead of a raw podman error further in. A macOS setup with no local machine at all - a remote `CONTAINER_HOST`, for instance - is left alone.

**`Error: docker daemon not reachable`.** The Docker CLI is installed but the
daemon is not answering - start it (`sudo systemctl start docker`, or Docker
Desktop) and check your user is in the `docker` group.

**`rootless Docker maps you to root inside the container`** or **`docker with
userns-remap cannot give the agent your uid`.** cjx refuses these two Docker
setups rather than half-sandboxing under them. Install Podman, or run cjx against
a plain rootful daemon.

**`Codex Linux sandbox uses bubblewrap and needs access to create user
namespaces`.** Docker only. codex wraps every command it runs in a bubblewrap
sandbox, which needs a user namespace, and Docker's default seccomp profile
denies one to a container without `CAP_SYS_ADMIN`. `needs_userns = true` in the
agent block takes the seccomp filter off that agent's runs; `--cap-drop=ALL`,
no-new-privileges, the read-only rootfs and the allow-listed proxy stay. The
built-in codex sets it. Podman gives a rootless container its own user namespace
and ignores the key.

**Clock drift inside the container.** Podman on macOS runs a VM whose clock stops
while the Mac sleeps, and TLS then fails with a certificate that is "not yet
valid". cjx re-syncs it on every run. Docker Desktop has the same failure mode but
no equivalent hook - restart Docker Desktop if you see it.

**Container builds fail with network errors.** Image builds run on your host network. Under Podman cjx passes `--http-proxy=false` to the build, so host proxy env vars are *not* forwarded into it - set proxies inside the build itself (e.g., `ENV http_proxy=...` in the Containerfile) if your build needs them. Docker has no such switch and takes its build proxies from `~/.docker/config.json`.

**Behind a corporate HTTP proxy at runtime.** cjx auto-detects `https_proxy` / `http_proxy` from the shell that launches it and, if the proxy is reachable (1s TCP probe), wires it into the firewall as a Squid `cache_peer parent` so the sandbox can reach the allow-listed hosts through it. When the same env vars are set but unreachable (e.g., a `.zshrc` entry while you're working from home), the probe falls back to direct out.

**Want to inspect what's inside the home volume?** `cjx run --shell` drops you into a bash inside the sandbox, mounted with the same home volume.

## License

Apache License 2.0, see [LICENSE](LICENSE) and [NOTICE](NOTICE).
