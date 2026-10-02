package engine_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine/enginetest"
)

func TestMain(m *testing.M) { enginetest.Main(m) }

func TestExists(t *testing.T) {
	enginetest.Fake(t, "image exists here=>ok;image exists gone=>")
	e := podmanEngine(t)
	if !e.Exists(engine.Image, "here") {
		t.Error("the engine said the image is there and Exists said no")
	}
	if e.Exists(engine.Image, "gone") {
		t.Error("the engine said the image is gone and Exists said yes")
	}
}

// A container is forced and given a deadline; an image has nothing to stop.
func TestRemove(t *testing.T) {
	c := enginetest.Fake(t, "container exists=>ok;container rm=>ok;image exists=>ok;image rm=>ok")
	e := podmanEngine(t)
	e.Remove(engine.Container, "cjx-firewall-x")
	e.Remove(engine.Image, "localhost/cjx-base:latest")
	if !c.Got("container rm -f -t 1 cjx-firewall-x") {
		t.Errorf("container removal: %v", c.All())
	}
	if !c.Got("image rm localhost/cjx-base:latest") {
		t.Errorf("image removal: %v", c.All())
	}
}

// A resource that is not there is not removed, so a stale name cannot make the
// engine print a warning about something that was already gone.
func TestRemoveSkipsWhatIsNotThere(t *testing.T) {
	c := enginetest.Fake(t, "volume exists=>")
	podmanEngine(t).Remove(engine.Volume, "cjx-home-gone")
	if c.Got("volume rm") {
		t.Errorf("removed a volume that does not exist: %v", c.All())
	}
}

func TestNames(t *testing.T) {
	enginetest.Fake(t, "volume ls=>cjx-home-a|other|cjx-home-b")
	got := podmanEngine(t).Names(engine.Volume, "cjx-home-")
	if len(got) != 2 || got[0] != "cjx-home-a" || got[1] != "cjx-home-b" {
		t.Errorf("got %v, want [cjx-home-a cjx-home-b]", got)
	}
}

// Containers are listed running-only here, unlike Names.
func TestRunningContainers(t *testing.T) {
	c := enginetest.Fake(t, "ps --format=>cjx-session-a")
	got := podmanEngine(t).RunningContainers("cjx-session-")
	if len(got) != 1 || got[0] != "cjx-session-a" {
		t.Errorf("got %v", got)
	}
	if c.Got("ps -a") {
		t.Errorf("asked for stopped containers too: %v", c.All())
	}
}

func TestImageDigest(t *testing.T) {
	enginetest.Fake(t, "image inspect --format {{.Id}}=>sha256:abc")
	got, err := podmanEngine(t).ImageDigest(t.Context(), "img")
	if err != nil || got != "sha256:abc" {
		t.Errorf("got %q, %v", got, err)
	}
}

// An inspect that prints an empty id must not pass for a digest: cjx would then
// rebuild nothing and run a stale image.
func TestImageDigestRejectsEmpty(t *testing.T) {
	enginetest.Fake(t, "image inspect=>|")
	if _, err := podmanEngine(t).ImageDigest(t.Context(), "img"); err == nil {
		t.Error("an empty digest was accepted")
	}
}

func TestImageLabel(t *testing.T) {
	enginetest.Fake(t, "image inspect=>deadbeef")
	if got := podmanEngine(t).ImageLabel(t.Context(), "img", "cjx.drift-hash"); got != "deadbeef" {
		t.Errorf("got %q", got)
	}
}

func TestImageLabelIsEmptyWhenTheImageIsGone(t *testing.T) {
	enginetest.Fake(t, "image inspect=>")
	if got := podmanEngine(t).ImageLabel(t.Context(), "img", "cjx.drift-hash"); got != "" {
		t.Errorf("got %q, want the empty label", got)
	}
}

func TestInspectIP(t *testing.T) {
	enginetest.Fake(t, "inspect --format=>10.89.0.4")
	got, err := podmanEngine(t).InspectIP(t.Context(), "cjx-firewall-x", "cjx-net-x")
	if err != nil || got != "10.89.0.4" {
		t.Errorf("got %q, %v", got, err)
	}
}

// Without an address the agent would be pointed at "http://:8080".
func TestInspectIPRejectsEmpty(t *testing.T) {
	enginetest.Fake(t, "inspect=>|")
	if _, err := podmanEngine(t).InspectIP(t.Context(), "c", "n"); err == nil {
		t.Error("an empty address was accepted")
	}
}

func TestIsRunning(t *testing.T) {
	enginetest.Fake(t, "ps -q --filter name=up=>up-id;ps -q --filter name=down=>|")
	e := podmanEngine(t)
	if !e.IsRunning("up") {
		t.Error("a container the engine listed is reported as not running")
	}
	if e.IsRunning("down") {
		t.Error("a container the engine did not list is reported as running")
	}
}

func TestRunDetachedAttachesEveryNetwork(t *testing.T) {
	c := enginetest.Fake(t, "run -d=>ok")
	err := podmanEngine(t).RunDetached(t.Context(), "cjx-firewall-x",
		[]string{"podman", "cjx-net-x"}, []string{"localhost/cjx-firewall:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Got("run -d --replace --name cjx-firewall-x --network podman --network cjx-net-x") {
		t.Errorf("networks not both on the run line: %v", c.All())
	}
}

func TestLogs(t *testing.T) {
	enginetest.Fake(t, "logs --tail 50=>Accepting HTTP")
	got := podmanEngine(t).Logs(t.Context(), "c", 50)
	if !strings.Contains(string(got), "Accepting HTTP") {
		t.Errorf("got %q", got)
	}
}

// The engine must be on PATH before anything else is attempted.
func TestReadyRefusesAMissingEngine(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := podmanEngine(t).Ready(t.Context()); err == nil {
		t.Error("Ready accepted an engine that is not installed")
	}
}

// On Linux there is no machine to be stopped, so Ready must not ask.
func TestReadyIgnoresMachinesOffDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("this is the non-macOS path")
	}
	c := enginetest.Fake(t, "")
	if err := podmanEngine(t).Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.Got("machine") {
		t.Errorf("asked about machines off macOS: %v", c.All())
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
