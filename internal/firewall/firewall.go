// Package firewall runs the per-session Squid proxy that limits where the agent
// can reach on the network.
package firewall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/image"

	"github.com/gofrs/flock"
)

// A Session owns one running firewall: its container, network, allowlist
// directory and the lock that marks the owning cjx process as alive.
type Session struct {
	eng       *engine.Engine
	container string
	network   string
	proxyURL  string
	dir       string
	lock      *flock.Flock
}

// sessionKey tags a cjx instance with engine/agent/mode/pid. The engine belongs
// in it because the cache directory is on the host, visible to both, while the
// containers it names are in one store.
func sessionKey(e *engine.Engine, a config.Agent, m config.Mode, pid int) string {
	return fmt.Sprintf("%s-%s-%s-%d", e.Name(), a.Name, m.Name, pid)
}

func sessionContainerName(e *engine.Engine, a config.Agent, m config.Mode, pid int) string {
	return "cjx-firewall-" + sessionKey(e, a, m, pid)
}

// AgentContainerName names the agent container so status can count it.
func AgentContainerName(e *engine.Engine, a config.Agent, m config.Mode, pid int) string {
	return "cjx-session-" + sessionKey(e, a, m, pid)
}

func sessionNetworkName(e *engine.Engine, a config.Agent, m config.Mode, pid int) string {
	return "cjx-net-" + sessionKey(e, a, m, pid)
}

func sessionDir(e *engine.Engine, a config.Agent, m config.Mode, pid int) string {
	return filepath.Join(config.CacheDir(), "firewall", sessionKey(e, a, m, pid))
}

// Start provisions the private network and the Squid container for one run.
func Start(
	ctx context.Context, e *engine.Engine, a config.Agent, m config.Mode, hosts []string,
) (*Session, error) {
	pid := os.Getpid()
	pruneDeadSessions(e)

	s := &Session{
		eng:       e,
		container: sessionContainerName(e, a, m, pid),
		network:   sessionNetworkName(e, a, m, pid),
		dir:       sessionDir(e, a, m, pid),
	}

	// 0o755: bind-mounted, needs world-traverse.
	if err := os.MkdirAll(s.dir, 0o755); err != nil { //nolint:gosec
		return nil, err
	}
	if err := writeSessionAllowlist(s.dir, hosts); err != nil {
		return nil, err
	}

	s.lock = flock.New(filepath.Join(s.dir, "owner.lock"))
	locked, err := s.lock.TryLock()
	if err != nil || !locked {
		s.cleanup()
		return nil, fmt.Errorf("session %s already locked: %w", s.container, err)
	}

	// Engines echo the network they just made; that is plumbing, not output.
	if _, err := e.Output(ctx, e.NetworkCreateArgs(s.network)...); err != nil {
		s.cleanup()
		return nil, err
	}

	if err := startFirewallContainer(ctx, s); err != nil {
		s.cleanup()
		return nil, err
	}

	ip, err := s.eng.InspectIP(ctx, s.container, s.network)
	if err != nil {
		s.cleanup()
		return nil, fmt.Errorf("inspect firewall IP: %w", err)
	}
	s.proxyURL = "http://" + net.JoinHostPort(ip, "8080")

	return s, nil
}

// Stop tears the session down. Idempotent, and independent of the run context so
// a cancelled run still cleans up.
func Stop(s *Session) {
	if s == nil {
		return
	}
	s.cleanup()
}

// Best-effort: a failure here must not mask the run's own error.
func (s *Session) cleanup() {
	_ = s.lock.Close()
	s.eng.Remove(engine.Container, s.container)
	s.eng.Remove(engine.Network, s.network)
	_ = os.RemoveAll(s.dir)
}

// pruneDeadSessions removes the containers, networks and state directories of
// sessions whose owner died. A lock cjx can take means the owner is gone.
func pruneDeadSessions(e *engine.Engine) {
	root := filepath.Join(config.CacheDir(), "firewall")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		return
	}
	for _, ent := range entries {
		// Another engine's leftovers: this one cannot remove its containers, and
		// dropping the directory would leave them with nothing to find them by.
		if !ent.IsDir() || !strings.HasPrefix(ent.Name(), e.Name()+"-") {
			continue
		}
		dir := filepath.Join(root, ent.Name())
		l := flock.New(filepath.Join(dir, "owner.lock"))
		locked, lockErr := l.TryLock()
		if lockErr != nil || !locked {
			_ = l.Close()
			continue
		}
		_ = l.Close()
		e.Remove(engine.Container, "cjx-firewall-"+ent.Name())
		e.Remove(engine.Network, "cjx-net-"+ent.Name())
		_ = os.RemoveAll(dir)
	}
}

// writeSessionAllowlist writes the dedup'd Squid dstdomain list (*.x -> .x).
func writeSessionAllowlist(dir string, hosts []string) error {
	seen := map[string]bool{}
	var lines []string
	add := func(raw []byte) {
		for _, ln := range strings.Split(string(raw), "\n") {
			entry := squidEntry(ln)
			if entry == "" || seen[entry] {
				continue
			}
			seen[entry] = true
			lines = append(lines, entry)
		}
	}
	add([]byte(strings.Join(hosts, "\n")))
	if user, err := os.ReadFile(config.AllowlistPath()); err == nil {
		add(user)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	body := strings.Join(lines, "\n") + "\n"
	// 0o644: bind-mounted to firewall UID, needs world-readable.
	return os.WriteFile(filepath.Join(dir, "allowlist"), []byte(body), 0o644) //nolint:gosec
}

// squidEntry converts a line to Squid's dstdomain format; "" for non-host lines.
func squidEntry(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	if strings.ContainsRune(line, ':') {
		return ""
	}
	if rest, ok := strings.CutPrefix(line, "*."); ok {
		return "." + rest
	}
	return line
}

// DetectUpstreamProxy returns "host:port" from http(s)_proxy, "" if unset or
// unreachable within a second. The probe rejects a stale .zshrc entry.
func DetectUpstreamProxy() string {
	var raw string
	for _, name := range []string{"https_proxy", "http_proxy", "HTTPS_PROXY", "HTTP_PROXY"} {
		if v := os.Getenv(name); v != "" {
			raw = v
			break
		}
	}
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = "8080"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	hostPort := net.JoinHostPort(u.Hostname(), port)
	conn, err := net.DialTimeout("tcp", hostPort, 1*time.Second)
	if err != nil {
		return ""
	}
	_ = conn.Close()
	return hostPort
}

// Blocks until Squid reports it is accepting, so the agent never starts too early.
func startFirewallContainer(ctx context.Context, s *Session) error {
	args := []string{
		"--tmpfs", "/var/spool/squid",
		"--tmpfs", "/var/log/squid",
		"--tmpfs", "/run:rw,mode=1777",
		"--memory=256m", "--pids-limit=64",
		"-v", s.eng.BindMount(s.dir, "/etc/cjx", "ro"),
	}
	args = append(args, s.eng.SecurityArgs()...)
	args = append(args, s.eng.NoProxyInjectArgs()...)
	if up := DetectUpstreamProxy(); up != "" {
		args = append(args, "-e", "CJX_UPSTREAM_PROXY="+up)
	}
	args = append(args, image.Firewall)
	// The outbound network so Squid reaches the upstream, the session-private one
	// so the agent talks to this Squid and to nothing else.
	networks := []string{s.eng.DefaultNetwork(), s.network}
	if err := s.eng.RunDetached(ctx, s.container, networks, args); err != nil {
		return err
	}

	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for waitCtx.Err() == nil {
		out := s.eng.Logs(waitCtx, s.container, 50)
		if bytes.Contains(out, []byte("Accepting HTTP")) {
			return nil
		}
		if !s.eng.IsRunning(s.container) {
			return fmt.Errorf("firewall container exited:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("firewall not ready within timeout:\n%s", s.eng.Logs(ctx, s.container, 50))
}

// ProxyURL is the address the agent container must send its traffic to.
func (s *Session) ProxyURL() string { return s.proxyURL }

// Network is the private network the agent container joins to reach the proxy.
func (s *Session) Network() string { return s.network }
