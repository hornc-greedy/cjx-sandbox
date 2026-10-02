package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/image"
	"github.com/hornc-greedy/cjx-sandbox/internal/ui"

	"github.com/spf13/cobra"
)

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the sandbox images, sessions, and (optionally) your saved agent data",
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := openEngine(cmd)
			if err != nil {
				return err
			}
			// Ask before destroying anything: a prompt that fails here must not
			// leave the images gone and the binary still installed.
			volumes := e.Names(engine.Volume, config.VolumePrefix)
			picked, err := uninstallTargets(volumes)
			if errors.Is(err, ui.ErrAborted) {
				fmt.Println("\nAborted; nothing was removed.")
				return nil
			}
			if err != nil {
				return err
			}

			for _, c := range e.Names(engine.Container, "cjx-firewall-") {
				e.Remove(engine.Container, c)
			}
			for _, c := range e.Names(engine.Container, "cjx-session-") {
				e.Remove(engine.Container, c)
			}
			for _, n := range e.Names(engine.Network, "cjx-net-") {
				e.Remove(engine.Network, n)
			}
			for _, img := range e.Names(engine.Image, "localhost/cjx-user-") {
				e.Remove(engine.Image, img)
			}
			for _, img := range e.Names(engine.Image, "localhost/cjx-agent-") {
				e.Remove(engine.Image, img)
			}
			e.Remove(engine.Image, image.Base)
			e.Remove(engine.Image, image.Firewall)

			if slices.Contains(picked, uninstallState) {
				_ = os.RemoveAll(config.CacheDir())
			}
			if slices.Contains(picked, uninstallVolumes) {
				for _, v := range volumes {
					e.Remove(engine.Volume, v)
				}
			}
			if slices.Contains(picked, uninstallConfig) {
				_ = os.RemoveAll(config.Path())
			}
			removeSelf()
			fmt.Printf("\nUninstall complete for %s. The other engine, if you use "+
				"one, keeps its own images and volumes.\n", e.Name())
			return nil
		},
	}
}

// Values of the uninstall multi-select.
const (
	uninstallState   = "state"
	uninstallVolumes = "volumes"
	uninstallConfig  = "config"
)

// uninstallTargets asks what to delete beyond images, containers and networks.
// Nothing is preselected: every entry here destroys user data.
func uninstallTargets(volumes []string) ([]string, error) {
	opts := []ui.Option{
		{Label: "Cache directory   " + config.CacheDir(), Value: uninstallState},
	}
	if len(volumes) > 0 {
		opts = append(opts, ui.Option{
			Label: fmt.Sprintf(
				"Saved agent data  %d volume(s) with login, settings and history", len(volumes)),
			Value: uninstallVolumes,
		})
	}
	opts = append(opts, ui.Option{
		Label: "Config directory  " + config.Path(), Value: uninstallConfig,
	})
	return ui.PickMany("Uninstall - what else should be removed?",
		"Images, containers, networks and the cjx binary go either way.", opts)
}
