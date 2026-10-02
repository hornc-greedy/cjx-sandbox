package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/hornc-greedy/cjx-sandbox/container"
	"github.com/hornc-greedy/cjx-sandbox/internal/config"
)

func TestOrphanPruneArgs(t *testing.T) {
	args := pruneDanglingArgs()
	if len(args) < 2 || args[0] != "image" || args[1] != "prune" {
		t.Fatalf("expected an image prune command, got %v", args)
	}
	// Must stay label-scoped so non-cjx dangling images are never touched.
	want := "label=" + driftHashLabel
	scoped := false
	for i, a := range args {
		if a == "--filter" && i+1 < len(args) && args[i+1] == want {
			scoped = true
		}
	}
	if !scoped {
		t.Errorf("prune not scoped to %q: %v", want, args)
	}
	if !strings.Contains(strings.Join(args, " "), "-f") {
		t.Errorf("prune not forced (-f missing): %v", args)
	}
}

func TestDriftHash(t *testing.T) {
	a := config.Agent{
		Name:     "claude",
		Binary:   "claude",
		Install:  "curl -fsSL https://example.invalid/install.sh | sh",
		Packages: []string{"nodejs", "npm"},
	}
	base := "sha256:abc"
	cf := agentContainerfile(a)

	got := driftHash(a, base, cf)
	if got == "" {
		t.Fatal("driftHash returned empty string")
	}
	if again := driftHash(a, base, cf); got != again {
		t.Errorf("driftHash not stable: %q vs %q", got, again)
	}

	// Each input dimension must influence the hash.
	withInstall := a
	withInstall.Install = "other"
	withPackages := a
	withPackages.Packages = []string{"go"}
	withBinary := a
	withBinary.Binary = "other"
	withName := a
	withName.Name = "other"
	for _, c := range []struct {
		name string
		hash string
	}{
		{"different base digest", driftHash(a, "sha256:def", cf)},
		{"different containerfile", driftHash(a, base, cf+"# extra\n")},
		{"different install", driftHash(withInstall, base, agentContainerfile(withInstall))},
		{"different packages", driftHash(withPackages, base, agentContainerfile(withPackages))},
		{"different binary", driftHash(withBinary, base, cf)},
		{"different name", driftHash(withName, base, cf)},
	} {
		if c.hash == got {
			t.Errorf("%s: hash unchanged (%q)", c.name, c.hash)
		}
	}
}

func TestUserDriftHash(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "Containerfile"), []byte("FROM x\nRUN echo hi\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "helper.sh"), []byte("#!/bin/sh\necho helper\n"), 0o644))

	got, err := userDriftHash(dir, "sha256:abc")
	if err != nil {
		t.Fatalf("userDriftHash: %v", err)
	}
	again, err := userDriftHash(dir, "sha256:abc")
	if err != nil {
		t.Fatalf("userDriftHash again: %v", err)
	}
	if got != again {
		t.Errorf("userDriftHash not stable: %q vs %q", got, again)
	}

	// Different agent digest must change the hash.
	if h, _ := userDriftHash(dir, "sha256:def"); h == got {
		t.Errorf("agent digest change did not affect hash")
	}

	// Editing a helper file must change the hash.
	must(os.WriteFile(filepath.Join(dir, "helper.sh"), []byte("#!/bin/sh\necho changed\n"), 0o644))
	if h, _ := userDriftHash(dir, "sha256:abc"); h == got {
		t.Errorf("helper edit did not affect hash")
	}

	// Adding a new file must change the hash.
	must(os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("more"), 0o644))
	beforeDelete, err := userDriftHash(dir, "sha256:abc")
	if err != nil {
		t.Fatalf("userDriftHash with extra: %v", err)
	}
	if beforeDelete == got {
		t.Errorf("new file did not affect hash")
	}

	// Deleting an existing file must change the hash.
	must(os.Remove(filepath.Join(dir, "helper.sh")))
	if h, _ := userDriftHash(dir, "sha256:abc"); h == beforeDelete {
		t.Errorf("file delete did not affect hash")
	}
}

func TestEmbeddedDriftHash(t *testing.T) {
	base, err := embeddedDriftHash(container.BaseFS)
	if err != nil {
		t.Fatalf("embeddedDriftHash(BaseFS): %v", err)
	}
	again, err := embeddedDriftHash(container.BaseFS)
	if err != nil {
		t.Fatal(err)
	}
	if base != again {
		t.Errorf("hash is not stable: %q vs %q", base, again)
	}

	fw, err := embeddedDriftHash(container.FirewallFS)
	if err != nil {
		t.Fatalf("embeddedDriftHash(FirewallFS): %v", err)
	}
	if base == fw {
		t.Error("base and firewall contexts hash alike")
	}
}

// A changed file must change the hash; that is what triggers the rebuild.
func TestEmbeddedDriftHashDetectsChange(t *testing.T) {
	before := fstest.MapFS{
		"Containerfile": &fstest.MapFile{Data: []byte("FROM debian\n")},
		"entrypoint.sh": &fstest.MapFile{Data: []byte("#!/bin/sh\n")},
	}
	after := fstest.MapFS{
		"Containerfile": &fstest.MapFile{Data: []byte("FROM debian\n")},
		"entrypoint.sh": &fstest.MapFile{Data: []byte("#!/bin/sh\nexec \"$@\"\n")},
	}
	h1, err := embeddedDriftHash(before)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := embeddedDriftHash(after)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Error("changed entrypoint did not change the hash")
	}
}

// The tags decide which image a run picks up; a wrong prefix silently starts
// the wrong thing.
func TestTags(t *testing.T) {
	a := config.Agent{Name: "pi.dev"}
	if got := AgentTag(a); got != "localhost/cjx-agent-pi.dev:latest" {
		t.Errorf("AgentTag = %q", got)
	}
	if got := UserTag(a); got != "localhost/cjx-user-pi.dev:latest" {
		t.Errorf("UserTag = %q", got)
	}
	if AgentTag(a) == UserTag(a) {
		t.Error("the agent and user layers share a tag")
	}
}
