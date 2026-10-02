package main

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/internal/engine/enginetest"
	"github.com/hornc-greedy/cjx-sandbox/internal/image"
)

func TestMain(m *testing.M) { enginetest.Main(m) }

// execute drives a command the way the shell would, so the wiring between
// cobra, the engine and the packages is exercised, not just the pieces.
func execute(t *testing.T, args ...string) error {
	t.Helper()
	root := newRoot()
	root.SetArgs(args)
	return root.ExecuteContext(t.Context())
}

// status reports one line per image, and says so when one is missing.
func TestStatusReportsEveryImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	c := enginetest.Fake(t, "image exists "+image.Base+"=>ok"+
		";image exists=>"+
		";volume ls=>cjx-home-claude-subscription"+
		";ps --format=>")
	if err := execute(t, "status"); err != nil {
		t.Fatal(err)
	}
	if !c.Got("image exists " + image.Firewall) {
		t.Errorf("the firewall image was not asked about: %v", c.All())
	}
}

// update rebuilds only agents whose image is already there, then reclaims what
// the rebuild superseded.
func TestUpdateSkipsAgentsNeverRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := enginetest.Fake(t, "build=>ok;image exists=>;image prune=>Total reclaimed space: 0B")
	if err := execute(t, "update"); err != nil {
		t.Fatal(err)
	}
	var builds int
	for _, call := range c.All() {
		if strings.HasPrefix(call, "build") {
			builds++
		}
	}
	if builds != 2 {
		t.Errorf("want the base and firewall builds only, got %d: %v", builds, c.All())
	}
	if !c.Got("image prune") {
		t.Errorf("the superseded images were not reclaimed: %v", c.All())
	}
}

// prune removes an agent image no agent maps to any more, and keeps one it
// does.
func TestPruneRemovesOnlyOrphans(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const listed = "localhost/cjx-agent-claude:latest|localhost/cjx-agent-gone:latest"
	c := enginetest.Fake(t, "image ls=>"+listed+";image exists=>ok;image rm=>ok")
	if err := execute(t, "prune"); err != nil {
		t.Fatal(err)
	}
	if !c.Got("image rm localhost/cjx-agent-gone:latest") {
		t.Errorf("the orphan was kept: %v", c.All())
	}
	if c.Got("image rm localhost/cjx-agent-claude:latest") {
		t.Errorf("an agent that is still configured was removed: %v", c.All())
	}
}

// export streams the volume's tar into a file, and import feeds that same
// archive back into the engine.
func TestExportImportRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	file := filepath.Join(t.TempDir(), "cjx-home-claude-subscription.tar.gz")
	c := enginetest.Fake(t, "volume ls=>cjx-home-claude-subscription"+
		";volume exists=>"+
		";build=>ok"+
		";run --rm=>tar-bytes")

	if err := execute(t, "export", "cjx-home-claude-subscription", "--file", file); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("the archive is not gzipped: %v", err)
	}
	packed, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(packed)) != "tar-bytes" {
		t.Errorf("the archive does not hold what tar wrote: %q", packed)
	}
	var pack string
	for _, call := range c.All() {
		if strings.Contains(call, "-cf") {
			pack = call
		}
	}
	if !strings.Contains(pack, "cjx-home-claude-subscription:/home/agent:ro") {
		t.Errorf("the volume was not read read-only: %v", c.All())
	}

	if err := execute(t, "import", file); err != nil {
		t.Fatal(err)
	}
	var unpack string
	for _, call := range c.All() {
		if strings.Contains(call, "-xf") {
			unpack = call
		}
	}
	if !strings.Contains(unpack, "cjx-home-claude-subscription:/home/agent") {
		t.Errorf("the archive was not unpacked into the volume it names: %v", c.All())
	}
}

// status measures the volumes in one container, and asks the engine for the
// image sizes rather than computing them.
func TestStatusReportsSizes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	c := enginetest.Fake(t, "image ls --format=>"+image.Base+"\t1.21GB"+
		";image exists=>ok"+
		";volume ls=>cjx-home-claude-subscription"+
		";run --rm=>11G\t/vols/cjx-home-claude-subscription"+
		";ps --format=>")
	if err := execute(t, "status"); err != nil {
		t.Fatal(err)
	}
	var du string
	for _, call := range c.All() {
		if strings.Contains(call, "du -sh") {
			du = call
		}
	}
	if !strings.Contains(du, "cjx-home-claude-subscription:/vols/cjx-home-claude-subscription:ro") {
		t.Errorf("the volume was not measured read-only: %v", c.All())
	}
}
