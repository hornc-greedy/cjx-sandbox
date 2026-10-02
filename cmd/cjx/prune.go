package main

import (
	"errors"
	"fmt"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/image"
	"github.com/hornc-greedy/cjx-sandbox/internal/ui"

	"github.com/spf13/cobra"
)

func pruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Remove images and offer to remove volumes that no agent maps to any more",
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := openEngine(cmd)
			if err != nil {
				return err
			}
			agents, err := config.LoadAgents()
			if err != nil {
				return err
			}
			keep := map[string]bool{}
			for _, a := range agents {
				keep[image.AgentTag(a)] = true
				keep[image.UserTag(a)] = true
			}
			removed := 0
			for _, prefix := range []string{"localhost/cjx-agent-", "localhost/cjx-user-"} {
				for _, img := range e.Names(engine.Image, prefix) {
					if keep[img] {
						continue
					}
					e.Remove(engine.Image, img)
					fmt.Printf("Removed orphan image %s\n", img)
					removed++
				}
			}
			if removed == 0 {
				fmt.Printf("No orphan agent images under %s.\n", e.Name())
			}
			return pruneVolumes(e, agents)
		},
	}
}

// pruneVolumes offers to delete home volumes no agent or mode maps to. Nothing
// is preselected: a home volume carries the agent login, settings and history.
func pruneVolumes(e *engine.Engine, agents []config.Agent) error {
	known := config.KnownVolumes(agents)
	var orphans []ui.Option
	for _, v := range e.Names(engine.Volume, config.VolumePrefix) {
		if !known[v] {
			orphans = append(orphans, ui.Option{Label: v, Value: v})
		}
	}
	if len(orphans) == 0 {
		fmt.Println("No orphan home volumes.")
		return nil
	}
	picked, err := ui.PickMany("Orphan home volumes - remove which?",
		"No agent or mode maps to these.\n"+
			"They hold the agent login, settings and history.", orphans)
	if errors.Is(err, ui.ErrAborted) {
		fmt.Println("\nKept every volume.")
		return nil
	}
	if err != nil {
		return err
	}
	for _, v := range picked {
		e.Remove(engine.Volume, v)
		fmt.Printf("Removed orphan volume %s\n", v)
	}
	return nil
}
