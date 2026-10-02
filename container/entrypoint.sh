#!/bin/bash
set -euo pipefail

# Bootstrap home from /etc/skel on first run, using a sentinel so that
# removing ~/.bashrc later doesn't trigger reseeding
sentinel="$HOME/.cjx-bootstrapped"
if [ ! -e "$sentinel" ] && [ -d /etc/skel ]; then
    if ! cp -rT /etc/skel "$HOME"; then
        echo "warning: failed to seed home from /etc/skel" >&2
    fi
    touch "$sentinel"
fi

# Mirror image-stashed agents into the volume-backed ~/.local so cjx update
# propagates new versions; cp -u leaves user state files untouched, and
# --remove-destination unlinks first so a binary still running in a concurrent
# same-mode session can't trigger ETXTBSY ("Text file busy").
if [ -d /opt/cjx/stash/.local ]; then
    mkdir -p "$HOME/.local"
    cp -ruT --remove-destination /opt/cjx/stash/.local "$HOME/.local"
fi

# pi's link in ~/.local/bin leads to ~/.pi/agent/bin, whose launcher reads the
# install beside it. Both stay in the image, the rest of ~/.pi/agent is pi's own.
if [ -d /opt/cjx/stash/.pi/agent ]; then
    mkdir -p "$HOME/.pi/agent"
    ln -sfn /opt/cjx/stash/.pi/agent/bin "$HOME/.pi/agent/bin"
    ln -sfn /opt/cjx/stash/.pi/agent/install "$HOME/.pi/agent/install"
fi

# Merge user-supplied MCP servers into ~/.claude.json on each run, so
# host edits propagate without overwriting OAuth/history fields.
# Claude-specific: other agents (pi, ...) have their own MCP schemas.
if [ "${CJX_AGENT:-}" = "claude" ] && [ -f /tmp/cjx-mcp.json ]; then
    names=$(jq -r '.mcpServers // {} | keys[]' /tmp/cjx-mcp.json 2>/dev/null || true)
    for name in $names; do
        server=$(jq -c ".mcpServers[\"$name\"]" /tmp/cjx-mcp.json)
        claude mcp remove "$name" --scope user >/dev/null 2>&1 || true
        claude mcp add-json --scope user "$name" "$server" >/dev/null 2>&1 \
            || echo "warning: failed to add MCP server $name" >&2
    done
fi

exec "$@"
