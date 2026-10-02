package main

import (
	"fmt"
	"strings"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/image"

	"github.com/spf13/cobra"
)

func updateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Pull base images and rebuild the sandbox images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			e, err := openEngine(cmd)
			if err != nil {
				return err
			}
			if err := image.BuildBase(ctx, e, e.PullNewer()); err != nil {
				return err
			}
			if err := image.BuildFirewall(ctx, e, e.PullNewer()); err != nil {
				return err
			}
			agents, err := config.LoadAgents()
			if err != nil {
				return err
			}
			// Agents the user never ran have no image; cjx run builds those lazily.
			var todo []config.Agent
			var untouched []string
			for _, a := range agents {
				if e.Exists(engine.Image, image.AgentTag(a)) {
					todo = append(todo, a)
					continue
				}
				untouched = append(untouched, a.Name)
			}
			rebuilt := make([]string, 0, len(todo))
			for i, a := range todo {
				fmt.Printf("\nRebuilding agent %s (%d of %d)\n", a.Name, i+1, len(todo))
				if err := image.BuildAgent(ctx, e, a, e.PullNewer(), "--no-cache"); err != nil {
					return err
				}
				if err := image.BuildUser(ctx, e, a, e.PullNewer(), "--no-cache"); err != nil {
					return err
				}
				rebuilt = append(rebuilt, a.Name)
			}
			// --no-cache rebuilds leave the superseded images dangling; reclaim them.
			image.PruneDangling(ctx, e)
			fmt.Printf("\nUpdate complete for %s.\n", e.Name())
			if len(rebuilt) > 0 {
				fmt.Printf("Rebuilt: %s\n", strings.Join(rebuilt, ", "))
			}
			if len(untouched) > 0 {
				fmt.Printf("Never run, so not built: %s\n", strings.Join(untouched, ", "))
			}
			return nil
		},
	}
}
