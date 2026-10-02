package firewall

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
)

func TestSquidEntry(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"# comment", ""},
		{"  # indented", ""},
		{"example.com", "example.com"},
		{"*.example.com", ".example.com"},
		{"  api.example.com  ", "api.example.com"},
		{"host:8080", ""},
		{"::1", ""},
	}
	for _, c := range cases {
		if got := squidEntry(c.in); got != c.want {
			t.Errorf("squidEntry(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDetectUpstreamProxy(t *testing.T) {
	for _, name := range []string{"https_proxy", "http_proxy", "HTTPS_PROXY", "HTTP_PROXY"} {
		t.Setenv(name, "")
	}

	if got := DetectUpstreamProxy(); got != "" {
		t.Errorf("no env set: got %q, want empty", got)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	t.Setenv("http_proxy", "http://"+addr)
	if got := DetectUpstreamProxy(); got != addr {
		t.Errorf("reachable: got %q, want %q", got, addr)
	}

	t.Setenv("https_proxy", "http://"+addr)
	if got := DetectUpstreamProxy(); got != addr {
		t.Errorf("https_proxy reachable: got %q, want %q", got, addr)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := DetectUpstreamProxy(); got != "" {
		t.Errorf("unreachable: got %q, want empty", got)
	}
}

func TestWriteSessionAllowlist(t *testing.T) {
	t.Run("hosts only, strips comments/blanks/ports, converts wildcard", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", t.TempDir())

		hosts := []string{"*.foo.com", "bar.com", "", "# skipped", "host:8080"}
		if err := writeSessionAllowlist(dir, hosts); err != nil {
			t.Fatalf("writeSessionAllowlist: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "allowlist"))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		want := ".foo.com\nbar.com\n"
		if string(got) != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("user file is merged and deduplicated", func(t *testing.T) {
		dir := t.TempDir()
		home := t.TempDir()
		t.Setenv("HOME", home)

		configDir := filepath.Join(home, ".config", "cjx")
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		userContent := []byte("# user list\nextra.com\nbar.com\n")
		userPath := filepath.Join(configDir, "allowlist")
		if err := os.WriteFile(userPath, userContent, 0o600); err != nil {
			t.Fatalf("write user: %v", err)
		}

		if err := writeSessionAllowlist(dir, []string{"*.foo.com", "bar.com"}); err != nil {
			t.Fatalf("writeSessionAllowlist: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "allowlist"))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		want := ".foo.com\nbar.com\nextra.com\n"
		if string(got) != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// The session state lives on the host, where both engines see it, while the
// containers it names live in one store. Without the engine in the key, a run
// under one engine would drop the other's state and orphan its containers.
func TestSessionNamesCarryTheEngine(t *testing.T) {
	e, err := engine.Detect(t.Context(), engine.Podman)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	a := config.Agent{Name: "claude"}
	m := config.Mode{Name: "subscription"}
	for _, got := range []string{
		sessionContainerName(e, a, m, 42),
		AgentContainerName(e, a, m, 42),
		sessionNetworkName(e, a, m, 42),
		sessionDir(e, a, m, 42),
	} {
		if !strings.Contains(got, engine.Podman+"-claude-subscription-42") {
			t.Errorf("%q does not carry engine, agent, mode and pid", got)
		}
	}
}

// A directory belonging to the other engine must survive: this engine cannot
// remove the containers it names, and deleting it would lose the only record of
// them.
func TestPruneDeadSessionsSkipsTheOtherEngine(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := filepath.Join(config.CacheDir(), "firewall")
	foreign := filepath.Join(root, engine.Docker+"-claude-subscription-42")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(root, engine.Podman+"-claude-subscription-43")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	e, err := engine.Detect(t.Context(), engine.Podman)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	pruneDeadSessions(e)
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("the other engine's session was reclaimed: %v", err)
	}
	if _, err := os.Stat(mine); err == nil {
		t.Error("this engine's own stale session was left behind")
	}
}
