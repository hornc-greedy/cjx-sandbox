package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/hornc-greedy/cjx-sandbox/container"

	"github.com/BurntSushi/toml"
)

// VolumePrefix is the shared prefix of every per-mode home volume.
const VolumePrefix = "cjx-home"

// Default container resource limits.
const (
	defaultCPUs   = 2
	defaultMemory = "4g"
	defaultPIDs   = 512
)

// Agent maps to one [[agent]] block in the built-in list or agents.local.toml.
type Agent struct {
	Name        string   `toml:"name"`
	Display     string   `toml:"display"`
	Binary      string   `toml:"binary"`
	Install     string   `toml:"install"`
	Packages    []string `toml:"packages"`
	Mcp         bool     `toml:"mcp"`
	NeedsUserns bool     `toml:"needs_userns"`
	EnvPrefixes []string `toml:"env_prefixes"`
	AllowHosts  []string `toml:"allow_hosts"`
	Volume      string   `toml:"volume"`
	CPUs        int      `toml:"cpus"`
	Memory      string   `toml:"memory"`
	PIDs        int      `toml:"pids"`
	Modes       []Mode   `toml:"mode"`
}

// Mode maps to one [[agent.mode]] block.
type Mode struct {
	Name       string   `toml:"name"`
	Display    string   `toml:"display"`
	AllowHosts []string `toml:"allow_hosts"`
	Volume     string   `toml:"volume"`
	CPUs       int      `toml:"cpus"`
	Memory     string   `toml:"memory"`
	PIDs       int      `toml:"pids"`
}

// Settings are the per-run values left after mode, agent and the built-in
// defaults have been folded together.
type Settings struct {
	Volume string
	CPUs   int
	Memory string
	PIDs   int
}

// Resolve folds mode over agent over the built-in defaults. Volume stays ""
// when neither the mode nor the agent names one.
func Resolve(a Agent, m Mode) Settings {
	r := Settings{
		Volume: m.Volume,
		CPUs:   m.CPUs,
		Memory: m.Memory,
		PIDs:   m.PIDs,
	}
	if r.Volume == "" {
		r.Volume = a.Volume
	}
	if r.CPUs == 0 {
		r.CPUs = a.CPUs
	}
	if r.CPUs == 0 {
		r.CPUs = defaultCPUs
	}
	if r.Memory == "" {
		r.Memory = a.Memory
	}
	if r.Memory == "" {
		r.Memory = defaultMemory
	}
	if r.PIDs == 0 {
		r.PIDs = a.PIDs
	}
	if r.PIDs == 0 {
		r.PIDs = defaultPIDs
	}
	return r
}

// VolumeName returns the per-mode home volume. The slug is never empty;
// validateAgents rejects an agent without one.
func VolumeName(a Agent, m Mode) string {
	return VolumePrefix + "-" + Resolve(a, m).Volume
}

// KnownVolumes is the set of home volumes the current config maps to. Anything
// else under the volume prefix belongs to an agent or mode that is gone.
func KnownVolumes(agents []Agent) map[string]bool {
	known := make(map[string]bool, len(agents))
	for _, a := range agents {
		for _, m := range a.Modes {
			known[VolumeName(a, m)] = true
		}
	}
	return known
}

// nameRE matches the container and volume names Podman and Docker accept.
var nameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// validateAgents rejects names an engine would refuse, an agent or mode defined
// twice, and a home volume two modes would share.
func validateAgents(agents []Agent) error {
	if len(agents) == 0 {
		return errors.New("no agents defined")
	}
	check := func(kind, value string, optional bool) error {
		if optional && value == "" {
			return nil
		}
		if !nameRE.MatchString(value) {
			return fmt.Errorf("invalid %s %q (must match %s)", kind, value, nameRE)
		}
		return nil
	}
	// Modes may share their agent's volume, but a volume a mode names itself is
	// that mode's alone, since two logins in one home would overwrite each other.
	type use struct {
		agent, mode string
		own         bool
	}
	uses := map[string]use{}
	names := map[string]bool{}
	for _, a := range agents {
		if err := check("agent name", a.Name, false); err != nil {
			return err
		}
		if names[a.Name] {
			return fmt.Errorf("agent %q is defined twice", a.Name)
		}
		names[a.Name] = true
		if err := check("agent volume", a.Volume, true); err != nil {
			return err
		}
		if len(a.Modes) == 0 {
			return fmt.Errorf("agent %q: no modes defined", a.Name)
		}
		seen := map[string]bool{}
		for _, m := range a.Modes {
			if err := check("mode name", m.Name, false); err != nil {
				return err
			}
			if err := check("mode volume", m.Volume, true); err != nil {
				return err
			}
			if a.Volume == "" && m.Volume == "" {
				return fmt.Errorf(
					"agent %q mode %q: volume must be set on agent or mode", a.Name, m.Name)
			}
			if seen[m.Name] {
				return fmt.Errorf("agent %q: mode %q is defined twice", a.Name, m.Name)
			}
			seen[m.Name] = true
			vol := VolumeName(a, m)
			here := use{agent: a.Name, mode: m.Name, own: m.Volume != ""}
			if prev, ok := uses[vol]; ok && (prev.agent != a.Name || prev.own || here.own) {
				return fmt.Errorf("agent %q mode %q: volume %s is already used by %s/%s",
					a.Name, m.Name, vol, prev.agent, prev.mode)
			}
			uses[vol] = here
		}
	}
	return nil
}

// LocalAgentsFile adds agents, or modes of a built-in agent, to the built-in list.
const LocalAgentsFile = "agents.local.toml"

// LoadAgents reads the built-in agents and adds what agents.local.toml defines.
func LoadAgents() ([]Agent, error) {
	agents, err := decodeAgents("built-in default-agents.toml", container.DefaultAgents)
	if err != nil {
		return nil, err
	}
	path := Path(LocalAgentsFile)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		local, err := decodeAgents(path, data)
		if err != nil {
			return nil, err
		}
		keys, err := agentKeys(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if agents, err = mergeAgents(agents, local, keys); err != nil {
			return nil, err
		}
	}
	if err := validateAgents(agents); err != nil {
		return nil, err
	}
	return agents, nil
}

func decodeAgents(path string, data []byte) ([]Agent, error) {
	var cfg struct {
		Agents []Agent `toml:"agent"`
	}
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// A key cjx does not know is a typo: the setting it was meant to be silently
	// does not apply, and for allow_hosts that means a firewall that blocks.
	if unknown := md.Undecoded(); len(unknown) > 0 {
		keys := make([]string, 0, len(unknown))
		for _, k := range unknown {
			keys = append(keys, k.String())
		}
		return nil, fmt.Errorf("%s: unknown key(s): %s", path, strings.Join(keys, ", "))
	}
	return cfg.Agents, nil
}

// agentKeys lists the keys each [[agent]] block sets, in file order. A key set
// to its zero value, such as mcp = false, counts as set.
func agentKeys(data []byte) ([][]string, error) {
	var raw struct {
		Agents []map[string]any `toml:"agent"`
	}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, err
	}
	keys := make([][]string, len(raw.Agents))
	for i, a := range raw.Agents {
		keys[i] = slices.Sorted(maps.Keys(a))
	}
	return keys, nil
}

// mergeAgents appends a new agent whole. A built-in agent only takes modes, each
// under a name of its own, so a copy of a built-in mode cannot hold back a release.
// keys holds what each local block sets.
func mergeAgents(builtin, local []Agent, keys [][]string) ([]Agent, error) {
	agents := slices.Clone(builtin)
	for n, l := range local {
		i := slices.IndexFunc(builtin, func(a Agent) bool { return a.Name == l.Name })
		if i < 0 {
			agents = append(agents, l)
			continue
		}
		for _, k := range keys[n] {
			if k != "name" && k != "mode" {
				return nil, fmt.Errorf("%s: agent %q is built in, so it takes only "+
					"[[agent.mode]] blocks, not %s", LocalAgentsFile, l.Name, k)
			}
		}
		for _, m := range l.Modes {
			taken := func(b Mode) bool { return b.Name == m.Name }
			if slices.ContainsFunc(builtin[i].Modes, taken) {
				return nil, fmt.Errorf("%s: agent %q already has a built-in mode %q, "+
					"give yours a name of its own", LocalAgentsFile, l.Name, m.Name)
			}
		}
		agents[i].Modes = append(slices.Clone(agents[i].Modes), l.Modes...)
	}
	return agents, nil
}
