// Package enginetest runs a fake container engine so tests can check what cjx
// asks a runtime to do without a runtime being installed. It follows the
// pattern os/exec uses for its own tests: the test binary re-executes itself as
// the command under test.
package enginetest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	scriptEnv = "CJX_FAKE_ENGINE"
	logEnv    = "CJX_FAKE_LOG"
)

// Main runs the fake engine when this binary was started as one, and the tests
// otherwise. Every test package that calls Fake needs it as its TestMain.
func Main(m *testing.M) {
	if script := os.Getenv(scriptEnv); script != "" {
		os.Exit(reply(script))
	}
	os.Exit(m.Run())
}

// Fake puts this test binary on PATH under both engine names, so every engine
// command in the code under test lands in reply.
//
// A script is "prefix=>output" rules separated by ";". The first rule whose
// prefix matches the arguments wins: its output is printed with "|" as the line
// separator, or, if it is empty, the fake exits non-zero. Arguments matching no
// rule succeed silently. A rule that never matches fails the test. The arrow is
// two characters because a single "=" occurs inside engine arguments.
//
// A prefix may start with "podman " or "docker ", and then only that engine
// answers it - "docker volume exists=>" says the volume is missing over there
// while podman still reports it.
func Fake(t *testing.T, script string) *Calls {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"podman", "docker"} {
		if err := os.Symlink(exe, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	c := &Calls{path: filepath.Join(dir, "calls")}
	t.Setenv("PATH", dir)
	// The fake is a whole test binary; a transfer starts two at once, so keep
	// each one to a single thread.
	t.Setenv("GOMAXPROCS", "1")
	t.Setenv(scriptEnv, script)
	t.Setenv(logEnv, c.path)
	t.Cleanup(func() { c.checkAllMatched(t, script) })
	return c
}

// Calls is what the fake engine was asked to do.
type Calls struct{ path string }

// All returns one line per invocation, arguments joined by spaces.
func (c *Calls) All() []string {
	b, err := os.ReadFile(c.path)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// Got reports whether some invocation started with prefix.
func (c *Calls) Got(prefix string) bool {
	for _, l := range c.All() {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// A rule nothing matched is a test asserting on something that stopped
// happening; cli/cli fails its command stubs the same way.
func (c *Calls) checkAllMatched(t *testing.T, script string) {
	t.Helper()
	for _, rule := range strings.Split(script, ";") {
		if rule == "" {
			continue
		}
		prefix, _, _ := strings.Cut(rule, "=>")
		_, prefix = scoped(prefix)
		if !c.Got(prefix) {
			t.Errorf("nothing asked the engine for %q; calls were %v", prefix, c.All())
		}
	}
}

// scoped splits an engine name off a rule prefix: "docker volume exists" answers
// for docker alone, "volume exists" for whichever engine asks.
func scoped(prefix string) (bin, rest string) {
	name, rest, ok := strings.Cut(prefix, " ")
	if ok && (name == "podman" || name == "docker") {
		return name, rest
	}
	return "", prefix
}

func reply(script string) int {
	// A real command reads what is piped into it; a fake that does not would
	// hand the writing end a broken pipe. With no pipe this returns at once.
	_, _ = io.Copy(io.Discard, os.Stdin)

	args := strings.Join(os.Args[1:], " ")
	if log := os.Getenv(logEnv); log != "" {
		// The path is the t.TempDir that Fake handed us.
		//nolint:gosec // not attacker-controlled
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintln(f, args)
			_ = f.Close()
		}
	}
	self := filepath.Base(os.Args[0])
	for _, rule := range strings.Split(script, ";") {
		prefix, out, _ := strings.Cut(rule, "=>")
		bin, prefix := scoped(prefix)
		if bin != "" && bin != self {
			continue
		}
		if !strings.HasPrefix(args, prefix) {
			continue
		}
		if out == "" {
			return 1
		}
		fmt.Println(strings.ReplaceAll(out, "|", "\n"))
		return 0
	}
	return 0
}
