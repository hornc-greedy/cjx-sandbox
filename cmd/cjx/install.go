package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/hornc-greedy/cjx-sandbox/internal/ui"

	"github.com/spf13/cobra"
)

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Copy the cjx binary into ~/.local/bin",
		// The menu belongs to the binary that was just replaced.
		Annotations: map[string]string{menuEnds: ""},
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := installSelf(); err != nil {
				return err
			}
			fmt.Println("\nInstall complete. Run `cjx run` to start.")
			return nil
		},
	}
}

func installSelf() error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	src, err = filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dst := filepath.Join(home, ".local", "bin", "cjx")
	if src == dst {
		fmt.Printf("cjx already at %s\n", dst)
		return nil
	}
	// 0o755: ~/.local/bin convention; user-bin dirs are world-traversable.
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	// Write beside the target and rename: overwriting a binary that is currently
	// running fails with ETXTBSY, and a failed write must not leave the user with
	// no cjx at all.
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil { //nolint:gosec // executable
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	fmt.Printf("Installed cjx %s at %s\n", Version, dst)
	hintPath(filepath.Dir(dst))
	return nil
}

func hintPath(dir string) {
	if slices.Contains(filepath.SplitList(os.Getenv("PATH")), dir) {
		return
	}
	fmt.Printf("\n%s is not in your PATH. Add this to ~/.bashrc or ~/.zshrc:\n", dir)
	fmt.Printf("    export PATH=\"$HOME/.local/bin:$PATH\"\n")
}

func removeSelf() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dst := filepath.Join(home, ".local", "bin", "cjx")
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		ui.Warnf("warning: remove %s: %v\n", dst, err)
		return
	}
	fmt.Printf("Removed cjx binary at %s\n", dst)
}
