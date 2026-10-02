package engine

import (
	"slices"
	"strings"
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/internal/engine/enginetest"
)

func TestBuildArgs(t *testing.T) {
	e := podman()

	t.Run("containerfile is always explicit", func(t *testing.T) {
		// Podman finds Containerfile on its own, but naming it keeps the
		// invocation true for a runtime that only looks for Dockerfile.
		args := e.BuildArgs(Build{Tag: "tag", Containerfile: "/ctx/Containerfile", Context: "/ctx"})
		i := slices.Index(args, "-f")
		if i < 0 || args[i+1] != "/ctx/Containerfile" {
			t.Errorf("no -f in %v", args)
		}
	})

	t.Run("label is omitted when empty", func(t *testing.T) {
		if slices.Contains(e.BuildArgs(Build{Tag: "tag", Context: "/ctx"}), "--label") {
			t.Error("emitted an empty --label")
		}
		args := e.BuildArgs(Build{Tag: "tag", Label: "k=v", Context: "/ctx"})
		i := slices.Index(args, "--label")
		if i < 0 || args[i+1] != "k=v" {
			t.Errorf("label missing from %v", args)
		}
	})

	t.Run("context comes last", func(t *testing.T) {
		args := e.BuildArgs(Build{
			Tag: "tag", Label: "k=v", Containerfile: "/ctx/Containerfile",
			Context: "/ctx", Extra: []string{"--no-cache"},
		})
		if args[len(args)-1] != "/ctx" {
			t.Errorf("context is not the final argument: %v", args)
		}
	})
}

func TestSecurityArgs(t *testing.T) {
	got := podman().SecurityArgs()
	for _, want := range []string{"--cap-drop=ALL", "--read-only"} {
		if !slices.Contains(got, want) {
			t.Errorf("%q missing from %v", want, got)
		}
	}
	if !slices.ContainsFunc(got, func(s string) bool {
		return strings.HasPrefix(s, "--security-opt=no-new-privileges")
	}) {
		t.Errorf("no-new-privileges missing from %v", got)
	}
}

func TestNetworkCreateArgs(t *testing.T) {
	got := podman().NetworkCreateArgs("cjx-net-x")
	if !slices.Contains(got, "--internal") {
		t.Errorf("the session network must have no route out: %v", got)
	}
	if got[len(got)-1] != "cjx-net-x" {
		t.Errorf("name is not the final argument: %v", got)
	}
}

// A bind mount is host:container:options, in that order; a shared one must not
// carry the private label that would lock the other container out.
func TestBindMount(t *testing.T) {
	e := podman()
	if got := e.BindMount("/host", "/workspace", "ro"); got != "/host:/workspace:ro,Z" {
		t.Errorf("got %q", got)
	}
	if e.SharedBindMount("/d", "/d", "rw") == e.BindMount("/d", "/d", "rw") {
		t.Error("a shared mount needs different relabelling than a private one")
	}
}

// The firewall joins two networks and replaces a leftover container of its own
// name; both have to survive into the argument list.
func TestRunDetachedArgs(t *testing.T) {
	e := podman()
	got := e.detachedArgs("cjx-firewall-x", []string{"podman", "cjx-net-x"},
		[]string{"--memory=256m", "localhost/cjx-firewall:latest"})
	want := []string{
		"run", "-d", "--replace", "--name", "cjx-firewall-x",
		"--network", "podman", "--network", "cjx-net-x",
		"--memory=256m", "localhost/cjx-firewall:latest",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// A container that ignores SIGTERM must not hold up teardown; an image or a
// network has nothing to stop, so the flag would be rejected there.
func TestRemoveArgsCapTheKillDelay(t *testing.T) {
	e := podman()
	if got := e.removeArgs("container", "c"); !slices.Contains(got, "-t") {
		t.Errorf("container removal without a kill delay: %v", got)
	}
	if got := e.removeArgs("image", "i"); slices.Contains(got, "-t") {
		t.Errorf("image removal carries a kill delay: %v", got)
	}
}

// Every kind must name its own listing; a missing case would silently list
// nothing and make cjx believe the store is empty.
func TestListArgs(t *testing.T) {
	for _, kind := range []Object{Image, Volume, Network, Container} {
		args := listArgs(kind)
		if len(args) == 0 {
			t.Errorf("%s has no listing", kind)
			continue
		}
		if !slices.Contains(args, "--format") {
			t.Errorf("%s listing prints more than names: %v", kind, args)
		}
	}
}

// The accessors are the one place each runtime difference is spelled; a wrong
// one is a wrong command line.
func TestSpellings(t *testing.T) {
	e := podman()
	cases := map[string]string{
		"Name":           e.Name(),
		"DefaultNetwork": e.DefaultNetwork(),
		"PullNewer":      e.PullNewer(),
	}
	want := map[string]string{
		"Name": "podman", "DefaultNetwork": "podman", "PullNewer": "--pull=newer",
	}
	for k, got := range cases {
		if got != want[k] {
			t.Errorf("%s = %q, want %q", k, got, want[k])
		}
	}
	if !slices.Contains(e.IdentityArgs(), "keep-id:uid=1000,gid=1000") {
		t.Errorf("IdentityArgs: %v", e.IdentityArgs())
	}
	if !slices.Contains(e.NoProxyInjectArgs(), "--http-proxy=false") {
		t.Errorf("NoProxyInjectArgs: %v", e.NoProxyInjectArgs())
	}
}

// Image sizes come from the engine's own template, so only the split is ours.
func TestImageSizes(t *testing.T) {
	enginetest.Fake(t, "image ls=>localhost/cjx-base:latest\t1.21GB"+
		"|localhost/cjx-user-claude:latest\t2.3GB")
	sizes := podman().ImageSizes()

	if sizes["localhost/cjx-base:latest"] != "1.21GB" {
		t.Errorf("base image = %q, want 1.21GB: %v", sizes["localhost/cjx-base:latest"], sizes)
	}
	if sizes["localhost/cjx-user-claude:latest"] != "2.3GB" {
		t.Errorf("agent image = %q, want 2.3GB: %v",
			sizes["localhost/cjx-user-claude:latest"], sizes)
	}
}
