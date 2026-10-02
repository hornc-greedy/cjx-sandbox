package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/image"

	"github.com/spf13/cobra"
)

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "status",
		Short:       "Show sandbox image, volume, and session status",
		Annotations: map[string]string{menuShows: ""},
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := openEngine(cmd)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			defer func() { _ = w.Flush() }()
			// Every row below is one engine's store, and the other engine's is a
			// separate one. Without this line "missing" reads as a defect rather
			// than as "not built over here".
			_, _ = fmt.Fprintf(w, "Engine\t%s\t\n", e.Name())
			imgSizes := e.ImageSizes()
			rows := []struct {
				label, name string
				kind        engine.Object
			}{
				{"Base image", image.Base, engine.Image},
				{"Firewall image", image.Firewall, engine.Image},
			}
			for _, r := range rows {
				status := "missing"
				if e.Exists(r.kind, r.name) {
					status = "present"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.label, status, r.name, imgSizes[r.name])
			}
			agents, err := config.LoadAgents()
			if err != nil {
				_, _ = fmt.Fprintf(w, "Agents\tunreadable\t%v\n", err)
			}
			for _, a := range agents {
				name := image.UserTag(a)
				status := "missing"
				if e.Exists(engine.Image, name) {
					status = "present"
				}
				_, _ = fmt.Fprintf(w, "Agent %s\t%s\t%s\t%s\n",
					a.Name, status, name, imgSizes[name])
			}
			volumes := e.Names(engine.Volume, config.VolumePrefix)
			count := "none"
			if len(volumes) > 0 {
				count = fmt.Sprintf("%d", len(volumes))
			}
			_, _ = fmt.Fprintf(w, "Volumes\t%s\t\t\n", count)
			// du walks the volumes in one container, so it needs the base image.
			var volSizes map[string]string
			if len(volumes) > 0 && e.Exists(engine.Image, image.Base) {
				volSizes = engine.VolumeSizes(cmd.Context(), e, volumes, image.Base)
			}
			// Without a readable agent list every volume would look orphaned.
			known := config.KnownVolumes(agents)
			for _, v := range volumes {
				label := ""
				if err == nil && !known[v] {
					label = "(orphan)"
				}
				_, _ = fmt.Fprintf(w, "\t%s\t%s\t%s\n", label, v, volSizes[v])
			}

			sessions := e.RunningContainers("cjx-session-")
			sessLabel := "none"
			if len(sessions) > 0 {
				sessLabel = strings.Join(sessions, " ")
			}
			_, _ = fmt.Fprintf(w, "Sessions\t%d running\t%s\n", len(sessions), sessLabel)
			return nil
		},
	}
}
