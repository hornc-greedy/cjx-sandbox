// Package engine drives the container runtime cjx builds and runs on.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hornc-greedy/cjx-sandbox/internal/ui"
)

// An Engine is one container runtime cjx can drive. Its fields hold the spellings
// that differ between runtimes, so callers assemble arguments without knowing
// which one they are talking to.
type Engine struct {
	bin        string
	installURL string

	defaultNetwork string   // the outbound network the firewall also joins
	imageIDTmpl    string   // --format template naming an image's content id
	noNewPrivs     string   // --security-opt value that blocks setuid escalation
	pullNewer      string   // build flag asking for a fresher base image
	existsVerb     string   // subcommand that answers "is this here?" by exit code
	killDelay      []string // rm flags capping how long a container may take to die
	replaceFlag    []string // run flag that takes over a name already in use
	networkFlags   []string // network create flags that seal the session network
	noProxyInject  []string // run flags that stop the runtime adding proxy variables
	identity       []string // run flags that map the host user onto the container user
	userNamespace  []string // run flags that let the container create a user namespace
	buildFlags     []string

	hostUIDInContainer  bool // container processes carry the host uid unchanged
	attachesAllNetworks bool // takes every --network on the run line, not one
	machineBacked       bool // runs containers in a VM whose clock can drift
	selinuxLabel        bool // relabels bind mounts, so they need a :z/:Z suffix
	injectsProxyEnv     bool // reads proxy variables from its own config, so cjx must
	// set them on every run even when there is no proxy
}

func podman() *Engine {
	return &Engine{
		bin:            "podman",
		installURL:     "https://podman.io",
		defaultNetwork: "podman",
		imageIDTmpl:    "{{.Id}}",
		noNewPrivs:     "no-new-privileges",
		pullNewer:      "--pull=newer",
		existsVerb:     "exists",
		killDelay:      []string{"-t", "1"},
		replaceFlag:    []string{"--replace"},
		// --disable-dns so the agent cannot resolve its way past the proxy.
		networkFlags:  []string{"--internal", "--disable-dns"},
		noProxyInject: []string{"--http-proxy=false"},
		identity:      []string{"--userns", "keep-id:uid=1000,gid=1000"},
		// --format docker keeps a Containerfile's SHELL (pipefail), which OCI drops;
		// --http-proxy=false stops podman injecting its machine config into builds.
		buildFlags:          []string{"--format", "docker", "--http-proxy=false"},
		attachesAllNetworks: true,
		machineBacked:       true,
		selinuxLabel:        true,
	}
}

// docker returns the Docker engine with the fields that do not depend on what
// the daemon reports; newDocker fills in the rest.
func docker() *Engine {
	return &Engine{
		bin:        "docker",
		installURL: "https://docs.docker.com/engine/install/",
		// Docker has no --replace, no rm timeout, no build format switch and no
		// --http-proxy; the empty slices are what those absences look like here.
		defaultNetwork:  "bridge",
		imageIDTmpl:     "{{.ID}}",
		noNewPrivs:      "no-new-privileges:true",
		pullNewer:       "--pull",
		existsVerb:      "inspect",
		networkFlags:    []string{"--internal"},
		injectsProxyEnv: true,
		// A rootful daemon runs the container process under the uid it is given,
		// so the host uid reaches the container as itself.
		hostUIDInContainer: true,
		// The default seccomp profile denies CLONE_NEWUSER to a container without
		// CAP_SYS_ADMIN, and Docker takes the filter off whole or not at all.
		userNamespace: []string{"--security-opt=seccomp=unconfined"},
	}
}

// An Object is a kind of thing an engine keeps: the word its CLI expects in
// `<engine> <object> exists`. Typed so a caller cannot pass a word the engine
// would reject.
type Object string

// The object kinds cjx asks about.
const (
	Image     Object = "image"
	Volume    Object = "volume"
	Network   Object = "network"
	Container Object = "container"
)

// Name reports which runtime cjx is driving.
func (e *Engine) Name() string { return e.bin }

// DefaultNetwork is the network with a route off the host.
func (e *Engine) DefaultNetwork() string { return e.defaultNetwork }

// PullNewer is the build flag that asks for a fresher base image.
func (e *Engine) PullNewer() string { return e.pullNewer }

// SecurityArgs are the flags that hold the sandbox shut. They apply to every
// container cjx starts, the firewall included.
func (e *Engine) SecurityArgs() []string {
	return []string{"--cap-drop=ALL", "--security-opt=" + e.noNewPrivs, "--read-only"}
}

// IdentityArgs map the invoking host user onto the container user, so the agent
// reads and writes the bind-mounted project as that user.
func (e *Engine) IdentityArgs() []string { return e.identity }

// UserNamespaceArgs are the flags an agent needs to create a user namespace:
// codex wraps every command it runs in a bubblewrap sandbox, which needs one.
// Rootless Podman containers already have theirs, so they get no flags.
func (e *Engine) UserNamespaceArgs() []string { return e.userNamespace }

// BindMount returns the -v value for a host path only this container uses. mode
// is "ro" or "rw".
func (e *Engine) BindMount(hostPath, containerPath, mode string) string {
	return e.bindMount(hostPath, containerPath, mode, "Z")
}

// SharedBindMount is BindMount for a path more than one container may relabel.
// A private label would take it away from whichever container mounted it first.
func (e *Engine) SharedBindMount(hostPath, containerPath, mode string) string {
	return e.bindMount(hostPath, containerPath, mode, "z")
}

// A runtime that does not relabel rejects the suffix, so it is left off there.
func (e *Engine) bindMount(hostPath, containerPath, mode, label string) string {
	opts := mode
	if e.selinuxLabel {
		opts += "," + label
	}
	return hostPath + ":" + containerPath + ":" + opts
}

// NetworkCreateArgs creates the session network: no route off the host, and as
// little name resolution as the runtime lets cjx take away.
func (e *Engine) NetworkCreateArgs(name string) []string {
	args := append([]string{"network", "create"}, e.networkFlags...)
	return append(args, name)
}

// NoProxyInjectArgs stop the runtime from adding proxy variables of its own; cjx
// sets them itself, to the session's Squid.
func (e *Engine) NoProxyInjectArgs() []string { return e.noProxyInject }

// ProxyArgs point the container at url. A runtime that has no switch against
// injecting its own values gets the variables set even when url is empty,
// because an unset variable is exactly what it would fill in.
func (e *Engine) ProxyArgs(url string) []string {
	if url == "" && !e.injectsProxyEnv {
		return nil
	}
	// NO_PROXY stays minimal on purpose: the sandbox has no route but Squid,
	// so nothing beyond localhost may bypass it.
	return []string{
		"-e", "HTTP_PROXY=" + url,
		"-e", "HTTPS_PROXY=" + url,
		"-e", "NO_PROXY=localhost,127.0.0.1",
	}
}

// A Build describes one image build. Every field but Context and Tag may be
// empty; they are all strings, so a struct literal is what keeps them from being
// swapped at the call site.
type Build struct {
	Tag           string   // name:tag the result is stored under
	Label         string   // one "key=value" OCI label, omitted when empty
	Containerfile string   // build file, always named because it is not called Dockerfile
	Context       string   // directory the build reads from
	Extra         []string // caller flags such as --no-cache
}

// BuildArgs assembles a build invocation.
func (e *Engine) BuildArgs(b Build) []string {
	args := make([]string, 0, 12+len(b.Extra))
	args = append(args, "build")
	args = append(args, e.buildFlags...)
	if b.Label != "" {
		args = append(args, "--label", b.Label)
	}
	args = append(args, b.Extra...)
	args = append(args, "-t", b.Tag)
	if b.Containerfile != "" {
		args = append(args, "-f", b.Containerfile)
	}
	return append(args, b.Context)
}

// Ready reports whether cjx can drive the engine right now.
func (e *Engine) Ready(ctx context.Context) error {
	if _, err := exec.LookPath(e.bin); err != nil {
		return fmt.Errorf("%s not found in PATH; install from %s", e.bin, e.installURL)
	}
	return e.machineReady(ctx)
}

// machineReady refuses a macOS setup whose VM is stopped, so the user gets that
// sentence instead of a raw runtime error several calls later. Listing machines
// is a local call; the clock sync it guards needs an ssh into the VM.
func (e *Engine) machineReady(ctx context.Context) error {
	if !e.machineBacked || runtime.GOOS != "darwin" {
		return nil
	}
	out, err := exec.CommandContext(ctx,
		e.bin, "machine", "list", "--format", "{{.Running}}").Output()
	if err != nil {
		return nil //nolint:nilerr // a runtime without machines is not a broken one
	}
	// One line per machine. None at all means the runtime is not machine-backed
	// here - a remote CONTAINER_HOST, say - and refusing to run would be wrong.
	states := strings.Fields(string(out))
	if len(states) > 0 && !slices.Contains(states, "true") {
		return errors.New("no podman machine is running; start it with: podman machine start")
	}
	e.syncMachineTime(ctx)
	return nil
}

// Run streams the engine's output straight to the terminal; cancelling ctx kills it.
func (e *Engine) Run(ctx context.Context, args ...string) error {
	c := exec.CommandContext(ctx, e.bin, args...)
	c.WaitDelay = 5 * time.Second
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// RunInteractive runs a container attached to this terminal and returns when it
// exits. WaitDelay bounds how long an unresponsive container keeps cjx alive
// after ctx is cancelled.
func (e *Engine) RunInteractive(ctx context.Context, args ...string) error {
	c := exec.CommandContext(ctx, e.bin, args...)
	c.WaitDelay = 5 * time.Second
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// ImageLabel returns one OCI label of an image, "" if either is missing.
func (e *Engine) ImageLabel(ctx context.Context, image, label string) string {
	out, err := exec.CommandContext(ctx, e.bin, "image", "inspect",
		"--format", "{{index .Config.Labels "+strconv.Quote(label)+"}}", image).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Output runs the engine and returns everything it wrote, stdout and stderr
// merged, so a caller can both report a failure and read what came before it.
func (e *Engine) Output(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, e.bin, args...).CombinedOutput()
}

// Exists reports whether the named image, volume, network or container is there.
func (e *Engine) Exists(kind Object, name string) bool {
	// Stdout stays unset, so an engine that answers by printing the object is as
	// quiet as one that answers by exit code alone.
	return exec.Command(e.bin, string(kind), e.existsVerb, name).Run() == nil
}

// Remove deletes a resource if it is there. A failure warns rather than stops:
// removal is always part of a larger cleanup that should continue.
func (e *Engine) Remove(kind Object, name string) {
	if !e.Exists(kind, name) {
		return
	}
	out, err := exec.Command(e.bin, e.removeArgs(kind, name)...).CombinedOutput()
	if err != nil {
		ui.Warnf("warning: remove %s %s: %v\n%s", kind, name, err, out)
	}
}

// RunDetached starts a container in the background under name and attaches it to
// every network given, replacing a container of that name that is already there.
// args carry the rest of the invocation and end with the image.
func (e *Engine) RunDetached(ctx context.Context, name string, networks, args []string) error {
	if len(e.replaceFlag) == 0 {
		e.Remove(Container, name)
	}
	c := exec.CommandContext(ctx, e.bin, e.detachedArgs(name, networks, args)...)
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return err
	}
	for _, n := range e.connectNetworks(networks) {
		if err := e.Run(ctx, "network", "connect", n, name); err != nil {
			return fmt.Errorf("connect %s to %s: %w", name, n, err)
		}
	}
	return nil
}

func (e *Engine) detachedArgs(name string, networks, args []string) []string {
	full := make([]string, 0, 6+2*len(networks)+len(args))
	full = append(full, "run", "-d")
	full = append(full, e.replaceFlag...)
	full = append(full, "--name", name)
	for _, n := range e.runNetworks(networks) {
		full = append(full, "--network", n)
	}
	return append(full, args...)
}

// runNetworks and connectNetworks split a network list the way the runtime
// wants it: Podman attaches all of them on the run line, Docker takes one there
// and needs a network connect for each of the rest.
func (e *Engine) runNetworks(networks []string) []string {
	if e.attachesAllNetworks || len(networks) == 0 {
		return networks
	}
	return networks[:1]
}

func (e *Engine) connectNetworks(networks []string) []string {
	return networks[len(e.runNetworks(networks)):]
}

// Logs returns the last n lines a container wrote, stdout and stderr merged.
// Output written before a failure is worth more than the error that reading it
// produced, so a failure yields whatever was read.
func (e *Engine) Logs(ctx context.Context, name string, n int) []byte {
	out, _ := exec.CommandContext(ctx, e.bin,
		"logs", "--tail", strconv.Itoa(n), name).CombinedOutput()
	return out
}

// IsRunning reports whether a container of that name is up right now.
func (e *Engine) IsRunning(name string) bool {
	out, err := exec.Command(e.bin, "ps", "-q",
		"--filter", "name="+name, "--filter", "status=running").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func (e *Engine) removeArgs(kind Object, name string) []string {
	args := []string{string(kind), "rm"}
	if kind == Container {
		args = append(args, "-f")
		args = append(args, e.killDelay...)
	}
	return append(args, name)
}

// Names lists the objects of one kind whose name starts with prefix. Containers
// are listed whether or not they are running; RunningContainers asks the
// narrower question.
func (e *Engine) Names(kind Object, prefix string) []string {
	return e.namesFrom(listArgs(kind), prefix)
}

// ImageSizes reports the on-disk size of every image, keyed by tag. Both engines
// take the same template, so no output parsing is needed beyond the split.
func (e *Engine) ImageSizes() map[string]string {
	out, err := exec.Command(e.bin, "image", "ls",
		"--format", "{{.Repository}}:{{.Tag}}\t{{.Size}}").Output()
	if err != nil {
		return nil
	}
	sizes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, size, ok := strings.Cut(line, "\t")
		if ok {
			sizes[name] = size
		}
	}
	return sizes
}

// RunningContainers lists only the containers that are up.
func (e *Engine) RunningContainers(prefix string) []string {
	return e.namesFrom([]string{"ps", "--format", "{{.Names}}"}, prefix)
}

func (e *Engine) namesFrom(listArgs []string, prefix string) []string {
	out, err := exec.Command(e.bin, listArgs...).Output()
	if err != nil {
		return nil
	}
	var names []string
	for _, n := range strings.Fields(string(out)) {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}
	return names
}

// What each kind's listing looks like on the CLI. Podman and Docker agree on
// all four today; they live here so a future divergence has one place to go.
func listArgs(kind Object) []string {
	switch kind {
	case Image:
		return []string{"image", "ls", "--format", "{{.Repository}}:{{.Tag}}"}
	case Volume:
		return []string{"volume", "ls", "--format", "{{.Name}}"}
	case Network:
		return []string{"network", "ls", "--format", "{{.Name}}"}
	case Container:
		return []string{"ps", "-a", "--format", "{{.Names}}"}
	}
	return nil
}

// A suspended macOS VM drifts, and TLS inside the container then fails with a
// certificate that is not yet valid.
func (e *Engine) syncMachineTime(ctx context.Context) {
	if !e.machineBacked || runtime.GOOS != "darwin" {
		return
	}
	timed, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	epoch := strconv.FormatInt(time.Now().Unix(), 10)
	cmd := exec.CommandContext(timed, e.bin, "machine", "ssh", "sudo", "date", "-s", "@"+epoch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		ui.Warnf("warning: could not sync podman machine clock: %v\n%s", err, out)
	}
}

// ImageDigest returns the content-addressable id of an image.
func (e *Engine) ImageDigest(ctx context.Context, name string) (string, error) {
	out, err := exec.CommandContext(ctx, e.bin, "image", "inspect",
		"--format", e.imageIDTmpl, name).Output()
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", name, err)
	}
	digest := strings.TrimSpace(string(out))
	if digest == "" {
		return "", fmt.Errorf("inspect %s: empty digest", name)
	}
	return digest, nil
}

// InspectIP returns the container's IPv4 on the given network.
func (e *Engine) InspectIP(ctx context.Context, container, network string) (string, error) {
	tmpl := "{{(index .NetworkSettings.Networks " + strconv.Quote(network) + ").IPAddress}}"
	out, err := exec.CommandContext(ctx, e.bin, "inspect", "--format", tmpl, container).Output()
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return "", fmt.Errorf("no IP for %s on %s", container, network)
	}
	return ip, nil
}

// The uid and gid the container images give the agent user.
const (
	agentUID = 1000
	agentGID = 1000
)

// needsHomeChown reports whether a fresh home volume would come out owned by
// someone the agent is not. Only a runtime that carries the host uid inside can,
// and only at a uid other than 1000. Podman's keep-id maps any host uid onto
// 1000, so it never does.
func (e *Engine) needsHomeChown(uid, gid int) bool {
	return e.hostUIDInContainer && (uid != agentUID || gid != agentGID)
}

// EnsureHomeVolume hands the home volume to the invoking user. It does nothing
// for a runtime that maps the user itself, and nothing for a volume the agent
// can already write - the chown is for a home that came out of the image owned
// by uid 1000, whether it was created just now or by an earlier run.
func (e *Engine) EnsureHomeVolume(ctx context.Context, name, image string) error {
	return e.ensureHomeVolume(ctx, name, image, os.Getuid(), os.Getgid())
}

func (e *Engine) ensureHomeVolume(ctx context.Context, name, image string, uid, gid int) error {
	if !e.needsHomeChown(uid, gid) {
		return nil
	}
	if e.Exists(Volume, name) && e.homeIsWritable(ctx, name, image) {
		return nil
	}
	owner := strconv.Itoa(uid) + ":" + strconv.Itoa(gid)
	out, err := e.Output(ctx, chownVolumeArgs(name, image, owner)...)
	if err != nil {
		return fmt.Errorf("chown volume %s to %s: %w\n%s", name, owner, err, out)
	}
	return nil
}

// homeIsWritable asks, as the user the agent runs as, whether the volume can be
// written at all. Everything else about it is the agent's business.
func (e *Engine) homeIsWritable(ctx context.Context, name, image string) bool {
	_, err := e.Output(ctx, writableArgs(name, image, e.identity)...)
	return err == nil
}

func writableArgs(volume, image string, identity []string) []string {
	args := make([]string, 0, 9+len(identity))
	args = append(args, "run", "--rm")
	args = append(args, identity...)
	return append(args, "-v", volume+":/home/agent",
		"--entrypoint", "test", image, "-w", "/home/agent")
}

// The mount both creates the volume and copies /home/agent into it, owner
// 1000:1000 included. A separate volume create would survive a failed chown and
// leave an empty volume EnsureHomeVolume then skips forever.
func chownVolumeArgs(volume, image, owner string) []string {
	return []string{
		"run", "--rm", "--user", "0:0",
		"--entrypoint", "chown", "-v", volume + ":/home/agent", image,
		"-R", owner, "/home/agent",
	}
}
