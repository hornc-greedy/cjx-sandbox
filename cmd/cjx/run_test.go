package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine/enginetest"
)

func TestAllowlistFor(t *testing.T) {
	a := config.Agent{AllowHosts: []string{"a.example.com", "*.foo.com"}}
	m := config.Mode{AllowHosts: []string{"b.example.com"}}
	got := allowlistFor(a, m)
	want := []string{"a.example.com", "*.foo.com", "b.example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("allowlistFor = %v, want %v", got, want)
	}
}

func TestHostEnv(t *testing.T) {
	a := config.Agent{EnvPrefixes: []string{"CJXTEST_FOO_", "CJXTEST_BAR_"}}
	t.Setenv("CJXTEST_FOO_KEEP", "1")
	t.Setenv("CJXTEST_BAR_OK", "ok")
	t.Setenv("CJXTEST_BAZ_NO", "no")

	got := hostEnv(a)
	if !slices.Contains(got, "CJXTEST_FOO_KEEP=1") || !slices.Contains(got, "CJXTEST_BAR_OK=ok") {
		t.Errorf("hostEnv misses a prefixed variable: %v", got)
	}
	if slices.Contains(got, "CJXTEST_BAZ_NO=no") {
		t.Errorf("hostEnv forwards an unprefixed variable: %v", got)
	}
}

func TestBuildRunArgs(t *testing.T) {
	eng := mustEngine(t)
	a := config.Agent{Name: "test", Binary: "/bin/test", EnvPrefixes: []string{"CJXTEST_FOO_"}}
	m := config.Mode{}
	const (
		network   = "cjx-net-test-1"
		sessProxy = "http://10.99.99.99:8080"
	)

	// wantProxy is the URL the explicit -e proxy vars must match, or "" if none
	// are expected. podman's own proxy injection is always off; cjx sets the vars.
	checkProxy := func(t *testing.T, args []string, wantProxy string) {
		t.Helper()
		if !slices.Contains(args, "--http-proxy=false") {
			t.Errorf("missing --http-proxy=false: %v", args)
		}
		for i, arg := range args {
			if arg != "-e" || i+1 >= len(args) {
				continue
			}
			kv := args[i+1]
			if !strings.HasPrefix(kv, "HTTP_PROXY=") && !strings.HasPrefix(kv, "HTTPS_PROXY=") {
				continue
			}
			if wantProxy == "" {
				t.Errorf("unexpected explicit proxy var: %q", kv)
			} else if !strings.Contains(kv, wantProxy) {
				t.Errorf("proxy var %q does not contain %q", kv, wantProxy)
			}
		}
	}

	// Firewall: proxy points at Squid.
	t.Run("firewall points proxy at Squid", func(t *testing.T) {
		args := buildRunArgs(eng, sandboxRun{
			Agent: a, Mode: m, Workdir: "/tmp/cwd", Network: network, ProxyURL: sessProxy,
		})
		checkProxy(t, args, sessProxy)
	})
	// Without a firewall there is no Squid, so the sandbox goes direct.
	t.Run("no-firewall stays direct without a proxy", func(t *testing.T) {
		args := buildRunArgs(eng, sandboxRun{
			runOpts: runOpts{noFirewall: true}, Agent: a, Mode: m, Workdir: "/tmp/cwd",
		})
		checkProxy(t, args, "")
	})
	t.Run("rw mounts read-write", func(t *testing.T) {
		args := buildRunArgs(eng, sandboxRun{
			runOpts: runOpts{rw: true}, Agent: a, Mode: m,
			Workdir: "/tmp/cwd", Network: network, ProxyURL: sessProxy,
		})
		var mount string
		for i, arg := range args {
			if arg == "-v" && i+1 < len(args) && strings.Contains(args[i+1], ":/workspace:") {
				mount = args[i+1]
			}
		}
		if !strings.Contains(mount, ":/workspace:rw,") {
			t.Errorf("rw flag did not produce rw mount; got %q", mount)
		}
	})
	t.Run("default mounts read-only", func(t *testing.T) {
		args := buildRunArgs(eng, sandboxRun{
			Agent: a, Mode: m, Workdir: "/tmp/cwd", Network: network, ProxyURL: sessProxy,
		})
		var mount string
		for i, arg := range args {
			if arg == "-v" && i+1 < len(args) && strings.Contains(args[i+1], ":/workspace:") {
				mount = args[i+1]
			}
		}
		if !strings.Contains(mount, ":/workspace:ro,") {
			t.Errorf("default mount not read-only; got %q", mount)
		}
	})
	t.Run("network from session", func(t *testing.T) {
		args := buildRunArgs(eng, sandboxRun{
			Agent: a, Mode: m, Workdir: "/tmp/cwd", Network: network, ProxyURL: sessProxy,
		})
		for i, arg := range args {
			if arg == "--network" && i+1 < len(args) && args[i+1] == network {
				return
			}
		}
		t.Errorf("missing --network %q in args", network)
	})
	t.Run("default resource limits", func(t *testing.T) {
		args := buildRunArgs(eng, sandboxRun{
			Agent: a, Mode: m, Workdir: "/tmp/cwd", Network: network, ProxyURL: sessProxy,
		})
		for _, want := range []string{"--cpus=2", "--memory=4g", "--pids-limit=512"} {
			if !slices.Contains(args, want) {
				t.Errorf("missing %q in args", want)
			}
		}
	})

	t.Run("mode resource limits override agent and defaults", func(t *testing.T) {
		agent := config.Agent{Name: "test", Binary: "/bin/test", CPUs: 4, Memory: "8g"}
		mode := config.Mode{Memory: "16g", PIDs: 1024}
		args := buildRunArgs(eng, sandboxRun{
			Agent: agent, Mode: mode, Workdir: "/tmp/cwd",
			Network: network, ProxyURL: sessProxy,
		})
		for _, want := range []string{"--cpus=4", "--memory=16g", "--pids-limit=1024"} {
			if !slices.Contains(args, want) {
				t.Errorf("missing %q in args (got %v)", want, args)
			}
		}
	})
}

func TestValidateAddDirTarget(t *testing.T) {
	ok := []string{
		"/var/home/user/project", // Fedora Silverblue user home lives under /var
		"/home/user/code/lib",
		"/Users/me/proj", // macOS
		"/data/x",
		"/var/lib/foo",        // a child of a reserved dir is permitted...
		"/usr/bin",            // ...even a system subdir (unlikely footgun, not pre-banned)
		"/home/agent/.config", // child of a mount point is permitted
		"/tmp/scratch",
	}
	for _, p := range ok {
		if err := validateAddDirTarget(p); err != nil {
			t.Errorf("validateAddDirTarget(%q) = %v, want nil", p, err)
		}
	}
	bad := []string{
		"/", "/usr", "/bin", "/etc", "/opt", "/proc",
		"/var", "/home", "/home/agent", "/workspace", "/tmp",
	}
	for _, p := range bad {
		if err := validateAddDirTarget(p); err == nil {
			t.Errorf("validateAddDirTarget(%q) = nil, want error", p)
		}
	}
}

func TestResolveAddDirs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Valid paths are returned absolute and de-duplicated, order preserved.
	got, err := resolveAddDirs([]string{dir, sub, dir})
	if err != nil {
		t.Fatalf("resolveAddDirs valid: %v", err)
	}
	if want := []string{dir, sub}; !slices.Equal(got, want) {
		t.Errorf("resolveAddDirs = %v, want %v", got, want)
	}
	if got, err := resolveAddDirs(nil); err != nil || got != nil {
		t.Errorf("resolveAddDirs(nil) = %v, %v; want nil, nil", got, err)
	}

	for _, c := range []struct{ name, in string }{
		{"missing path", filepath.Join(dir, "nope")},
		{"not a directory", file},
		{"filesystem root", "/"},
		{"reserved exact", "/usr"},
		{"ancestor of reserved", "/home"}, // ancestor of /home/agent
	} {
		if _, err := resolveAddDirs([]string{c.in}); err == nil {
			t.Errorf("resolveAddDirs(%s=%q): expected error, got nil", c.name, c.in)
		}
	}
}

func TestBuildRunArgsAddDir(t *testing.T) {
	eng := mustEngine(t)
	claude := config.Agent{Name: "claude", Binary: "claude"}
	pi := config.Agent{Name: "pi.dev", Binary: "pi"}
	m := config.Mode{}
	dirs := []string{"/data/foo", "/data/bar"}

	hasMount := func(args []string, spec string) bool {
		for i, a := range args {
			if a == "-v" && i+1 < len(args) && args[i+1] == spec {
				return true
			}
		}
		return false
	}
	addDirFlags := func(args []string) []string {
		var out []string
		for i, a := range args {
			if a == "--add-dir" && i+1 < len(args) {
				out = append(out, args[i+1])
			}
		}
		return out
	}

	t.Run("mounts same path, read-only by default", func(t *testing.T) {
		args := buildRunArgs(eng, sandboxRun{
			runOpts: runOpts{addDirs: dirs}, Agent: claude, Mode: m, Workdir: "/cwd",
		})
		for _, d := range dirs {
			if !hasMount(args, d+":"+d+":ro,z") {
				t.Errorf("missing ro mount for %s; got %v", d, args)
			}
		}
	})
	t.Run("rw mounts with --rw", func(t *testing.T) {
		args := buildRunArgs(eng, sandboxRun{
			runOpts: runOpts{rw: true, addDirs: dirs}, Agent: claude,
			Mode: m, Workdir: "/cwd",
		})
		if !hasMount(args, "/data/foo:/data/foo:rw,z") {
			t.Errorf("missing rw mount; got %v", args)
		}
	})
	t.Run("passes --add-dir to claude", func(t *testing.T) {
		args := buildRunArgs(eng, sandboxRun{
			runOpts: runOpts{addDirs: dirs}, Agent: claude, Mode: m, Workdir: "/cwd",
		})
		if got := addDirFlags(args); !slices.Equal(got, dirs) {
			t.Errorf("claude --add-dir = %v, want %v", got, dirs)
		}
	})
	t.Run("no --add-dir for non-claude, still mounts", func(t *testing.T) {
		args := buildRunArgs(eng, sandboxRun{
			runOpts: runOpts{addDirs: dirs}, Agent: pi, Mode: m, Workdir: "/cwd",
		})
		if got := addDirFlags(args); len(got) != 0 {
			t.Errorf("non-claude should not get --add-dir; got %v", got)
		}
		if !hasMount(args, "/data/foo:/data/foo:ro,z") {
			t.Errorf("non-claude should still mount the dir; got %v", args)
		}
	})
	t.Run("no --add-dir in shell mode, still mounts", func(t *testing.T) {
		opts := runOpts{shell: true, addDirs: dirs}
		args := buildRunArgs(eng, sandboxRun{
			runOpts: opts, Agent: claude, Mode: m, Workdir: "/cwd",
		})
		if got := addDirFlags(args); len(got) != 0 {
			t.Errorf("shell mode should not get --add-dir; got %v", got)
		}
		if !hasMount(args, "/data/foo:/data/foo:ro,z") {
			t.Errorf("shell mode should still mount the dir; got %v", args)
		}
	})
}

// The sandbox rests on these flags; no combination of options may drop one.
func TestRunArgsAlwaysSandboxed(t *testing.T) {
	eng := mustEngine(t)
	a := config.Agent{Name: "test", Binary: "/bin/test"}
	m := config.Mode{}
	cases := map[string]runOpts{
		"plain":       {},
		"read-write":  {rw: true},
		"shell":       {shell: true},
		"no firewall": {noFirewall: true},
		"add-dir":     {addDirs: []string{"/tmp"}},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			args := buildRunArgs(eng, sandboxRun{
				runOpts: opts, Agent: a, Mode: m, Workdir: "/cwd", Network: "net",
			})
			want := append(eng.SecurityArgs(), eng.IdentityArgs()...)
			for _, w := range want {
				if !slices.Contains(args, w) {
					t.Errorf("%s dropped from %v", w, args)
				}
			}
			if !slices.ContainsFunc(args, func(s string) bool {
				return strings.HasPrefix(s, "--pids-limit=")
			}) {
				t.Error("pid limit dropped")
			}
		})
	}
}

// runFixture adds an agent of its own and pins it: runAgent then takes the
// saved-selection path and no menu opens. The agent is a local one, so an edit to
// the built-in agents cannot turn these into failures that point at the wrong file.
func runFixture(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := os.MkdirAll(config.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	const local = `[[agent]]
name = "fixture"
display = "Fixture"
binary = "fixture"
allow_hosts = ["api.example.com"]

  [[agent.mode]]
  name = "Subscription"
  volume = "fixture-subscription"
`
	if err := os.WriteFile(config.Path(config.LocalAgentsFile), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.SeedAllowlist(); err != nil {
		t.Fatal(err)
	}
	agents, err := config.LoadAgents()
	if err != nil {
		t.Fatal(err)
	}
	agent, mode, ok := config.FindSelection(agents,
		config.Selection{Agent: "fixture", Mode: "Subscription"})
	if !ok {
		t.Fatal("the fixture agent did not load")
	}
	config.SaveSelection(agent, mode)
}

// The pieces below runAgent are each tested on their own; this drives the whole
// flow so that a reordering, or a session whose network never reaches the
// sandbox, fails here instead of at the first real run.
func TestRunAgentStartsTheSandboxBehindTheFirewall(t *testing.T) {
	runFixture(t)
	// The digest reply is the same for the image ID and for the drift-hash
	// label, so the hash never matches and the build path runs too.
	c := enginetest.Fake(t, "image inspect --format=>sha256:1"+
		";logs=>Accepting HTTP connections"+
		";inspect --format=>10.89.0.7")

	if err := runAgent(t.Context(), mustEngine(t), "", runOpts{}); err != nil {
		t.Fatal(err)
	}

	calls := c.All()
	at := func(prefix string) int {
		return slices.IndexFunc(calls, func(s string) bool { return strings.HasPrefix(s, prefix) })
	}
	images, squid, sandbox := at("tag "), at("run -d"), at("run --rm")
	if images < 0 || squid < images || sandbox < squid {
		t.Fatalf("images/firewall/sandbox out of order: %v", calls)
	}

	// resolveNetwork feeds buildRunArgs: what the session opened is what the
	// sandbox joins and trusts.
	if !strings.Contains(calls[sandbox], "--network cjx-net-") {
		t.Errorf("sandbox not on the session network: %s", calls[sandbox])
	}
	if !strings.Contains(calls[sandbox], "-e HTTPS_PROXY=http://10.89.0.7:8080") {
		t.Errorf("sandbox does not trust the session proxy: %s", calls[sandbox])
	}
	if !c.Got("network rm cjx-net-") {
		t.Errorf("the deferred cleanup left the session network behind: %v", calls)
	}
}

// Switching engines must not cost the login: the home volume is carried over
// from the other engine before the agent starts.
func TestRunAgentCarriesTheHomeOver(t *testing.T) {
	runFixture(t)
	c := enginetest.Fake(t, "info --format=>name=seccomp,profile=builtin"+
		// The two engines ask differently: podman has a verb for it, docker
		// inspects. Docker answers "missing", podman "here it is".
		";docker volume inspect=>"+
		";podman volume exists=>ok"+
		";image inspect --format=>sha256:1"+
		";logs=>Accepting HTTP connections"+
		";inspect --format=>10.89.0.7")
	e, err := engine.Detect(t.Context(), engine.Docker)
	if err != nil {
		t.Fatal(err)
	}
	if err := runAgent(t.Context(), e, "", runOpts{}); err != nil {
		t.Fatal(err)
	}

	var read, write string
	for _, call := range c.All() {
		if strings.Contains(call, "cjx-home-fixture-subscription:/home/agent:ro") {
			read = call
		}
		if strings.Contains(call, "-xf") {
			write = call
		}
	}
	if read == "" || write == "" {
		t.Fatalf("the home was not streamed over: %v", c.All())
	}
	if !strings.Contains(read, "--entrypoint tar") {
		t.Errorf("the source was not read with tar: %s", read)
	}
}

// A bad --add-dir must fail before anything is built.
func TestRunAgentStopsBeforeTouchingTheEngine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "missing")

	c := enginetest.Fake(t, "")
	err := runAgent(t.Context(), mustEngine(t), "", runOpts{addDirs: []string{missing}})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("runAgent = %v, want the bad path named", err)
	}
	if len(c.All()) > 0 {
		t.Errorf("the engine was asked for something anyway: %v", c.All())
	}
}

func TestMcpConfigPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got, err := mcpConfigPath(config.Agent{Mcp: false}); got != "" || err != nil {
		t.Errorf("agent without mcp: %q, %v", got, err)
	}
	if got, err := mcpConfigPath(config.Agent{Mcp: true}); got != "" || err != nil {
		t.Errorf("mcp.json not written yet: %q, %v", got, err)
	}
	want := config.Path("mcp-server", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(want), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := mcpConfigPath(config.Agent{Mcp: true}); got != want || err != nil {
		t.Errorf("mcpConfigPath = %q, %v, want %q", got, err, want)
	}
}

func mustEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.Detect(t.Context(), engine.Podman)
	if err != nil {
		t.Fatalf("Detect(%q): %v", engine.Podman, err)
	}
	return e
}

// Docker reads proxy variables from its own config and has no switch against it,
// so a run has to blank them even when there is no proxy at all.
func TestBuildRunArgsBlanksDockerProxyVars(t *testing.T) {
	enginetest.Fake(t, "info --format=>name=seccomp,profile=builtin")
	e, err := engine.Detect(t.Context(), engine.Docker)
	if err != nil {
		t.Fatal(err)
	}
	args := buildRunArgs(e, sandboxRun{
		Agent: config.Agent{Name: "test", Binary: "/bin/test"}, Mode: config.Mode{},
		Workdir: "/cwd",
	})
	for _, want := range []string{
		"HTTP_PROXY=", "HTTPS_PROXY=", "NO_PROXY=localhost,127.0.0.1",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("%q missing from %v", want, args)
		}
	}
}

// An agent that creates a user namespace gets it from Docker only once the
// seccomp filter is off. Every other agent keeps the filter.
func TestBuildRunArgsLiftsDockerSeccompForAUserNamespace(t *testing.T) {
	enginetest.Fake(t, "info --format=>name=seccomp,profile=builtin")
	e, err := engine.Detect(t.Context(), engine.Docker)
	if err != nil {
		t.Fatal(err)
	}
	const unconfined = "--security-opt=seccomp=unconfined"
	for _, tc := range []struct {
		name  string
		agent config.Agent
		want  bool
	}{
		{"user namespace", config.Agent{Name: "codex", Binary: "codex", NeedsUserns: true}, true},
		{"plain agent", config.Agent{Name: "claude", Binary: "claude"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := buildRunArgs(e, sandboxRun{
				Agent: tc.agent, Mode: config.Mode{}, Workdir: "/cwd",
			})
			if got := slices.Contains(args, unconfined); got != tc.want {
				t.Errorf("%s in args = %v, want %v: %v", unconfined, got, tc.want, args)
			}
		})
	}
}

// A rootless Podman container has a user namespace already, so the agent that
// wants one needs nothing added to the run.
func TestBuildRunArgsKeepsPodmanSeccompForAUserNamespace(t *testing.T) {
	args := buildRunArgs(mustEngine(t), sandboxRun{
		Agent:   config.Agent{Name: "codex", Binary: "codex", NeedsUserns: true},
		Mode:    config.Mode{},
		Workdir: "/cwd",
	})
	if slices.ContainsFunc(args, func(s string) bool {
		return strings.Contains(s, "seccomp")
	}) {
		t.Errorf("podman run touched seccomp: %v", args)
	}
}
