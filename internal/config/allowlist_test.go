package config

import (
	"os"
	"strings"
	"testing"
)

func TestValidateAllowlistHost(t *testing.T) {
	existing := []string{"registry.npmjs.org", "*.corp.example.com"}
	cases := []struct {
		name    string
		host    string
		wantErr string
	}{
		{"plain host", "files.pythonhosted.org", ""},
		{"unrelated wildcard", "*.example.org", ""},
		{"bare wildcard", "*.", "domain and a suffix"},
		{"wildcard over a whole tld", "*.com", "domain and a suffix"},
		{"single label", "localhost", "domain and a suffix"},
		{"trailing dot", "example.com.", "domain and a suffix"},
		{"empty", "", "enter a hostname"},
		{"port", "localhost:8080", "no port"},
		{"space", "a b", "no port"},
		{"comment", "#nope", "not a hostname"},
		{"duplicate", "registry.npmjs.org", "already allowed"},
		{"apex under listed wildcard", "corp.example.com", "overlaps"},
		{"subdomain under listed wildcard", "api.corp.example.com", "overlaps"},
		{"deep subdomain under listed wildcard", "a.b.corp.example.com", "overlaps"},
		{"wildcard under listed wildcard", "*.api.corp.example.com", "overlaps"},
		{"wildcard over listed apex", "*.registry.npmjs.org", "overlaps"},
		{"unrelated host sharing a suffix", "corp.example.com.evil.net", ""},
		{"sibling of a listed apex", "other.npmjs.org", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateAllowlistHost(c.host, existing)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateAllowlistHost(%q) = %v, want nil", c.host, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("ValidateAllowlistHost(%q) = %v, want error containing %q",
					c.host, err, c.wantErr)
			}
		})
	}
}

func TestAllowlistHostsSkipsComments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "# header\n\nregistry.npmjs.org\n  *.corp.example.com  \n# trailing\n"
	if err := os.WriteFile(AllowlistPath(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts, err := AllowlistHosts()
	if err != nil {
		t.Fatalf("AllowlistHosts: %v", err)
	}
	want := []string{"registry.npmjs.org", "*.corp.example.com"}
	if len(hosts) != len(want) {
		t.Fatalf("got %v, want %v", hosts, want)
	}
	for i := range want {
		if hosts[i] != want[i] {
			t.Errorf("host %d: got %q, want %q", i, hosts[i], want[i])
		}
	}
}

// Removing an entry must leave the header the file was seeded with in place.
func TestRemoveAllowlistHostKeepsComments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "# header\nregistry.npmjs.org\nfiles.pythonhosted.org\n"
	if err := os.WriteFile(AllowlistPath(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAllowlistHost("registry.npmjs.org"); err != nil {
		t.Fatalf("RemoveAllowlistHost: %v", err)
	}
	data, err := os.ReadFile(AllowlistPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "# header\nfiles.pythonhosted.org\n" {
		t.Errorf("got %q", got)
	}
}
