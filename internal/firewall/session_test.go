package firewall

import (
	"strings"
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine/enginetest"
)

func TestMain(m *testing.M) { enginetest.Main(m) }

// Start provisions the private network, the Squid container and the state
// directory, and waits for Squid to say it is listening.
func TestStart(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	c := enginetest.Fake(t, "network create=>ok"+
		";run -d=>ok"+
		";logs=>Accepting HTTP connections"+
		";inspect --format=>10.89.0.7")
	a, m := config.Agent{Name: "claude"}, config.Mode{Name: "sub"}
	s, err := Start(t.Context(), podmanEngine(t), a, m, []string{"api.anthropic.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer Stop(s)

	if s.ProxyURL() != "http://10.89.0.7:8080" {
		t.Errorf("proxy address: %q", s.ProxyURL())
	}
	if !strings.HasPrefix(s.Network(), "cjx-net-") {
		t.Errorf("network name: %q", s.Network())
	}
	if !c.Got("network create --internal --disable-dns cjx-net-") {
		t.Errorf("session network not sealed: %v", c.All())
	}
	for _, call := range c.All() {
		if strings.HasPrefix(call, "run -d") && !strings.Contains(call, "--network podman") {
			t.Errorf("Squid cannot reach the upstream: %s", call)
		}
	}
}

// A Squid that dies during startup must be reported with its log, not waited
// out until the timeout.
func TestStartReportsAnExitedFirewall(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	enginetest.Fake(t, "network create=>ok"+
		";run -d=>ok"+
		";logs=>squid: ERROR: bad allowlist"+
		";ps -q=>|"+
		";network exists=>ok;network rm=>ok;container exists=>|")
	a, m := config.Agent{Name: "claude"}, config.Mode{Name: "sub"}
	_, err := Start(t.Context(), podmanEngine(t), a, m, nil)
	if err == nil {
		t.Fatal("a firewall that exited was accepted")
	}
	if !strings.Contains(err.Error(), "bad allowlist") {
		t.Errorf("the log is missing from the error: %v", err)
	}
}

func podmanEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.Detect(t.Context(), engine.Podman)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
