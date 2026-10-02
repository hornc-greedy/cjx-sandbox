// Package config owns everything cjx keeps on disk: where it lives, the agent
// definitions, the firewall allowlist and the remembered run selection.
package config

import (
	"os"
	"path/filepath"
)

// Path joins parts onto the cjx configuration directory, ~/.config/cjx.
func Path(parts ...string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(append([]string{home, ".config", "cjx"}, parts...)...)
}

// CacheDir returns ~/.cache/cjx, where cjx keeps what it can rebuild: firewall
// session dirs, lock files and the remembered selection.
func CacheDir() string {
	cache, _ := os.UserCacheDir()
	return filepath.Join(cache, "cjx")
}

// AllowlistPath returns the user's own firewall allowlist. Agent built-in hosts
// are merged onto it at run time.
func AllowlistPath() string { return Path("allowlist") }
