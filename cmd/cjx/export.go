package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/image"
	"github.com/hornc-greedy/cjx-sandbox/internal/ui"

	"github.com/spf13/cobra"
)

func exportCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "export [volume]",
		Short: "Write an agent's home volume to an archive",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			volume := ""
			if len(args) == 1 {
				volume = args[0]
			}
			return exportHome(cmd, volume, file)
		},
	}
	cmd.Flags().StringVar(&file, "file", "",
		"`path` to write; default <volume>.tar.gz in the current directory")
	return cmd
}

func exportHome(cmd *cobra.Command, volume, file string) error {
	ctx := cmd.Context()
	e, err := openEngine(cmd)
	if err != nil {
		return err
	}
	volume, err = chooseVolume(e, volume, "Export which home volume?")
	if errors.Is(err, ui.ErrAborted) {
		fmt.Println("\nAborted; nothing was exported.")
		return nil
	}
	if err != nil {
		return err
	}
	if file == "" {
		file, err = archivePath(volume)
		if errors.Is(err, ui.ErrAborted) {
			fmt.Println("\nAborted; nothing was exported.")
			return nil
		}
		if err != nil {
			return err
		}
	}
	// Refuse rather than overwrite: the archive it would replace is someone's backup.
	if _, err := os.Stat(file); err == nil {
		return fmt.Errorf("%s exists; pass --file to write somewhere else", file)
	}
	// tar comes from the base image, the same one the volume's agent runs on.
	if err := image.EnsureBase(ctx, e); err != nil {
		return err
	}
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	err = ui.Spin("Writing "+filepath.Base(file), func() error {
		return engine.ExportVolume(ctx, e, volume, image.Base, f)
	})
	if err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("Wrote %s from %s on %s.\n", file, volume, e.Name())
	return nil
}

// archivePath asks which directory to write the archive into. Without a terminal
// to ask on it lands in the working directory, so a script needs no --file.
func archivePath(volume string) (string, error) {
	name := volume + archiveSuffix
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if !ui.Interactive() {
		return filepath.Join(cwd, name), nil
	}
	dir, err := ui.PickDir("Save "+name+" where?", cwd)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// chooseVolume takes the named volume, or asks which of this engine's home
// volumes to act on.
func chooseVolume(e *engine.Engine, volume, title string) (string, error) {
	volumes := e.Names(engine.Volume, config.VolumePrefix)
	if len(volumes) == 0 {
		return "", fmt.Errorf("%s has no cjx home volumes", e.Name())
	}
	if volume != "" {
		if !slices.Contains(volumes, volume) {
			return "", fmt.Errorf("%s has no home volume %q", e.Name(), volume)
		}
		return volume, nil
	}
	i, err := ui.Pick(title, volumes)
	if err != nil {
		return "", err
	}
	return volumes[i], nil
}
