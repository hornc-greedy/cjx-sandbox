package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// SeedAllowlist writes the user allowlist with a header explaining the wildcard
// rules, unless it exists; Squid rejects a list that names the same domain twice.
func SeedAllowlist() error {
	path := AllowlistPath()
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	t := `# cjx allowlist
# One hostname per line.
#   *.example.com  matches any subdomain AND the apex (api.example.com and example.com)
#   example.com    matches only the apex (exact match)
# Don't list both *.example.com and example.com -- Squid rejects the overlap.
# Built-in agent hosts are added automatically by cjx; this file is only for
# your own additions.
`
	if err := os.WriteFile(path, []byte(t), 0o600); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n", path)
	return nil
}

// AllowlistHosts returns the entries of the user's allowlist, without the
// comments and blank lines the file is seeded with.
func AllowlistHosts() ([]string, error) {
	lines, err := allowlistLines()
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, ln := range lines {
		if h := strings.TrimSpace(ln); h != "" && !strings.HasPrefix(h, "#") {
			hosts = append(hosts, h)
		}
	}
	return hosts, nil
}

// AddAllowlistHost appends host, seeding the file first if it is not there yet.
func AddAllowlistHost(host string) error {
	if err := SeedAllowlist(); err != nil {
		return err
	}
	lines, err := allowlistLines()
	if err != nil {
		return err
	}
	return writeAllowlistLines(append(lines, host))
}

// RemoveAllowlistHost drops every line that names host, leaving comments alone.
func RemoveAllowlistHost(host string) error {
	lines, err := allowlistLines()
	if err != nil {
		return err
	}
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.TrimSpace(ln) == host {
			continue
		}
		kept = append(kept, ln)
	}
	return writeAllowlistLines(kept)
}

// ValidateAllowlistHost reports why host cannot be added. existing must hold the
// user's entries plus every agent's built-in hosts, because Squid refuses a
// dstdomain list that names the same domain twice.
func ValidateAllowlistHost(host string, existing []string) error {
	if host == "" {
		return errors.New("enter a hostname")
	}
	if strings.ContainsAny(host, ": \t") {
		return fmt.Errorf("%q: a hostname carries no port and no spaces", host)
	}
	if strings.HasPrefix(host, "#") {
		return fmt.Errorf("%q: not a hostname", host)
	}
	// "*." collapses to a bare "." and "*.com" to ".com": Squid would allow far
	// more than the entry looks like it does.
	labels := strings.Split(strings.TrimPrefix(host, "*."), ".")
	if len(labels) < 2 || slices.Contains(labels, "") {
		return fmt.Errorf("%q: needs at least a domain and a suffix", host)
	}
	for _, e := range existing {
		if e == host {
			return fmt.Errorf("%q is already allowed", host)
		}
		if covers(e, host) || covers(host, e) {
			return fmt.Errorf("%q overlaps with %q; Squid refuses both at once", host, e)
		}
	}
	return nil
}

// covers reports whether entry already allows host. A "*.d" entry allows d and
// everything below it; a plain entry allows only itself.
func covers(entry, host string) bool {
	host = strings.TrimPrefix(host, "*.")
	if entry == host {
		return true
	}
	d, ok := strings.CutPrefix(entry, "*.")
	if !ok {
		return false
	}
	return host == d || strings.HasSuffix(host, "."+d)
}

func allowlistLines() ([]string, error) {
	data, err := os.ReadFile(AllowlistPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

func writeAllowlistLines(lines []string) error {
	body := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(AllowlistPath(), []byte(body), 0o600)
}
