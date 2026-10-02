// Command cjx runs a coding agent in a Podman or Docker sandbox.
package main

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/ui"

	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags "-X main.Version=...".
var Version = "dev" //nolint:gochecknoglobals // -ldflags injection target

func main() {
	if err := run(); err != nil {
		// The menu runs before cobra does, so cobra cannot report its errors.
		ui.Warnf("Error: %v\n", err)
		os.Exit(1)
	}
}

// newRoot wires the command tree.
func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "cjx",
		Short:         "Run a coding agent in a Podman or Docker sandbox",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	// Registration order is the order, in the menu and in --help alike: daily
	// first, then occasional, and the two you run once or regret at the end.
	cobra.EnableCommandSorting = false
	root.PersistentFlags().String("engine", os.Getenv("CJX_ENGINE"),
		"container runtime to use: `podman|docker` (default: podman if installed)")
	root.AddCommand(runCmd(), statusCmd(), configCmd(), updateCmd(),
		pruneCmd(), migrateCmd(), exportCmd(), importCmd(), uninstallCmd(), installCmd())
	return root
}

// Split out from main so the deferred signal-handler cleanup runs before exit.
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Bare `cjx` on a terminal asks what to do; redirected or in CI it prints the
	// help cobra would have printed, so scripts see no menu they cannot answer.
	if len(os.Args) > 1 || !ui.Interactive() {
		return newRoot().ExecuteContext(ctx)
	}

	m := newMenu()
	for {
		// A fresh tree each round: cobra keeps the flag values of the last run.
		root := newRoot()
		args, cmd, err := m.choose(root)
		if err != nil {
			return ui.IgnoreUserAbort(err)
		}
		root.SetArgs(args)
		// A command that only reports is read on a screen of its own, the way
		// cjx config shows what it would load - not scrolled off behind the menu.
		var report bytes.Buffer
		_, shows := cmd.Annotations[menuShows]
		if shows {
			root.SetOut(&report)
		}
		if err := root.ExecuteContext(ctx); err != nil {
			return err
		}
		if shows && report.Len() > 0 {
			if err := ui.Note(cmd.Name(), strings.TrimRight(report.String(), "\n")); err != nil {
				return ui.IgnoreUserAbort(err)
			}
		}
		if _, ends := cmd.Annotations[menuEnds]; ends {
			return nil
		}
	}
}

// openEngine picks the runtime for this command and refuses early if cjx cannot
// drive it, so no command gets halfway before the engine turns out unusable.
func openEngine(cmd *cobra.Command) (*engine.Engine, error) {
	name, _ := cmd.Flags().GetString("engine")
	// The flag defaults to CJX_ENGINE, so an empty name means neither said
	// anything and the last choice from the menu decides.
	if name == "" {
		name = config.SavedEngine()
	}
	e, err := engine.Detect(cmd.Context(), name)
	if err != nil {
		return nil, err
	}
	return e, e.Ready(cmd.Context())
}
