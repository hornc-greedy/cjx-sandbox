package main

import (
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/image"
	"github.com/hornc-greedy/cjx-sandbox/internal/ui"

	"github.com/spf13/cobra"
)

func migrateCmd() *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "migrate --to <engine>",
		Short: "Copy the agent home volumes into the other container engine",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return migrateHomes(cmd, to)
		},
	}
	cmd.Flags().StringVar(&to, "to", "",
		"`engine` to copy the home volumes into: podman or docker")
	_ = cmd.MarkFlagRequired("to")
	// Tells the menu to offer the two engines instead of an empty input field.
	_ = cmd.Flags().SetAnnotation("to", flagChoices, []string{engine.Podman, engine.Docker})
	return cmd
}

// migrateHomes copies the home volumes you pick into the other engine, in
// whichever direction you are going and however often.
func migrateHomes(cmd *cobra.Command, to string) error {
	ctx := cmd.Context()
	// Examine the source before probing the target: with nothing to copy, the
	// answer is that, not how to install the target engine.
	from, err := engine.Other(to)
	if err != nil {
		return err
	}
	if !engine.IsInstalled(from) {
		fmt.Printf("%s is not installed, so there is nothing to copy into %s.\n", from, to)
		return nil
	}
	source, err := engine.Detect(ctx, from)
	if err != nil {
		return err
	}
	if err := source.Ready(ctx); err != nil {
		return err
	}
	volumes := source.Names(engine.Volume, config.VolumePrefix)
	if len(volumes) == 0 {
		fmt.Printf("%s has no cjx home volumes to copy.\n", source.Name())
		return nil
	}

	target, err := engine.Detect(ctx, to)
	if err != nil {
		return err
	}
	if err := target.Ready(ctx); err != nil {
		return err
	}

	onTarget := make(map[string]bool, len(volumes))
	for _, v := range volumes {
		onTarget[v] = target.Exists(engine.Volume, v)
	}
	picked, err := chooseVolumes(volumes, onTarget, target.Name())
	if errors.Is(err, ui.ErrAborted) {
		fmt.Println("\nCopied nothing.")
		return nil
	}
	if err != nil {
		return err
	}
	if len(picked) == 0 {
		fmt.Println("Copied nothing.")
		return nil
	}
	ok, err := confirmReplace(picked, onTarget, target.Name())
	if errors.Is(err, ui.ErrAborted) || (err == nil && !ok) {
		fmt.Println("Copied nothing.")
		return nil
	}
	if err != nil {
		return err
	}

	// tar lives in the base image, and the target needs that image to run cjx at
	// all, so building it here costs the user nothing they would not pay later.
	if err := image.EnsureBase(ctx, source); err != nil {
		return err
	}
	if err := image.EnsureBase(ctx, target); err != nil {
		return err
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	for _, v := range picked {
		// Removed rather than unpacked over: the tar would leave whatever the
		// target holds beside it and hand the agent two logins at once.
		if onTarget[v] {
			target.Remove(engine.Volume, v)
		}
		// The target volume starts as the image's own /home/agent and the tar is
		// unpacked over it, so a dotfile the source had deleted reappears.
		if err := target.EnsureHomeVolume(ctx, v, image.Base); err != nil {
			_ = w.Flush()
			return err
		}
		if err := engine.CopyVolume(ctx, source, target, v, image.Base); err != nil {
			_ = w.Flush()
			return err
		}
		_, _ = fmt.Fprintf(w, "%s\tcopied\t%s -> %s\n", v, source.Name(), target.Name())
	}
	_ = w.Flush()
	fmt.Printf("\n%d of %d volume(s) copied. The %s volumes are untouched.\n",
		len(picked), len(volumes), source.Name())
	return nil
}

// chooseVolumes offers every home volume the source has and returns the ticked
// ones.
func chooseVolumes(volumes []string, onTarget map[string]bool, target string) ([]string, error) {
	if !ui.Interactive() {
		return nil, errors.New("migrate needs a terminal to ask which volumes to copy")
	}
	return ui.PickMany("Copy which home volumes to "+target+"?",
		"Each one holds that agent's login, settings and history.",
		migrateOptions(volumes, onTarget, target))
}

// migrateOptions labels a volume the target already has with what copying does
// to it, so the answer is in the line being ticked.
func migrateOptions(volumes []string, onTarget map[string]bool, target string) []ui.Option {
	options := make([]ui.Option, 0, len(volumes))
	for _, v := range volumes {
		label := v
		if onTarget[v] {
			label += " (replaces the one on " + target + ")"
		}
		options = append(options, ui.Option{Label: label, Value: v})
	}
	return options
}

// confirmReplace asks once for the volumes that are about to be thrown away.
func confirmReplace(picked []string, onTarget map[string]bool, target string) (bool, error) {
	n := 0
	for _, v := range picked {
		if onTarget[v] {
			n++
		}
	}
	if n == 0 {
		return true, nil
	}
	return ui.ConfirmNote(fmt.Sprintf("Replace %d volume(s) on %s?", n, target),
		"Their login, settings and history go away with them.")
}
