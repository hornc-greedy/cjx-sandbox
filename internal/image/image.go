// Package image builds and refreshes the container images cjx runs agents in.
package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hornc-greedy/cjx-sandbox/container"
	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/ui"
)

// Base is what every agent layer builds on, Firewall runs each session's Squid.
const (
	Base     = "localhost/cjx-base:latest"
	Firewall = "localhost/cjx-firewall:latest"
)

// driftHashLabel carries the hash cjx compares against to decide on a rebuild.
const driftHashLabel = "cjx.drift-hash"

// AgentTag names the per-agent image built from the shared base.
func AgentTag(a config.Agent) string {
	return "localhost/cjx-agent-" + a.Name + ":latest"
}

// UserTag names the image a run starts: the agent image with the user's own
// layer on top, or an alias of it.
func UserTag(a config.Agent) string {
	return "localhost/cjx-user-" + a.Name + ":latest"
}

// BuildBase builds the image every agent layer starts from.
func BuildBase(ctx context.Context, e *engine.Engine, extraArgs ...string) error {
	tmp, err := os.MkdirTemp("", "cjx-build-base-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := os.CopyFS(tmp, container.BaseFS); err != nil {
		return err
	}

	hash, err := embeddedDriftHash(container.BaseFS)
	if err != nil {
		return err
	}
	args := e.BuildArgs(engine.Build{
		Tag:           Base,
		Label:         driftHashLabel + "=" + hash,
		Containerfile: filepath.Join(tmp, "Containerfile"),
		Context:       tmp,
		Extra:         extraArgs,
	})
	if err := e.Run(ctx, args...); err != nil {
		return err
	}
	fmt.Printf("Built image %s\n", Base)
	return nil
}

// BuildFirewall builds the Squid image the per-session firewall runs.
func BuildFirewall(ctx context.Context, e *engine.Engine, extraArgs ...string) error {
	tmp, err := os.MkdirTemp("", "cjx-build-fw-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := os.CopyFS(tmp, container.FirewallFS); err != nil {
		return err
	}

	hash, err := embeddedDriftHash(container.FirewallFS)
	if err != nil {
		return err
	}
	fwCtx := filepath.Join(tmp, "firewall")
	args := e.BuildArgs(engine.Build{
		Tag:           Firewall,
		Label:         driftHashLabel + "=" + hash,
		Containerfile: filepath.Join(fwCtx, "Containerfile"),
		Context:       fwCtx,
		Extra:         extraArgs,
	})
	if err := e.Run(ctx, args...); err != nil {
		return err
	}
	fmt.Printf("Built image %s\n", Firewall)
	return nil
}

// BuildAgent builds the agent layer and stamps its drift hash.
func BuildAgent(ctx context.Context, e *engine.Engine, a config.Agent, extraArgs ...string) error {
	baseDigest, err := e.ImageDigest(ctx, Base)
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "cjx-build-agent-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	containerfile := agentContainerfile(a)
	cf := filepath.Join(tmp, "Containerfile")
	// 0o644: build context, container reads, world-readable is fine.
	if err := os.WriteFile(cf, []byte(containerfile), 0o644); err != nil { //nolint:gosec
		return err
	}

	label := driftHashLabel + "=" + driftHash(a, baseDigest, containerfile)
	args := e.BuildArgs(engine.Build{
		Tag: AgentTag(a), Label: label, Containerfile: cf, Context: tmp, Extra: extraArgs,
	})
	if err := e.Run(ctx, args...); err != nil {
		return err
	}
	fmt.Printf("Built image %s\n", AgentTag(a))
	return nil
}

// driftHash hashes the agent definition, generated Containerfile, and base digest.
func driftHash(a config.Agent, baseDigest, containerfile string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "name=%s\n", a.Name)
	_, _ = fmt.Fprintf(h, "binary=%s\n", a.Binary)
	_, _ = fmt.Fprintf(h, "install=%s\n", a.Install)
	_, _ = fmt.Fprintf(h, "packages=%s\n", strings.Join(a.Packages, ","))
	h.Write([]byte("---\n"))
	h.Write([]byte(containerfile))
	h.Write([]byte("---\n"))
	_, _ = fmt.Fprintf(h, "base=%s\n", baseDigest)
	return hex.EncodeToString(h.Sum(nil))
}

// embeddedDriftHash hashes an embedded build context so a cjx binary carrying
// changed image sources rebuilds instead of reusing the image it finds.
func embeddedDriftHash(fsys fs.FS) (string, error) {
	var paths []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		_, _ = fmt.Fprintf(h, "path=%s\n", p)
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return "", err
		}
		h.Write(data)
		h.Write([]byte("---\n"))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// userDriftHash hashes the user build-context tree plus the agent image digest.
func userDriftHash(buildCtxDir, agentDigest string) (string, error) {
	var paths []string
	err := filepath.WalkDir(buildCtxDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		rel, _ := filepath.Rel(buildCtxDir, p)
		_, _ = fmt.Fprintf(h, "path=%s\n", rel)
		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		h.Write(data)
		h.Write([]byte("---\n"))
	}
	_, _ = fmt.Fprintf(h, "agent=%s\n", agentDigest)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// EnsureBase builds the base image on first use and rebuilds it when this
// binary carries different sources than the image was built from.
func EnsureBase(ctx context.Context, e *engine.Engine) error {
	return ensureEmbedded(ctx, e, Base, container.BaseFS, BuildBase)
}

// EnsureFirewall builds the firewall image on first use and rebuilds it when
// this binary carries different sources than the image was built from.
func EnsureFirewall(ctx context.Context, e *engine.Engine) error {
	return ensureEmbedded(ctx, e, Firewall, container.FirewallFS, BuildFirewall)
}

func ensureEmbedded(
	ctx context.Context, e *engine.Engine, name string, fsys fs.FS,
	build func(context.Context, *engine.Engine, ...string) error,
) error {
	if e.Exists(engine.Image, name) {
		want, err := embeddedDriftHash(fsys)
		if err != nil {
			return err
		}
		if want == e.ImageLabel(ctx, name, driftHashLabel) {
			return nil
		}
		fmt.Printf("Rebuilding %s: image sources changed\n", name)
	}
	return build(ctx, e)
}

// EnsureAgent rebuilds the agent image when it is missing or has drifted and
// reports whether it did. The user layer on top is left alone; EnsureUser runs
// next and retags or rebuilds it. Removing it here would pull the image out from
// under a session that is still running on it.
func EnsureAgent(ctx context.Context, e *engine.Engine, a config.Agent) (bool, error) {
	needsBuild := !e.Exists(engine.Image, AgentTag(a))
	if !needsBuild {
		baseDigest, err := e.ImageDigest(ctx, Base)
		if err != nil {
			return false, err
		}
		want := driftHash(a, baseDigest, agentContainerfile(a))
		needsBuild = want != e.ImageLabel(ctx, AgentTag(a), driftHashLabel)
	}
	if !needsBuild {
		return false, nil
	}
	return true, BuildAgent(ctx, e, a)
}

// EnsureUser rebuilds the user layer when its build context or the agent image
// underneath it has changed.
func EnsureUser(ctx context.Context, e *engine.Engine, a config.Agent) error {
	if !e.Exists(engine.Image, UserTag(a)) {
		return BuildUser(ctx, e, a)
	}
	// Nothing can drift for an agent that has no layer of its own.
	if !a.Mcp {
		return e.Run(ctx, "tag", AgentTag(a), UserTag(a))
	}
	userCF := config.Path("mcp-server", "Containerfile")
	if _, err := os.Stat(userCF); errors.Is(err, fs.ErrNotExist) {
		return e.Run(ctx, "tag", AgentTag(a), UserTag(a))
	} else if err != nil {
		return err
	}
	agentDigest, err := e.ImageDigest(ctx, AgentTag(a))
	if err != nil {
		return err
	}
	want, err := userDriftHash(filepath.Dir(userCF), agentDigest)
	if err != nil {
		return err
	}
	if want == e.ImageLabel(ctx, UserTag(a), driftHashLabel) {
		return nil
	}
	return BuildUser(ctx, e, a)
}

// Install runs with HOME=/opt/cjx/stash so it survives the home-volume overlay.
func agentContainerfile(a config.Agent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n\n", Base)
	if len(a.Packages) > 0 {
		b.WriteString("USER root\n")
		fmt.Fprintf(&b,
			"RUN apt-get update && apt-get install -y --no-install-recommends \\\n    %s \\\n",
			strings.Join(a.Packages, " "))
		b.WriteString("    && rm -rf /var/lib/apt/lists/*\n\n")
	}
	if a.Install != "" {
		b.WriteString("USER agent\n")
		b.WriteString("ENV HOME=/opt/cjx/stash NPM_CONFIG_PREFIX=/opt/cjx/stash/.local " +
			"PATH=/opt/cjx/stash/.local/bin:/usr/local/bin:/usr/bin:/bin\n")
		b.WriteString("RUN " + a.Install + "\n\n")
		b.WriteString("ENV HOME=/home/agent NPM_CONFIG_PREFIX=/home/agent/.local " +
			"PATH=/home/agent/.local/bin:/usr/local/bin:/usr/bin:/bin\n")
	}
	// Final USER agent: avoids root leak for packages-only agents.
	b.WriteString("USER agent\n")
	return b.String()
}

// BuildUser stacks the user's own Containerfile on the agent image, or retags
// when there is nothing to stack.
func BuildUser(ctx context.Context, e *engine.Engine, a config.Agent, extraArgs ...string) error {
	// Only mcp = true agents get the shared mcp-server layer; others alias the agent image.
	if !a.Mcp {
		return e.Run(ctx, "tag", AgentTag(a), UserTag(a))
	}
	userCF := config.Path("mcp-server", "Containerfile")
	if _, err := os.Stat(userCF); errors.Is(err, fs.ErrNotExist) {
		return e.Run(ctx, "tag", AgentTag(a), UserTag(a))
	} else if err != nil {
		return err
	}

	agentDigest, err := e.ImageDigest(ctx, AgentTag(a))
	if err != nil {
		return err
	}
	hash, err := userDriftHash(filepath.Dir(userCF), agentDigest)
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "cjx-build-user-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := os.CopyFS(tmp, os.DirFS(filepath.Dir(userCF))); err != nil {
		return err
	}
	userContent, err := os.ReadFile(userCF)
	if err != nil {
		return err
	}
	wrapped := filepath.Join(tmp, "Containerfile.cjx")
	body := []byte(wrapUserContainerfile(a, string(userContent)))
	// 0o644: build context, world-readable is fine.
	if err := os.WriteFile(wrapped, body, 0o644); err != nil { //nolint:gosec
		return err
	}

	args := e.BuildArgs(engine.Build{
		Tag:           UserTag(a),
		Label:         driftHashLabel + "=" + hash,
		Containerfile: wrapped,
		Context:       tmp,
		Extra:         extraArgs,
	})
	return e.Run(ctx, args...)
}

// wrapUserContainerfile prepends FROM <agent-image> and drops any FROM the user wrote.
func wrapUserContainerfile(a config.Agent, userContent string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n", AgentTag(a))
	for _, line := range strings.Split(userContent, "\n") {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "FROM ") {
			continue
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// Scoped by the drift-hash label so only cjx's own orphans are removed.
func pruneDanglingArgs() []string {
	return []string{"image", "prune", "-f", "--filter", "label=" + driftHashLabel}
}

// PruneDangling reclaims the dangling images a rebuild or retag leaves behind
// and prints the space freed. Best-effort: a failure only warns.
func PruneDangling(ctx context.Context, e *engine.Engine) {
	out, err := e.Output(ctx, pruneDanglingArgs()...)
	if err != nil {
		ui.Warnf("warning: prune dangling images: %v\n%s", err, out)
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "Total reclaimed space:") {
			fmt.Println(line)
		}
	}
}
