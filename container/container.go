// Package container holds the embedded build contexts for the sandbox
// images and the default agents config.
package container

import "embed"

// BaseFS is the cjx-base image build context: a Debian image with the
// shared system tools, agent user, and entrypoint. Per-agent layers go
// on top via FROM cjx-base:latest.
//
//go:embed Containerfile entrypoint.sh managed-settings.json
var BaseFS embed.FS

// FirewallFS is the firewall-image build context. Files are under firewall/.
//
//go:embed firewall
var FirewallFS embed.FS

// DefaultAgents is the built-in agent list. cjx reads it directly and adds
// what ~/.config/cjx/agents.local.toml defines.
//
//go:embed default-agents.toml
var DefaultAgents []byte
