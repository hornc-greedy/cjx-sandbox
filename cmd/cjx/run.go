package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/firewall"
	"github.com/hornc-greedy/cjx-sandbox/internal/image"
	"github.com/hornc-greedy/cjx-sandbox/internal/ui"

	"github.com/spf13/cobra"
)

func runCmd() *cobra.Command {
	var opts runOpts
	cmd := &cobra.Command{
		Use:   "run [flags] [agent]",
		Short: "Start the sandbox and run a coding agent",
		// The agent had the terminal; dropping back into a menu after it exits
		// is not what the user asked for.
		Annotations: map[string]string{menuEnds: ""},
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			e, err := openEngine(cmd)
			if err != nil {
				return err
			}
			return runAgent(cmd.Context(), e, arg, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.rw, "rw", false,
		"Mount the project directory read-write (default: read-only)")
	cmd.Flags().BoolVar(&opts.shell, "shell", false,
		"Start an interactive bash shell instead of the agent")
	cmd.Flags().BoolVar(&opts.noFirewall, "no-firewall", false,
		"Bypass the firewall and allow unrestricted network access")
	cmd.Flags().BoolVar(&opts.selectMenu, "select", false,
		"Re-prompt for agent and mode, ignoring the saved selection")
	cmd.Flags().StringArrayVar(&opts.addDirs, "add-dir", nil,
		"Mount an extra host `directory` into the sandbox at the same path (repeatable)")
	// Tells the shell completions and the menu that this flag takes a directory.
	_ = cmd.MarkFlagDirname("add-dir")
	return cmd
}

type runOpts struct {
	rw         bool
	shell      bool
	noFirewall bool
	selectMenu bool
	addDirs    []string // extra host dirs, mounted at the same absolute path
}

func runAgent(ctx context.Context, e *engine.Engine, arg string, opts runOpts) error {
	if opts.noFirewall {
		if err := confirmNoFirewall(); err != nil {
			return err
		}
	}

	// Resolve --add-dir paths up front so a bad path fails before cjx builds or starts anything.
	addDirs, err := resolveAddDirs(opts.addDirs)
	if err != nil {
		return err
	}
	opts.addDirs = addDirs

	if err := config.SeedAllowlist(); err != nil {
		return err
	}
	agents, err := config.LoadAgents()
	if err != nil {
		return err
	}
	agent, mode, err := resolveSelection(agents, arg, opts.selectMenu)
	if err != nil {
		return err
	}

	config.SaveSelection(agent, mode)

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	if err := ensureImages(ctx, e, agent); err != nil {
		return err
	}

	if err := carryHomeOver(ctx, e, agent, mode); err != nil {
		return err
	}
	// The volume the run mounts would otherwise come into being owned by the
	// image's uid, which under Docker is not the uid the agent runs as.
	if err := e.EnsureHomeVolume(ctx, config.VolumeName(agent, mode), image.Base); err != nil {
		return err
	}

	mcp, err := mcpConfigPath(agent)
	if err != nil {
		return err
	}

	var session *firewall.Session
	if !opts.noFirewall {
		s, err := firewall.Start(ctx, e, agent, mode, allowlistFor(agent, mode))
		if err != nil {
			return err
		}
		// firewall.Stop is ctx-independent so Ctrl-C still cleans up.
		defer firewall.Stop(s)
		session = s
	}

	if !opts.shell {
		fmt.Println("Starting agent...")
	}

	network, proxyURL := resolveNetwork(session)
	args := buildRunArgs(e, sandboxRun{
		runOpts: opts, Agent: agent, Mode: mode,
		Workdir: cwd, MCPPath: mcp, Network: network, ProxyURL: proxyURL,
	})
	return e.RunInteractive(ctx, args...)
}

// carryHomeOver copies this mode's home volume from the other engine when this
// one has none. Switching engines would otherwise drop the agent into an empty
// home and ask it to sign in again, although the login exists one store over.
func carryHomeOver(ctx context.Context, e *engine.Engine, a config.Agent, m config.Mode) error {
	volume := config.VolumeName(a, m)
	if e.Exists(engine.Volume, volume) {
		return nil
	}
	src := otherEngineWith(ctx, e, volume)
	if src == nil {
		return nil
	}
	if err := image.EnsureBase(ctx, src); err != nil {
		return err
	}
	if err := e.EnsureHomeVolume(ctx, volume, image.Base); err != nil {
		return err
	}
	return ui.Spin("Copying "+volume+" over from "+src.Name(), func() error {
		return engine.CopyVolume(ctx, src, e, volume, image.Base)
	})
}

// otherEngineWith is the engine cjx is not running on, if it is installed, ready
// and carries that volume - nil whenever there is nothing to copy from.
func otherEngineWith(ctx context.Context, e *engine.Engine, volume string) *engine.Engine {
	name, err := engine.Other(e.Name())
	if err != nil || !engine.IsInstalled(name) {
		return nil
	}
	src, err := engine.Detect(ctx, name)
	if err != nil || src.Ready(ctx) != nil || !src.Exists(engine.Volume, volume) {
		return nil
	}
	return src
}

func confirmNoFirewall() error {
	ok, err := ui.ConfirmNote("Continue without the network sandbox?",
		"--no-firewall drops the allow-listed Squid proxy.\n"+
			"The container then reaches any host on the internet.")
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("aborted")
	}
	return nil
}

func ensureImages(ctx context.Context, e *engine.Engine, agent config.Agent) error {
	if err := image.EnsureBase(ctx, e); err != nil {
		return err
	}
	if err := image.EnsureFirewall(ctx, e); err != nil {
		return err
	}
	rebuilt, err := image.EnsureAgent(ctx, e, agent)
	if err != nil {
		return err
	}
	if err := image.EnsureUser(ctx, e, agent); err != nil {
		return err
	}
	// The retag in EnsureUser leaves the superseded user image dangling.
	if rebuilt {
		image.PruneDangling(ctx, e)
	}
	return nil
}

// mcpConfigPath returns the mcp.json to mount, or "" when the agent takes none
// or the file was never created.
func mcpConfigPath(agent config.Agent) (string, error) {
	if !agent.Mcp {
		return "", nil
	}
	p := config.Path("mcp-server", "mcp.json")
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return p, nil
}

// resolveNetwork reports what the sandbox sees of the network: the session's
// network and its Squid, or nothing at all without a firewall.
func resolveNetwork(session *firewall.Session) (network, proxyURL string) {
	if session == nil {
		return "", ""
	}
	return session.Network(), session.ProxyURL()
}

// agentLabels renders one line per agent for the pickers.
func agentLabels(agents []config.Agent) []string {
	labels := make([]string, 0, len(agents))
	for _, a := range agents {
		labels = append(labels, fmt.Sprintf("%s: %s", a.Name, a.Display))
	}
	return labels
}

func selectAgent(agents []config.Agent, arg string) (config.Agent, error) {
	if arg != "" {
		for _, a := range agents {
			if a.Name == arg {
				return a, nil
			}
		}
		return config.Agent{}, fmt.Errorf("unknown agent %q", arg)
	}
	if len(agents) == 1 {
		return agents[0], nil
	}
	idx, err := ui.Pick("Select an agent", agentLabels(agents))
	if err != nil {
		return config.Agent{}, err
	}
	return agents[idx], nil
}

func selectMode(a config.Agent) (config.Mode, error) {
	if len(a.Modes) == 1 {
		return a.Modes[0], nil
	}
	labels := make([]string, 0, len(a.Modes))
	for _, m := range a.Modes {
		labels = append(labels, fmt.Sprintf("%s: %s", m.Name, m.Display))
	}
	idx, err := ui.Pick("Select a mode for "+a.Display, labels)
	if err != nil {
		return config.Mode{}, err
	}
	return a.Modes[idx], nil
}

func resolveSelection(
	agents []config.Agent, arg string, forceMenu bool,
) (config.Agent, config.Mode, error) {
	// A named agent still takes the saved mode, as long as it is that agent's:
	// the menu names one on every run, and asking for the mode again each time
	// would make the saved selection unreachable from there.
	if !forceMenu {
		if s, ok := config.LoadSelection(); ok {
			if a, m, ok := config.FindSelection(agents, s); ok && (arg == "" || arg == a.Name) {
				fmt.Printf("Using saved selection: %s / %s (run with --select to change)\n",
					a.Display, m.Display)
				return a, m, nil
			}
		}
	}
	agent, err := selectAgent(agents, arg)
	if err != nil {
		return config.Agent{}, config.Mode{}, err
	}
	mode, err := selectMode(agent)
	if err != nil {
		return config.Agent{}, config.Mode{}, err
	}
	return agent, mode, nil
}
