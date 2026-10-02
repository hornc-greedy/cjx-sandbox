package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// CopyVolume copies one volume from one engine's store into the other's. The
// stores are separate trees owned by different processes, so the transfer runs
// as a tar streamed between two containers, one per engine. No second copy
// lands on disk.
func CopyVolume(ctx context.Context, from, to *Engine, volume, image string) error {
	read := exec.CommandContext(ctx, from.bin, from.tarReadArgs(volume, image)...)
	write := exec.CommandContext(ctx, to.bin, to.tarWriteArgs(volume, image)...)

	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	read.Stdout, write.Stdin = pw, pr
	var readErr, writeErr bytes.Buffer
	read.Stderr, write.Stderr = &readErr, &writeErr

	if err := read.Start(); err != nil {
		return err
	}
	if err := write.Start(); err != nil {
		_ = read.Process.Kill()
		_ = read.Wait()
		return err
	}
	// Both children hold their own descriptors now. The reader only sees EOF once
	// this process lets go of the write end.
	_ = pw.Close()
	_ = pr.Close()

	rErr, wErr := read.Wait(), write.Wait()
	if rErr != nil {
		return fmt.Errorf("read %s from %s: %w\n%s", volume, from.bin, rErr, readErr.String())
	}
	if wErr != nil {
		return fmt.Errorf("write %s to %s: %w\n%s", volume, to.bin, wErr, writeErr.String())
	}
	return nil
}

// ExportVolume writes a volume's home directory into w as a gzipped tar.
func ExportVolume(ctx context.Context, e *Engine, volume, image string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	cmd := exec.CommandContext(ctx, e.bin, e.tarReadArgs(volume, image)...)
	cmd.Stdout = gz
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("read %s from %s: %w\n%s", volume, e.bin, err, stderr.String())
	}
	return gz.Close()
}

// ImportVolume unpacks what ExportVolume wrote over a volume's home directory.
func ImportVolume(ctx context.Context, e *Engine, volume, image string, r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, e.bin, e.tarWriteArgs(volume, image)...)
	cmd.Stdin = gz
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("write %s to %s: %w\n%s", volume, e.bin, err, stderr.String())
	}
	return nil
}

// VolumeSizes reports what each volume takes on disk, keyed by name. One
// container mounts them all and lets du walk them; the engines' own df output
// differs too much to parse.
func VolumeSizes(ctx context.Context, e *Engine, volumes []string, image string) map[string]string {
	args := make([]string, 0, 4+2*len(volumes)+len(e.identity))
	args = append(args, "run", "--rm")
	args = append(args, e.identity...)
	for _, v := range volumes {
		args = append(args, "-v", v+":/vols/"+v+":ro")
	}
	args = append(args, "--entrypoint", "sh", image, "-c", "du -sh /vols/*")
	out, err := e.Output(ctx, args...)
	if err != nil {
		return nil
	}
	sizes := make(map[string]string, len(volumes))
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		size, path, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		sizes[strings.TrimPrefix(path, "/vols/")] = size
	}
	return sizes
}

// Both ends mount at /home/agent because the image owns it with the uid the
// agent runs as; copy-up then hands tar a directory it may write to.
func (e *Engine) tarReadArgs(volume, image string) []string {
	args := make([]string, 0, 11+len(e.identity))
	args = append(args, "run", "--rm")
	args = append(args, e.identity...)
	return append(args, "-v", volume+":/home/agent:ro",
		"--entrypoint", "tar", image, "-C", "/home/agent", "-cf", "-", ".")
}

func (e *Engine) tarWriteArgs(volume, image string) []string {
	args := make([]string, 0, 12+len(e.identity))
	args = append(args, "run", "--rm", "-i")
	args = append(args, e.identity...)
	return append(args, "-v", volume+":/home/agent",
		"--entrypoint", "tar", image, "-C", "/home/agent", "-xf", "-")
}
