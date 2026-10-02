package config

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestLoadAgentsEmbeddedDefault(t *testing.T) {
	// Empty HOME -> no agents.local.toml -> the built-in agents alone.
	t.Setenv("HOME", t.TempDir())

	agents, err := LoadAgents()
	if err != nil {
		t.Fatalf("loadAgents: %v", err)
	}
	if len(agents) == 0 {
		t.Fatal("no agents in embedded default")
	}

	var claude *Agent
	for i := range agents {
		if agents[i].Binary == "claude" {
			claude = &agents[i]
			break
		}
	}
	if claude == nil {
		t.Fatal("embedded default missing claude agent (binary=claude)")
	}
	// claude opts into the mcp-server layer; other agents default to off.
	if !claude.Mcp {
		t.Error("claude default should set mcp = true")
	}
	for i := range agents {
		if agents[i].Binary == "pi" && agents[i].Mcp {
			t.Error("pi should default to mcp = false")
		}
	}

	// A subscription login lives in the home volume, so no built-in agent takes
	// variables from the host shell.
	for _, a := range agents {
		if len(a.EnvPrefixes) > 0 {
			t.Errorf("%s forwards %v from the host", a.Name, a.EnvPrefixes)
		}
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name string
		a    Agent
		m    Mode
		want Settings
	}{
		{
			name: "all defaults",
			want: Settings{Volume: "", CPUs: defaultCPUs, Memory: defaultMemory, PIDs: defaultPIDs},
		},
		{
			name: "agent-level wins over defaults",
			a:    Agent{Volume: "av", CPUs: 4, Memory: "8g", PIDs: 1024},
			want: Settings{Volume: "av", CPUs: 4, Memory: "8g", PIDs: 1024},
		},
		{
			name: "mode-level overrides agent",
			a:    Agent{Volume: "av", CPUs: 4, Memory: "8g", PIDs: 1024},
			m:    Mode{Volume: "mv", CPUs: 8, Memory: "16g", PIDs: 2048},
			want: Settings{Volume: "mv", CPUs: 8, Memory: "16g", PIDs: 2048},
		},
		{
			name: "mode partial override",
			a:    Agent{CPUs: 4, Memory: "8g"},
			m:    Mode{Memory: "16g"},
			want: Settings{Volume: "", CPUs: 4, Memory: "16g", PIDs: defaultPIDs},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Resolve(c.a, c.m); got != c.want {
				t.Errorf("resolve = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestVolumeNameFor(t *testing.T) {
	cases := []struct {
		name string
		a    Agent
		m    Mode
		want string
	}{
		{"agent slug only", Agent{Volume: "claude"}, Mode{}, "cjx-home-claude"},
		{"mode slug overrides agent", Agent{Volume: "av"}, Mode{Volume: "mv"}, "cjx-home-mv"},
		{"mode slug only", Agent{}, Mode{Volume: "mv"}, "cjx-home-mv"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := VolumeName(c.a, c.m); got != c.want {
				t.Errorf("volumeNameFor = %q, want %q", got, c.want)
			}
		})
	}
}

func TestValidateAgents(t *testing.T) {
	ok := []Agent{{
		Name:   "claude",
		Volume: "claude-sub",
		Modes: []Mode{
			{Name: "Subscription"},
			{Name: "Second"},
			{Name: "Work", Volume: "claude-work"},
		},
	}}
	if err := validateAgents(ok); err != nil {
		t.Errorf("valid input rejected: %v", err)
	}

	bad := []struct {
		name  string
		input []Agent
		want  string
	}{
		{"empty agents list", []Agent{}, "no agents defined"},
		{"empty agent name", []Agent{{Name: ""}}, "agent name"},
		{"slash in agent name", []Agent{{Name: "foo/bar"}}, "agent name"},
		{"leading dot in agent name", []Agent{{Name: ".hidden"}}, "agent name"},
		{"empty mode name", []Agent{{Name: "a", Modes: []Mode{{Name: ""}}}}, "mode name"},
		{"path traversal in agent volume", []Agent{{Name: "a", Volume: "../etc"}}, "agent volume"},
		{
			"shell metachar in mode volume",
			[]Agent{{Name: "a", Modes: []Mode{{Name: "m", Volume: "v$x"}}}},
			"mode volume",
		},
		{"agent with no modes", []Agent{{Name: "a"}}, "no modes"},
		{
			"no volume on agent or mode",
			[]Agent{{Name: "a", Modes: []Mode{{Name: "m"}}}},
			"volume must be set",
		},
		{
			"agent defined twice",
			[]Agent{
				{Name: "a", Volume: "v", Modes: []Mode{{Name: "m"}}},
				{Name: "a", Volume: "w", Modes: []Mode{{Name: "m"}}},
			},
			"defined twice",
		},
		{
			"mode defined twice",
			[]Agent{{Name: "a", Volume: "v", Modes: []Mode{{Name: "m"}, {Name: "m"}}}},
			"defined twice",
		},
		{
			"a mode naming another agent's volume",
			[]Agent{
				{Name: "a", Modes: []Mode{{Name: "m", Volume: "home"}}},
				{Name: "b", Modes: []Mode{{Name: "m", Volume: "home"}}},
			},
			"already used",
		},
		{
			"a mode naming its agent's shared volume",
			[]Agent{{
				Name: "a", Volume: "home",
				Modes: []Mode{{Name: "m"}, {Name: "n", Volume: "home"}},
			}},
			"already used",
		},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			err := validateAgents(c.input)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestLoadAgentsAddsLocalAgentsAndModes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	const local = `[[agent]]
name = "claude"

  [[agent.mode]]
  name = "Extra"
  volume = "claude-extra"

[[agent]]
name = "acme"
binary = "acme"

  [[agent.mode]]
  name = "default"
  volume = "acme-default"
`
	if err := os.WriteFile(Path(LocalAgentsFile), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	agents, err := LoadAgents()
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	modes := map[string]string{}
	var acme bool
	for _, a := range agents {
		if a.Name == "claude" {
			if a.Binary != "claude" {
				t.Errorf("claude lost its built-in binary: %q", a.Binary)
			}
			for _, m := range a.Modes {
				modes[m.Name] = m.Display
			}
		}
		acme = acme || a.Name == "acme"
	}
	if _, ok := modes["Extra"]; !ok {
		t.Errorf("a new local mode was not added: %v", modes)
	}
	if _, ok := modes["Subscription"]; !ok {
		t.Errorf("the built-in mode went missing: %v", modes)
	}
	if !acme {
		t.Error("a new local agent was not added")
	}
}

// A built-in agent keeps its own install, hosts and limits, so a stale copy of
// them in agents.local.toml cannot hold back what a release changes.
func TestLoadAgentsRefusesToRedefineABuiltInAgent(t *testing.T) {
	for name, line := range map[string]string{
		"a changed install": `install = "curl -fsSL https://pi.dev/install.sh | sh"`,
		"a zero value":      `mcp = false`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if err := os.MkdirAll(Path(), 0o700); err != nil {
				t.Fatal(err)
			}
			local := "[[agent]]\nname = \"pi.dev\"\n" + line + "\n"
			if err := os.WriteFile(Path(LocalAgentsFile), []byte(local), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadAgents(); err == nil || !strings.Contains(err.Error(), "built in") {
				t.Fatalf("LoadAgents = %v, want a refusal naming the built-in agent", err)
			}
		})
	}
}

// A local copy of a built-in mode would pin it to the release it was copied from.
func TestLoadAgentsRefusesABuiltInModeName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	const local = `[[agent]]
name = "pi.dev"

  [[agent.mode]]
  name = "Subscription"
  volume = "pi-work"
`
	if err := os.WriteFile(Path(LocalAgentsFile), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAgents(); err == nil || !strings.Contains(err.Error(), "built-in mode") {
		t.Fatalf("LoadAgents = %v, want a refusal naming the built-in mode", err)
	}
}

func TestLoadAgentsRefusesALocalAgentDefinedTwice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	const block = `[[agent]]
name = "acme"
binary = "acme"

  [[agent.mode]]
  name = "default"
  volume = "acme-default"
`
	if err := os.WriteFile(Path(LocalAgentsFile), []byte(block+"\n"+block), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAgents(); err == nil || !strings.Contains(err.Error(), "defined twice") {
		t.Fatalf("LoadAgents = %v, want a refusal naming the duplicate", err)
	}
}

// A misspelled key must fail loudly: the setting it stands for never applies,
// and for allow_hosts that silently narrows the firewall.
func TestLoadAgentsRejectsUnknownKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `[[agent]]
name = "claude"
binary = "claude"
allow_host = ["api.anthropic.com"]

  [[agent.mode]]
  name = "Subscription"
  volume = "claude-subscription"
  allow_host = ["api.anthropic.com"]
`
	if err := os.WriteFile(Path(LocalAgentsFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadAgents()
	if err == nil {
		t.Fatal("LoadAgents accepted misspelled keys")
	}
	for _, want := range []string{"agent.allow_host", "agent.mode.allow_host"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
}

// Reading the agent list must not create anything; only SeedAllowlist writes.
func TestLoadAgentsWritesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	agents, err := LoadAgents()
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	if len(agents) == 0 {
		t.Fatal("no agents from the built-in default")
	}
	if _, err := os.Stat(Path()); !errors.Is(err, fs.ErrNotExist) {
		entries, _ := os.ReadDir(Path())
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("LoadAgents created %s containing %v", Path(), names)
	}

	if err := SeedAllowlist(); err != nil {
		t.Fatalf("SeedAllowlist: %v", err)
	}
	if _, err := os.Stat(Path("allowlist")); err != nil {
		t.Errorf("SeedAllowlist did not create the allowlist: %v", err)
	}
}

// Every volume the config maps to carries a mode suffix, so a bare cjx-home is
// a leftover from before per-mode volumes and must read as an orphan.
func TestKnownVolumes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	agents, err := LoadAgents()
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	known := KnownVolumes(agents)
	if known[VolumePrefix] {
		t.Errorf("%q counts as mapped, so it would never be offered for pruning", VolumePrefix)
	}
	for name := range known {
		if !strings.HasPrefix(name, VolumePrefix+"-") {
			t.Errorf("volume %q has no mode suffix", name)
		}
	}
	for _, want := range []string{"cjx-home-claude-subscription", "cjx-home-codex-subscription"} {
		if !known[want] {
			t.Errorf("default config does not map %q; got %v", want, known)
		}
	}
}

// The codex block only helps if the shipped default still parses: an unknown key
// or a name podman rejects would surface on a run, not here.
func TestLoadAgentsHasCodex(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	agents, err := LoadAgents()
	if err != nil {
		t.Fatalf("loadAgents: %v", err)
	}
	var codex *Agent
	for i := range agents {
		if agents[i].Name == "codex" {
			codex = &agents[i]
			break
		}
	}
	if codex == nil {
		t.Fatal("embedded default missing the codex agent")
	}
	if len(codex.Modes) != 1 || codex.Modes[0].Name != "Subscription" {
		t.Fatalf("codex has %d mode(s), want one Subscription", len(codex.Modes))
	}
	// codex runs its commands in bubblewrap, which Docker denies the namespace for.
	if !codex.NeedsUserns {
		t.Error("codex needs_userns = false, want true")
	}
	if got := VolumeName(*codex, codex.Modes[0]); got != "cjx-home-codex-subscription" {
		t.Errorf("volume = %q, want cjx-home-codex-subscription", got)
	}
}
