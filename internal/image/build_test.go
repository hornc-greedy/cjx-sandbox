package image

import (
	"strings"
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/container"
	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine/enginetest"
)

func TestMain(m *testing.M) { enginetest.Main(m) }

func claude() config.Agent {
	return config.Agent{Name: "claude", Binary: "claude", Packages: []string{"jq"}}
}

// Podman finds a Containerfile on its own, Docker looks for a Dockerfile; the
// build must name the file either way.
func TestBuildBaseNamesTheContainerfile(t *testing.T) {
	c := enginetest.Fake(t, "build=>ok")
	if err := BuildBase(t.Context(), podmanEngine(t)); err != nil {
		t.Fatal(err)
	}
	call := c.All()[0]
	if !strings.Contains(call, "-f ") || !strings.Contains(call, "Containerfile") {
		t.Errorf("build does not name the file: %s", call)
	}
	if !strings.Contains(call, "-t "+Base) {
		t.Errorf("build does not tag the base image: %s", call)
	}
}

// The firewall context is a subdirectory of the embedded tree, not its root.
func TestBuildFirewallUsesItsOwnContext(t *testing.T) {
	c := enginetest.Fake(t, "build=>ok")
	if err := BuildFirewall(t.Context(), podmanEngine(t)); err != nil {
		t.Fatal(err)
	}
	if call := c.All()[0]; !strings.Contains(call, "firewall/Containerfile") {
		t.Errorf("firewall build reads the wrong context: %s", call)
	}
}

// The agent layer carries the hash a later run compares against.
func TestBuildAgentStampsTheDriftHash(t *testing.T) {
	c := enginetest.Fake(t, "image inspect=>sha256:base;build=>ok")
	if err := BuildAgent(t.Context(), podmanEngine(t), claude()); err != nil {
		t.Fatal(err)
	}
	if !c.Got("build") {
		t.Fatalf("no build: %v", c.All())
	}
	for _, call := range c.All() {
		if strings.HasPrefix(call, "build") && !strings.Contains(call, "--label cjx.drift-hash=") {
			t.Errorf("agent build carries no drift hash: %s", call)
		}
	}
}

// An agent without an mcp layer gets the user tag as an alias, not a rebuild.
func TestBuildUserRetagsWhenThereIsNothingToStack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := enginetest.Fake(t, "tag=>ok")
	if err := BuildUser(t.Context(), podmanEngine(t), claude()); err != nil {
		t.Fatal(err)
	}
	if !c.Got("tag " + AgentTag(claude()) + " " + UserTag(claude())) {
		t.Errorf("no retag: %v", c.All())
	}
}

// A present image whose label matches must not be rebuilt; that check is what
// keeps every run from rebuilding.
func TestEnsureAgentSkipsWhenNothingDrifted(t *testing.T) {
	a := claude()
	want := driftHash(a, "sha256:base", agentContainerfile(a))
	c := enginetest.Fake(t, "image exists=>ok"+
		";image inspect --format {{.Id}}=>sha256:base"+
		";image inspect --format {{index .Config.Labels=>"+want)
	if _, err := EnsureAgent(t.Context(), podmanEngine(t), a); err != nil {
		t.Fatal(err)
	}
	if c.Got("build") {
		t.Errorf("rebuilt although the label matched: %v", c.All())
	}
}

// A missing image is built, and the caller is told, so it can reclaim the
// superseded user layer.
func TestEnsureAgentReportsARebuild(t *testing.T) {
	c := enginetest.Fake(t, "image exists=>;image inspect=>sha256:base;build=>ok")
	rebuilt, err := EnsureAgent(t.Context(), podmanEngine(t), claude())
	if err != nil {
		t.Fatal(err)
	}
	if !rebuilt {
		t.Error("a build happened and EnsureAgent reported none")
	}
	if !c.Got("build") {
		t.Errorf("no build: %v", c.All())
	}
}

// The user layer must survive the agent rebuild: a session still running holds
// it, and EnsureUser retags or rebuilds it right afterwards.
func TestEnsureAgentLeavesTheUserImageAlone(t *testing.T) {
	c := enginetest.Fake(t, "image exists=>;image inspect=>sha256:base;build=>ok")
	if _, err := EnsureAgent(t.Context(), podmanEngine(t), claude()); err != nil {
		t.Fatal(err)
	}
	if c.Got("image rm " + UserTag(claude())) {
		t.Errorf("the user image was dropped: %v", c.All())
	}
}

func TestPruneDanglingScopesToCjx(t *testing.T) {
	c := enginetest.Fake(t, "image prune=>Total reclaimed space: 1.2GB")
	PruneDangling(t.Context(), podmanEngine(t))
	if !c.Got("image prune -f --filter label=cjx.drift-hash") {
		t.Errorf("prune not scoped to cjx: %v", c.All())
	}
}

// A binary whose embedded sources changed must rebuild the base image rather
// than reuse the one it finds.
func TestEnsureBaseRebuildsOnChangedSources(t *testing.T) {
	c := enginetest.Fake(t, "image exists=>ok"+
		";image inspect --format {{index .Config.Labels=>from-an-older-binary"+
		";build=>ok")
	if err := EnsureBase(t.Context(), podmanEngine(t)); err != nil {
		t.Fatal(err)
	}
	if !c.Got("build") {
		t.Errorf("stale sources were not rebuilt: %v", c.All())
	}
}

// The same binary must not rebuild on every run.
func TestEnsureBaseSkipsWhenTheSourcesMatch(t *testing.T) {
	want, err := embeddedDriftHash(container.BaseFS)
	if err != nil {
		t.Fatal(err)
	}
	c := enginetest.Fake(t, "image exists=>ok"+
		";image inspect --format {{index .Config.Labels=>"+want)
	if err := EnsureBase(t.Context(), podmanEngine(t)); err != nil {
		t.Fatal(err)
	}
	if c.Got("build") {
		t.Errorf("rebuilt although the sources match: %v", c.All())
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
