package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
	"github.com/hornc-greedy/cjx-sandbox/internal/engine"
	"github.com/hornc-greedy/cjx-sandbox/internal/image"
	"github.com/hornc-greedy/cjx-sandbox/internal/ui"

	"github.com/spf13/cobra"
)

const archiveSuffix = ".tar.gz"

func importCmd() *cobra.Command {
	var volume string
	cmd := &cobra.Command{
		Use:   "import [archive]",
		Short: "Restore an agent's home volume from an archive",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := ""
			if len(args) == 1 {
				file = args[0]
			}
			return importHome(cmd, file, volume)
		},
	}
	cmd.Flags().StringVar(&volume, "volume", "",
		"home `volume` to restore into; default the one the archive is named after")
	return cmd
}

func importHome(cmd *cobra.Command, file, volume string) error {
	ctx := cmd.Context()
	if file == "" {
		found, err := chooseArchive()
		if errors.Is(err, ui.ErrAborted) {
			fmt.Println("\nAborted; nothing was imported.")
			return nil
		}
		if err != nil {
			return err
		}
		file = found
	}
	if volume == "" {
		volume = strings.TrimSuffix(filepath.Base(file), archiveSuffix)
	}
	if !strings.HasPrefix(volume, config.VolumePrefix+"-") {
		return fmt.Errorf("%q does not name a cjx home volume; pass --volume", volume)
	}

	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	e, err := openEngine(cmd)
	if err != nil {
		return err
	}
	if e.Exists(engine.Volume, volume) {
		ok, cErr := ui.ConfirmNote("Overwrite "+volume+"?",
			"The archive's files replace the volume's; what it does not carry stays.")
		if cErr != nil {
			return ui.IgnoreUserAbort(cErr)
		}
		if !ok {
			fmt.Println("\nAborted; nothing was imported.")
			return nil
		}
	}
	if err := image.EnsureBase(ctx, e); err != nil {
		return err
	}
	if err := e.EnsureHomeVolume(ctx, volume, image.Base); err != nil {
		return err
	}
	if err := ui.Spin("Restoring "+volume, func() error {
		return engine.ImportVolume(ctx, e, volume, image.Base, f)
	}); err != nil {
		return err
	}
	fmt.Printf("Restored %s into %s on %s.\n", file, volume, e.Name())
	return nil
}

// chooseArchive browses for the directory the archives are in and lists the ones
// it holds; the file picker itself takes directories only.
func chooseArchive() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir, err := ui.PickDir("Import from which directory?", cwd)
	if err != nil {
		return "", err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*"+archiveSuffix))
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no %s archive in %s", archiveSuffix, dir)
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = filepath.Base(f)
	}
	i, err := ui.Pick("Import which archive?", names)
	if err != nil {
		return "", err
	}
	return files[i], nil
}
