# Contributing to cjx

How cjx is put together and how to work on it. Using cjx is covered in the [README](README.md).

## Development

Lint and file hygiene run as [pre-commit](https://pre-commit.com) hooks: `golangci-lint` (it reads `.golangci.yml`), shellcheck on the container scripts, gitleaks, and the YAML/JSON/TOML and whitespace checks. CI runs the same set as its own `pre-commit` job; run `pre-commit install` once after cloning to get them on each commit instead of in CI.

Pushing a tag `v*` runs the Release workflow, which builds the archives with GoReleaser and publishes them on the GitHub releases page.

## Source layout

```
cmd/cjx/            one file per command (main, install, run, config, migrate, export, import, update, prune, status, uninstall) plus menu.go, the menu bare `cjx` opens, and sandbox.go, which assembles the agent container invocation
internal/config/    paths, built-in and local agents, allowlist, remembered selection
internal/engine/    the container runtime cjx drives
internal/image/     the four image layers and their drift hashes
internal/firewall/  per-session Squid, network, allowlist file
internal/ui/        terminal output and prompts
container/          container.go (//go:embed), Containerfile, entrypoint.sh, managed-settings.json, default-agents.toml, firewall/{Containerfile,entrypoint.sh,squid.conf}
examples/           Drop-in user-config samples (mcp-server/Containerfile + mcp.json)
```

`container` holds only `//go:embed` declarations; everything under `internal/` is
private to this module by the Go toolchain's own rule.

## How it works

### Image layering

Four image kinds, all `localhost/`-tagged (no registry pull):

| Image | Built from | Rebuild trigger |
|---|---|---|
| `cjx-base:latest` | `container/Containerfile` - Debian + bubblewrap/curl/fd/git/jq/less/ripgrep + Node.js 26 (NodeSource) + agent user (uid 1000) + Claude Code managed-settings at `/etc/claude-code/managed-settings.json` | Drift-Hash mismatch (embedded build context changed); `cjx update` also pulls a newer Debian |
| `cjx-firewall:latest` | `container/firewall/Containerfile` - Debian + Squid | Drift-Hash mismatch (embedded build context changed); `cjx update` also pulls a newer Debian |
| `cjx-agent-<name>:latest` | Dynamically-generated Containerfile (FROM `cjx-base` + apt packages + agent install) | Drift-Hash mismatch (any of `Agent.Name`, `Binary`, `Install`, `Packages`, generated Containerfile, base-digest changed) |
| `cjx-user-<name>:latest` | Tag-alias of `cjx-agent-<name>` unless the agent sets `mcp = true` and a `mcp-server/Containerfile` exists; then FROM `cjx-agent-<name>` wrapped with that Containerfile | Drift-Hash over the build-context tree + agent-image digest |

Drift hashes are stamped on as the OCI label `cjx.drift-hash`. Each lazy `cjx run` reads the label and rebuilds if it does not match the freshly computed hash.

Build contexts (`container/Containerfile`, `container/entrypoint.sh`, `container/managed-settings.json`, `container/firewall/*`) and the built-in agent list (`container/default-agents.toml`) are baked into the cjx binary via `//go:embed`. The binary is self-contained - no separate build assets to ship. Because the drift hash covers that embedded context, installing a cjx release whose image sources changed rebuilds the base and firewall images on the next `cjx run`; you do not have to remember `cjx update`.

### What cjx update builds

`cjx update` does, in order:

1. `image.BuildBase` with the engine's pull flag (`--pull=newer` under Podman, `--pull` under Docker) - rebuild `cjx-base`, layer cache OK.
2. `image.BuildFirewall` with the same flag - rebuild `cjx-firewall`, layer cache OK.
3. For every agent image you have already built: `image.BuildAgent --no-cache` then `image.BuildUser --no-cache`, both with the pull flag. `--no-cache` because the install step pulls fresh installer versions that the layer cache would freeze.
4. Remove the dangling images those rebuilds leave behind.

### Per-session firewall

Every `cjx run` provisions its own Squid container and private network:

- Container: `cjx-firewall-<engine>-<agent>-<mode>-<pid>`
- Network: `cjx-net-<engine>-<agent>-<mode>-<pid>` (`--internal`, L2-isolated; Podman also gets `--disable-dns`, which Docker has no equivalent for - the network has no way out either way)
- Cache dir: `~/.cache/cjx/firewall/<engine>-<agent>-<mode>-<pid>/` (`~/Library/Caches/cjx/...` on macOS) with `owner.lock` (kernel-flock) and `allowlist` (Squid `dstdomain`)

The agent container joins the per-session network, and Squid joins both the per-session network and the default outbound one (`podman` or `bridge`) so it can reach the upstream. Podman attaches both at `run`; Docker takes one there and gets the second through `network connect`. `HTTP_PROXY` / `HTTPS_PROXY` env vars inside the agent container point at Squid by IP (`http://<inspected-IP>:8080`).

When `cjx run` exits, `defer firewall.Stop` removes the container, the network and the session dir. The engine is part of the name because the cache directory sits on the host, where both engines can see it, while the container and network it names live in one engine's store - a sweep run under the other engine skips it. If the process is `kill -9`'d, the kernel releases the flock and the next `cjx run` on the same engine sweeps the orphan dir (`pruneDeadSessions`).

### Upstream HTTP proxy

If `https_proxy` / `http_proxy` are set on the host and reachable within 1s (TCP probe), cjx passes the `host:port` into the Squid container via `CJX_UPSTREAM_PROXY`. The firewall entrypoint writes a runtime config that adds `cache_peer <host> parent <port> 0 no-query default` + `never_direct allow all` and strips the public `dns_nameservers` line (corp networks usually block 1.1.1.1/8.8.8.8).

When the env vars are set but unreachable (e.g. a `.zshrc` entry while working from home), the probe falls back to direct out.

### How your uid reaches the container

The agent has to read and write the bind-mounted project as you. Rootless Podman
gets there with `--userns keep-id:uid=1000,gid=1000`; rootful Docker already runs
containers with host uids, so cjx passes `--user <your uid>:<your gid>` instead.
Under Docker, if your uid or gid is not 1000, a home volume the agent cannot write
is handed over with a one-off `chown`, because the engine seeds it from the image
with owner 1000. A volume the agent can already write is left alone - that is
where its login lives. Podman's keep-id needs no chown.

### Editing a home archive

A home archive from `cjx export` is a plain gzipped tar, so you can unpack it,
change what you like, and pack it again - on Linux and macOS alike:

```bash
mkdir home && tar -xzf cjx-home-claude-subscription.tar.gz -C home
# edit under home/
cd home && COPYFILE_DISABLE=1 tar -czf ../cjx-home-claude-subscription.tar.gz .
```

Two details decide whether the result imports. Pack **from inside** the unpacked
directory, with the trailing `.`: the entries have to be relative to the home
root, and `tar -czf archive.tar.gz home` would put everything under
`/home/agent/home/`. And keep the file named after its volume, or name the volume
yourself with `cjx import archive.tar.gz --volume cjx-home-claude-subscription`.
`COPYFILE_DISABLE=1` is the macOS part: without it bsdtar packs its `._*`
attribute files, which then land in the volume.
