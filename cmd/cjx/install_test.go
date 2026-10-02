package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The binary lands in ~/.local/bin and is executable; a copy without the mode
// bit is a cjx the shell refuses to start.
func TestInstallSelf(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := installSelf(); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(home, ".local", "bin", "cjx")
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("nothing was installed: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the installed binary is not executable: %v", info.Mode())
	}
	if info.Size() == 0 {
		t.Error("the installed binary is empty")
	}
}

// Installing over an older copy replaces it rather than failing.
func TestInstallSelfOverAnOlderCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dst := filepath.Join(home, ".local", "bin", "cjx")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("an older cjx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installSelf(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil || info.Size() == int64(len("an older cjx")) {
		t.Errorf("the older copy is still there: %v, %v", info, err)
	}
}

// Uninstalling twice is not an error: the second run has nothing to remove.
func TestRemoveSelf(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dst := filepath.Join(home, ".local", "bin", "cjx")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("cjx"), 0o755); err != nil {
		t.Fatal(err)
	}
	removeSelf()
	if _, err := os.Stat(dst); err == nil {
		t.Error("the binary is still there")
	}
	removeSelf()
}
