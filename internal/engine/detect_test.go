package engine

import (
	"slices"
	"strings"
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/internal/engine/enginetest"
)

func TestParseSecurityOptions(t *testing.T) {
	cases := map[string]struct {
		out  string
		want securityOptions
	}{
		"rootful daemon": {
			"name=seccomp,profile=builtin\nname=cgroupns\n",
			securityOptions{},
		},
		"rootless": {
			"name=seccomp,profile=builtin\nname=rootless\nname=cgroupns\n",
			securityOptions{rootless: true},
		},
		"userns-remap": {
			"name=userns\nname=seccomp,profile=builtin\n",
			securityOptions{usernsRemap: true},
		},
		"selinux host": {
			"name=selinux\nname=seccomp,profile=builtin\n",
			securityOptions{selinux: true},
		},
		"nothing reported": {"", securityOptions{}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := parseSecurityOptions([]byte(c.out)); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestDetectRejectsUnknownEngine(t *testing.T) {
	if _, err := Detect(t.Context(), "containerd"); err == nil {
		t.Fatal("accepted an engine cjx cannot drive")
	}
}

// Every flag that differs between the runtimes, in one place: a wrong spelling
// here fails at run time on a machine the tests never see.
func TestDockerSpellings(t *testing.T) {
	d, p := docker(), podman()
	cases := []struct {
		what              string
		docker, notPodman string
	}{
		{"image id template", d.imageIDTmpl, p.imageIDTmpl},
		{"no-new-privileges", d.noNewPrivs, p.noNewPrivs},
		{"pull flag", d.pullNewer, p.pullNewer},
		{"exists verb", d.existsVerb, p.existsVerb},
		{"default network", d.defaultNetwork, p.defaultNetwork},
	}
	for _, c := range cases {
		if c.docker == c.notPodman {
			t.Errorf("%s: docker and podman must not share %q", c.what, c.docker)
		}
	}
	if len(d.replaceFlag) != 0 {
		t.Errorf("docker run has no --replace, got %v", d.replaceFlag)
	}
	if len(d.killDelay) != 0 {
		t.Errorf("docker rm has no stop timeout, got %v", d.killDelay)
	}
	if len(d.buildFlags) != 0 {
		t.Errorf("docker build takes neither --format nor --http-proxy, got %v", d.buildFlags)
	}
	if slices.Contains(d.networkFlags, "--disable-dns") {
		t.Errorf("docker network create has no --disable-dns, got %v", d.networkFlags)
	}
	if !slices.Contains(d.networkFlags, "--internal") {
		t.Errorf("the session network must stay internal, got %v", d.networkFlags)
	}
}

// A build must always name its file: Podman finds a Containerfile on its own,
// Docker looks for a Dockerfile and would not find one.
func TestBuildArgsAlwaysNameTheContainerfile(t *testing.T) {
	for _, e := range []*Engine{podman(), docker()} {
		args := e.BuildArgs(Build{Tag: "t", Containerfile: "/ctx/Containerfile", Context: "/ctx"})
		i := slices.Index(args, "-f")
		if i < 0 || args[i+1] != "/ctx/Containerfile" {
			t.Errorf("%s: build does not name the containerfile: %v", e.bin, args)
		}
	}
}

// Docker reads proxy variables from ~/.docker/config.json and has no switch
// against it, so cjx has to overwrite them even when there is no proxy.
func TestProxyArgs(t *testing.T) {
	if got := podman().ProxyArgs(""); got != nil {
		t.Errorf("podman without a proxy should set nothing, got %v", got)
	}
	got := docker().ProxyArgs("")
	if !slices.Contains(got, "HTTP_PROXY=") || !slices.Contains(got, "HTTPS_PROXY=") {
		t.Errorf("docker without a proxy must blank the variables, got %v", got)
	}
	for _, e := range []*Engine{podman(), docker()} {
		got := e.ProxyArgs("http://10.0.0.2:8080")
		if !slices.Contains(got, "HTTP_PROXY=http://10.0.0.2:8080") {
			t.Errorf("%s: proxy not set, got %v", e.bin, got)
		}
		if !slices.Contains(got, "NO_PROXY=localhost,127.0.0.1") {
			t.Errorf("%s: NO_PROXY must stay minimal, got %v", e.bin, got)
		}
	}
}

// Podman takes every network on the run line; Docker takes one and connects the
// rest, and the firewall needs both either way.
func TestNetworkSplit(t *testing.T) {
	nets := []string{"bridge", "cjx-net-x"}
	if got := podman().runNetworks(nets); !slices.Equal(got, nets) {
		t.Errorf("podman run networks: got %v, want %v", got, nets)
	}
	if got := podman().connectNetworks(nets); len(got) != 0 {
		t.Errorf("podman should connect nothing afterwards, got %v", got)
	}
	if got := docker().runNetworks(nets); !slices.Equal(got, nets[:1]) {
		t.Errorf("docker run networks: got %v, want %v", got, nets[:1])
	}
	if got := docker().connectNetworks(nets); !slices.Equal(got, nets[1:]) {
		t.Errorf("docker connect networks: got %v, want %v", got, nets[1:])
	}
	if got := docker().connectNetworks(nil); len(got) != 0 {
		t.Errorf("no networks means nothing to connect, got %v", got)
	}
}

// Only a runtime that relabels gets the suffix; Docker without SELinux would
// reject it.
func TestBindMountFollowsSELinux(t *testing.T) {
	d := docker()
	if got := d.BindMount("/src", "/dst", "ro"); got != "/src:/dst:ro" {
		t.Errorf("without SELinux: got %q, want %q", got, "/src:/dst:ro")
	}
	d.selinuxLabel = true
	if got := d.BindMount("/src", "/dst", "ro"); got != "/src:/dst:ro,Z" {
		t.Errorf("with SELinux: got %q, want %q", got, "/src:/dst:ro,Z")
	}
	if got := d.SharedBindMount("/src", "/dst", "rw"); got != "/src:/dst:rw,z" {
		t.Errorf("shared mount: got %q, want %q", got, "/src:/dst:rw,z")
	}
}

// keep-id maps whatever host uid cjx runs under onto container uid 1000, so a
// Podman home volume is correct as the image left it. Chowning it to a macOS
// uid 501 or a second Linux account's 1001 would take the agent's own home away
// from it.
func TestNeedsHomeChown(t *testing.T) {
	cases := []struct {
		name     string
		engine   *Engine
		uid, gid int
		want     bool
	}{
		{"podman, uid 1000", podman(), 1000, 1000, false},
		{"podman, macOS uid 501 gid 20", podman(), 501, 20, false},
		{"podman, second linux account", podman(), 1001, 1001, false},
		{"docker, uid 1000", docker(), 1000, 1000, false},
		{"docker, macOS uid 501 gid 20", docker(), 501, 20, true},
		{"docker, second linux account", docker(), 1001, 1001, true},
		{"docker, uid 1000 but another group", docker(), 1000, 1001, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.engine.needsHomeChown(c.uid, c.gid); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// The volume must come into being through the mount, not through a prior
// create: a create that outlives a failed chown would leave an empty volume
// that EnsureHomeVolume then skips forever.
func TestChownVolumeArgs(t *testing.T) {
	args := chownVolumeArgs("cjx-home-claude-sub", "localhost/cjx-user-claude", "501:20")
	if slices.Contains(args, "create") {
		t.Errorf("volume created separately from the mount: %v", args)
	}
	if !slices.Contains(args, "cjx-home-claude-sub:/home/agent") {
		t.Errorf("volume not mounted where the image has content: %v", args)
	}
	i := slices.Index(args, "--entrypoint")
	if i < 0 || args[i+1] != "chown" {
		t.Errorf("image entrypoint not bypassed: %v", args)
	}
	if !slices.Contains(args, "0:0") {
		t.Errorf("chown needs root in the container: %v", args)
	}
	if args[len(args)-2] != "501:20" || args[len(args)-1] != "/home/agent" {
		t.Errorf("chown target wrong: %v", args)
	}
}

func TestOther(t *testing.T) {
	for in, want := range map[string]string{Podman: Docker, Docker: Podman} {
		got, err := Other(in)
		if err != nil || got != want {
			t.Errorf("Other(%q) = %q, %v; want %q, nil", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "containerd", "Podman", "docker "} {
		if _, err := Other(bad); err == nil {
			t.Errorf("Other(%q) accepted an engine cjx cannot drive", bad)
		}
	}
}

// Both ends have to mount at /home/agent: that is the only path the image owns
// with the uid the agent runs as, so the runtime's copy-up hands the extracting
// tar a directory it may write to. The source must be read-only.
func TestTarArgsMountWhereTheImageOwnsTheDirectory(t *testing.T) {
	for _, e := range []*Engine{podman(), docker()} {
		e.identity = []string{"--user", "1000:1000"}
		read := e.tarReadArgs("cjx-home-claude-sub", "localhost/cjx-base:latest")
		if !slices.Contains(read, "cjx-home-claude-sub:/home/agent:ro") {
			t.Errorf("%s: source not mounted read-only at the image's home: %v", e.bin, read)
		}
		if !slices.Contains(read, "-cf") || read[len(read)-1] != "." {
			t.Errorf("%s: source does not stream its whole content: %v", e.bin, read)
		}
		write := e.tarWriteArgs("cjx-home-claude-sub", "localhost/cjx-base:latest")
		if !slices.Contains(write, "cjx-home-claude-sub:/home/agent") {
			t.Errorf("%s: target not mounted at the image's home: %v", e.bin, write)
		}
		if !slices.Contains(write, "-i") {
			t.Errorf("%s: target cannot read the stream without -i: %v", e.bin, write)
		}
		for _, args := range [][]string{read, write} {
			i := slices.Index(args, "--entrypoint")
			if i < 0 || args[i+1] != "tar" {
				t.Errorf("%s: image entrypoint not bypassed: %v", e.bin, args)
			}
			if !slices.Contains(args, "--user") {
				t.Errorf("%s: tar does not run as the agent user: %v", e.bin, args)
			}
			if !slices.Contains(args, "--rm") {
				t.Errorf("%s: transfer container outlives the transfer: %v", e.bin, args)
			}
		}
	}
}

// A daemon that answers is accepted; the SELinux report decides whether mounts
// carry a relabel suffix.
func TestNewDockerReadsTheDaemonReport(t *testing.T) {
	enginetest.Fake(t, "info --format=>name=seccomp,profile=builtin|name=selinux")
	e, err := Detect(t.Context(), Docker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.BindMount("/s", "/d", "ro"), ":ro,Z") {
		t.Errorf("SELinux was reported and the mount carries no label: %q",
			e.BindMount("/s", "/d", "ro"))
	}
}

// Two Docker setups cjx cannot sandbox under are refused rather than half-used.
func TestNewDockerRefusesWhatItCannotSandbox(t *testing.T) {
	for name, report := range map[string]string{
		"rootless":     "name=rootless",
		"userns-remap": "name=userns",
	} {
		t.Run(name, func(t *testing.T) {
			enginetest.Fake(t, "info --format=>"+report)
			if _, err := Detect(t.Context(), Docker); err == nil {
				t.Errorf("%s Docker was accepted", name)
			}
		})
	}
}

// An unreachable daemon is an error, not a fallback to Podman.
func TestNewDockerRefusesAnUnreachableDaemon(t *testing.T) {
	enginetest.Fake(t, "info=>")
	if _, err := Detect(t.Context(), Docker); err == nil {
		t.Error("an unreachable daemon was accepted")
	}
}

// With nothing installed there is no engine to fall back to.
func TestDetectWithoutAnyEngine(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := Detect(t.Context(), ""); err == nil {
		t.Error("Detect found an engine on a machine with none")
	}
}

// A home volume from an earlier run still belongs to the image's uid, so being
// there is not enough: what counts is whether the agent can write it.
func TestEnsureHomeVolumeChownsAnUnwritableHome(t *testing.T) {
	c := enginetest.Fake(t, "volume inspect=>ok;run --rm --user 501:20=>")
	e := docker()
	e.hostUIDInContainer = true
	e.identity = []string{"--user", "501:20"}
	if err := e.ensureHomeVolume(t.Context(), "cjx-home-x", "img", 501, 20); err != nil {
		t.Fatal(err)
	}
	if !c.Got("run --rm --user 0:0 --entrypoint chown") {
		t.Errorf("the unwritable home was left alone: %v", c.All())
	}
}

// A home the agent can write is none of cjx's business, however it got there.
func TestEnsureHomeVolumeLeavesAWritableHomeAlone(t *testing.T) {
	c := enginetest.Fake(t, "volume inspect=>ok;run --rm --user 501:20=>ok")
	e := docker()
	e.hostUIDInContainer = true
	e.identity = []string{"--user", "501:20"}
	if err := e.ensureHomeVolume(t.Context(), "cjx-home-x", "img", 501, 20); err != nil {
		t.Fatal(err)
	}
	if c.Got("run --rm --user 0:0 --entrypoint chown") {
		t.Errorf("a writable home was chowned anyway: %v", c.All())
	}
}

// Podman maps whatever host uid it is onto 1000, so it must never touch a home
// volume - not even to look whether one exists.
func TestEnsureHomeVolumeDoesNothingUnderPodman(t *testing.T) {
	c := enginetest.Fake(t, "")
	if err := podman().EnsureHomeVolume(t.Context(), "cjx-home-x", "img"); err != nil {
		t.Fatal(err)
	}
	if len(c.All()) != 0 {
		t.Errorf("podman asked the engine something: %v", c.All())
	}
}

// Under Docker at the image's own uid there is equally nothing to hand over.
func TestEnsureHomeVolumeDoesNothingAtUid1000(t *testing.T) {
	c := enginetest.Fake(t, "")
	e := docker()
	e.hostUIDInContainer = true
	if err := e.ensureHomeVolume(t.Context(), "cjx-home-x", "img", 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if len(c.All()) != 0 {
		t.Errorf("asked the engine something: %v", c.All())
	}
}

// Detect prefers the head of this list, so the order is what makes the menu and
// the fallback agree.
func TestInstalledIsOrderedByPreference(t *testing.T) {
	enginetest.Fake(t, "")
	if got := Installed(); !slices.Equal(got, []string{Podman, Docker}) {
		t.Errorf("Installed = %v, want [%s %s]", got, Podman, Docker)
	}
	t.Setenv("PATH", t.TempDir())
	if got := Installed(); len(got) != 0 {
		t.Errorf("Installed = %v on an empty PATH", got)
	}
}
