package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/firewall"
	"github.com/hornc-greedy/cjx-sandbox/internal/image"
)

// agentNameClaude is special-cased: --add-dir is Claude-specific.
const agentNameClaude = "claude"

const (
	usrDir       = "/usr"
	workspaceDir = "/workspace"
)

// An --add-dir target may not equal one of these or be a parent of one.
// A child path is allowed.
//
//nolint:gochecknoglobals // immutable lookup table
var reservedMountTargets = []string{
	usrDir, "/bin", "/sbin", "/lib", "/lib64", "/etc", "/var",
	"/proc", "/sys", "/dev", "/run", "/boot", "/root", "/opt",
	"/home/agent", workspaceDir, "/tmp",
}

// validateAddDirTarget rejects an absolute path that would collide with a
// container mount point. abs must be cleaned and absolute.
func validateAddDirTarget(abs string) error {
	if abs == "/" {
		return errors.New("refusing to mount the filesystem root")
	}
	for _, r := range reservedMountTargets {
		if abs == r || strings.HasPrefix(r, abs+"/") {
			return fmt.Errorf("would shadow the container path %q", r)
		}
	}
	return nil
}

func resolveAddDirs(dirs []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, d := range dirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, fmt.Errorf("--add-dir %q: %w", d, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("--add-dir %q: %w", d, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("--add-dir %q: not a directory", d)
		}
		if err := validateAddDirTarget(abs); err != nil {
			return nil, fmt.Errorf("--add-dir %q: %w", d, err)
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	return out, nil
}

func allowlistFor(a config.Agent, m config.Mode) []string {
	hosts := slices.Clone(a.AllowHosts)
	return append(hosts, m.AllowHosts...)
}

// hostEnv is the part of the host environment the agent's env_prefixes forward.
func hostEnv(a config.Agent) []string {
	var out []string
	for _, kv := range os.Environ() {
		if slices.ContainsFunc(a.EnvPrefixes, func(p string) bool {
			return strings.HasPrefix(kv, p)
		}) {
			out = append(out, kv)
		}
	}
	return out
}

// A sandboxRun is one agent invocation. The paths and names are all strings, so
// a struct literal is what keeps them from being swapped at the call site.
type sandboxRun struct {
	runOpts
	Agent    config.Agent
	Mode     config.Mode
	Workdir  string // host directory mounted at /workspace
	MCPPath  string // host mcp.json, empty when the agent has none
	Network  string // session network, empty without a firewall
	ProxyURL string // proxy the container must use, empty for direct
}

func buildRunArgs(e *engine.Engine, r sandboxRun) []string {
	mountMode := "ro"
	if r.rw {
		mountMode = "rw"
	}
	a, m := r.Agent, r.Mode
	limits := config.Resolve(a, m)
	args := []string{
		"run", "--rm", "-it",
		"--name", firewall.AgentContainerName(e, a, m, os.Getpid()),
		"--cpus=" + strconv.Itoa(limits.CPUs),
		"--memory=" + limits.Memory,
		"--pids-limit=" + strconv.Itoa(limits.PIDs),
		"--shm-size=512m",
		"--hostname=cjx",
		"--tmpfs", "/tmp:size=512m",
		"-v", config.VolumeName(a, m) + ":/home/agent",
		"-v", e.BindMount(r.Workdir, workspaceDir, mountMode),
		"-w", workspaceDir,
		"-e", "CJX_AGENT=" + a.Name,
	}
	args = append(args, e.IdentityArgs()...)
	args = append(args, e.SecurityArgs()...)
	if a.NeedsUserns {
		args = append(args, e.UserNamespaceArgs()...)
	}
	// Never let the runtime inject proxy variables of its own; cjx points them at
	// the session's Squid below, or blanks them.
	args = append(args, e.NoProxyInjectArgs()...)
	if r.MCPPath != "" {
		args = append(args, "-v", e.BindMount(r.MCPPath, "/tmp/cjx-mcp.json", "ro"))
	}
	// Shared relabel: an --add-dir path may be mounted by more than one session.
	for _, d := range r.addDirs {
		args = append(args, "-v", e.SharedBindMount(d, d, mountMode))
	}
	if r.Network != "" {
		args = append(args, "--network", r.Network)
	}
	args = append(args, e.ProxyArgs(r.ProxyURL)...)
	for _, kv := range hostEnv(a) {
		args = append(args, "-e", kv)
	}
	args = append(args, image.UserTag(a))
	if r.shell {
		args = append(args, "/bin/bash")
	} else {
		args = append(args, a.Binary)
		// --add-dir is Claude-specific; other agents just get the mount.
		if a.Name == agentNameClaude {
			for _, d := range r.addDirs {
				args = append(args, "--add-dir", d)
			}
		}
	}
	return args
}
