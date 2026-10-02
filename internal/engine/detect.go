package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/hornc-greedy/cjx-sandbox/internal/ui"
)

// Podman and Docker are the runtimes cjx can drive.
const (
	Podman = "podman"
	Docker = "docker"
)

// Detect returns the engine to drive. name may be empty, "podman" or "docker".
// Empty takes CJX_ENGINE, then prefers Podman and falls back to Docker. An
// engine that is installed but configured in a way cjx cannot sandbox under is
// an error, not a fallback.
func Detect(ctx context.Context, name string) (*Engine, error) {
	if name == "" {
		name = os.Getenv("CJX_ENGINE")
	}
	switch name {
	case Podman:
		return podman(), nil
	case Docker:
		return newDocker(ctx)
	case "":
	default:
		return nil, unknownEngine(name)
	}
	found := Installed()
	if len(found) == 0 {
		return nil, errors.New("no container engine found; install podman or docker")
	}
	if found[0] == Podman {
		return podman(), nil
	}
	return newDocker(ctx)
}

// Installed lists the runtimes whose CLI is on PATH, most preferred first. When
// nothing names an engine, Detect drives the head of this list.
func Installed() []string {
	found := make([]string, 0, 2)
	for _, name := range []string{Podman, Docker} {
		if IsInstalled(name) {
			found = append(found, name)
		}
	}
	return found
}

// newDocker reads the daemon's own report of how it isolates containers, which
// decides both the uid mapping and whether mounts need SELinux labels.
func newDocker(ctx context.Context) (*Engine, error) {
	e := docker()
	if _, err := exec.LookPath(e.bin); err != nil {
		return nil, fmt.Errorf("%s not found in PATH; install from %s", e.bin, e.installURL)
	}
	out, err := exec.CommandContext(ctx, e.bin, "info",
		"--format", "{{range .SecurityOptions}}{{println .}}{{end}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker daemon not reachable: %w", err)
	}
	sec := parseSecurityOptions(out)
	if sec.rootless {
		return nil, errors.New(
			"rootless Docker maps you to root inside the container; use Podman instead")
	}
	if sec.usernsRemap {
		return nil, errors.New(
			"docker with userns-remap cannot give the agent your uid; use Podman instead")
	}
	e.selinuxLabel = sec.selinux
	e.identity = []string{"--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())}
	warnRootfulDocker()
	return e, nil
}

// Membership of the docker group is root without a password prompt: -v /:/host
// reads and writes anything on the machine. cjx cannot take that away, only
// name it.
func warnRootfulDocker() {
	if os.Getuid() == 0 {
		return
	}
	ui.Warnf("note: the Docker daemon runs as root, so anyone who can reach it can\n")
	ui.Warnf("      escalate to root on this host. cjx sandboxes the agent, not the\n")
	ui.Warnf("      daemon. Podman rootless does not have this property.\n")
}

// securityOptions is what a daemon reports about its own isolation.
type securityOptions struct {
	rootless    bool
	usernsRemap bool
	selinux     bool
}

// One "name=..." entry per line, some with further comma-separated attributes
// (name=seccomp,profile=builtin) that carry no isolation of their own.
func parseSecurityOptions(out []byte) securityOptions {
	var s securityOptions
	for _, line := range strings.Split(string(out), "\n") {
		switch strings.TrimSpace(strings.Split(line, ",")[0]) {
		case "name=rootless":
			s.rootless = true
		case "name=userns":
			s.usernsRemap = true
		case "name=selinux":
			s.selinux = true
		}
	}
	return s
}

// Other names the engine that is not name, and rejects a name cjx cannot drive.
func Other(name string) (string, error) {
	switch name {
	case Podman:
		return Docker, nil
	case Docker:
		return Podman, nil
	}
	return "", unknownEngine(name)
}

// IsInstalled reports whether an engine's CLI is on PATH. Cheap enough to ask
// before a probe whose failure would name a remedy the user does not want.
func IsInstalled(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func unknownEngine(name string) error {
	return fmt.Errorf("unknown engine %q; use %q or %q", name, Podman, Docker)
}
