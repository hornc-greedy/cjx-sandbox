package main

import (
	"strings"
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
)

func TestConfigSummary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	agents, err := config.LoadAgents()
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	summary := configSummary(
		agents,
		[]string{"registry.npmjs.org"},
		map[string]config.MCPServer{"playwright": {Command: "npx"}},
	)
	for _, want := range []string{
		"Config dir   ", "Cache dir    ",
		"Agents       built in, plus ", "agents.local.toml", "(edit by hand)",
		// nothing has been seeded in this temp HOME, and the summary must say so
		"(not created yet)",
		"claude: ", "pi.dev: ",
		"Allowlist    1 in ", "MCP servers  1 in ",
		"no extra image layer; add ", "Selection    none saved yet",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q:\n%s", want, summary)
		}
	}
}

// Every agent's built-in hosts have to reach the allowlist validator, or a user
// entry could shadow one and Squid would refuse the whole list.
func TestBuiltinHosts(t *testing.T) {
	agents := []config.Agent{{
		Name:       "claude",
		AllowHosts: []string{"api.anthropic.com"},
		Modes: []config.Mode{
			{Name: "work", AllowHosts: []string{"work.example.com"}},
			{Name: "subscription"},
		},
	}}
	got := builtinHosts(agents)
	want := []string{"api.anthropic.com", "work.example.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("host %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
