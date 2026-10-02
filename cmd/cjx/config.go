package main

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/ui"

	"github.com/spf13/cobra"
)

func configCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "View and edit the files under ~/.config/cjx",
		RunE: func(_ *cobra.Command, _ []string) error {
			return configMenu()
		},
	}
}

func configMenu() error {
	agents, err := config.LoadAgents()
	if err != nil {
		return err
	}
	for {
		hosts, err := config.AllowlistHosts()
		if err != nil {
			return err
		}
		servers, err := config.MCPServers()
		if err != nil {
			return err
		}
		idx, err := ui.Pick("Configuration - "+config.Path(), []string{
			fmt.Sprintf("Allowlist (%d)", len(hosts)),
			fmt.Sprintf("MCP servers (%d)", len(servers)),
			"Show configuration",
			"Quit",
		})
		if err != nil {
			return ui.IgnoreUserAbort(err)
		}
		switch idx {
		case 0:
			err = allowlistMenu(hosts, builtinHosts(agents))
		case 1:
			err = mcpMenu(servers)
		case 2:
			err = ui.Note("Configuration", configSummary(agents, hosts, servers))
		case 3:
			return nil
		}
		// Leaving a submenu returns to this one; only a real failure ends the command.
		if err := ui.IgnoreUserAbort(err); err != nil {
			return err
		}
	}
}

// builtinHosts collects the allow_hosts every agent and mode carries, so the
// allowlist editor can reject an entry that would collide with one of them.
func builtinHosts(agents []config.Agent) []string {
	var hosts []string
	for _, a := range agents {
		hosts = append(hosts, a.AllowHosts...)
		for _, m := range a.Modes {
			hosts = append(hosts, m.AllowHosts...)
		}
	}
	return hosts
}

func allowlistMenu(hosts, builtin []string) error {
	title := config.AllowlistPath()
	if len(hosts) > 0 {
		title += "\n\n  " + strings.Join(hosts, "\n  ")
	}
	idx, err := ui.Pick(title, []string{"Add a host", "Remove a host", "Back"})
	if err != nil {
		return err
	}
	switch idx {
	case 0:
		return allowlistAdd(hosts, builtin)
	case 1:
		return allowlistRemove(hosts)
	}
	return nil
}

func allowlistAdd(hosts, builtin []string) error {
	taken := append(slices.Clone(hosts), builtin...)
	host, err := ui.Ask("Host to allow", "registry.npmjs.org",
		func(s string) error { return config.ValidateAllowlistHost(s, taken) })
	if err != nil {
		return err
	}
	if err := config.AddAllowlistHost(host); err != nil {
		return err
	}
	fmt.Printf("Added %s\n", host)
	return nil
}

func allowlistRemove(hosts []string) error {
	if len(hosts) == 0 {
		fmt.Println("Nothing to remove.")
		return nil
	}
	idx, err := ui.Pick("Remove which host?", hosts)
	if err != nil {
		return err
	}
	gone := hosts[idx]
	ok, err := ui.Confirm("Remove " + gone + "?")
	if err != nil || !ok {
		return err
	}
	if err := config.RemoveAllowlistHost(gone); err != nil {
		return err
	}
	fmt.Printf("Removed %s\n", gone)
	return nil
}

func mcpMenu(servers map[string]config.MCPServer) error {
	names := slices.Sorted(maps.Keys(servers))
	title := config.MCPPath()
	for _, n := range names {
		title += fmt.Sprintf("\n  %s: %s", n, servers[n])
	}
	idx, err := ui.Pick(title, []string{"Add a server", "Remove a server", "Back"})
	if err != nil {
		return err
	}
	switch idx {
	case 0:
		return mcpAdd(servers)
	case 1:
		return mcpRemove(names)
	}
	return nil
}

func mcpAdd(servers map[string]config.MCPServer) error {
	name, err := ui.Ask("Server name", "playwright", ui.NonEmpty("a name"))
	if err != nil {
		return err
	}
	command, err := ui.Ask("Command", "npx", ui.NonEmpty("a command"))
	if err != nil {
		return err
	}
	argLine, err := ui.Ask("Arguments, space separated", "@playwright/mcp --headless", nil)
	if err != nil {
		return err
	}
	if _, exists := servers[name]; exists {
		ok, err := ui.Confirm("A server named " + name + " already exists. Replace it?")
		if err != nil || !ok {
			return err
		}
	}
	entry := config.MCPServer{Command: command, Args: strings.Fields(argLine)}
	if err := config.SetMCPServer(name, entry); err != nil {
		return err
	}
	fmt.Printf("Added %s\n", name)
	return nil
}

func mcpRemove(names []string) error {
	if len(names) == 0 {
		fmt.Println("Nothing to remove.")
		return nil
	}
	idx, err := ui.Pick("Remove which server?", names)
	if err != nil {
		return err
	}
	ok, err := ui.Confirm("Remove " + names[idx] + "?")
	if err != nil || !ok {
		return err
	}
	if err := config.RemoveMCPServer(names[idx]); err != nil {
		return err
	}
	fmt.Printf("Removed %s\n", names[idx])
	return nil
}

// configSummary describes what cjx would load right now, across all sources.
func configSummary(
	agents []config.Agent, hosts []string, servers map[string]config.MCPServer,
) string {
	const indent = "             "
	lines := []string{
		"Config dir   " + config.Path(),
		"Cache dir    " + config.CacheDir(),
		"",
		"Agents       built in, plus " + describePath(config.Path(config.LocalAgentsFile)) +
			" (edit by hand)",
	}
	for _, a := range agents {
		modes := make([]string, 0, len(a.Modes))
		for _, m := range a.Modes {
			modes = append(modes, m.Name)
		}
		lines = append(lines, indent+a.Name+": "+strings.Join(modes, ", "))
	}
	layer := config.Path("mcp-server", "Containerfile")
	layerLine := indent + "no extra image layer; add " + layer + " to build one"
	if _, err := os.Stat(layer); err == nil {
		layerLine = indent + "extra image layer from " + layer
	}
	selection := "Selection    none saved yet"
	if sel, ok := config.LoadSelection(); ok {
		selection = "Selection    " + sel.Agent + " / " + sel.Mode
	}
	return strings.Join(append(lines,
		"",
		fmt.Sprintf("Allowlist    %d in %s", len(hosts), describePath(config.AllowlistPath())),
		indent+"agent built-in hosts are merged on top at run time",
		fmt.Sprintf("MCP servers  %d in %s", len(servers), describePath(config.MCPPath())),
		layerLine,
		selection,
	), "\n")
}

// Marks a path cjx would read but that nothing has written yet.
func describePath(path string) string {
	if _, err := os.Stat(path); err != nil {
		return path + " (not created yet)"
	}
	return path
}
