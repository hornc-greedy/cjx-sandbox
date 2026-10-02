package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Editing must not drop keys cjx does not model, at either JSON level.
func TestSetMCPServerPreservesUnknownFields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(MCPPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{"topLevel":{"keep":"me"},` +
		`"mcpServers":{"legacy":{"command":"node","env":{"T":"x"},"timeout":30}}}`
	if err := os.WriteFile(MCPPath(), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetMCPServer("added", MCPServer{Command: "uvx"}); err != nil {
		t.Fatalf("SetMCPServer: %v", err)
	}

	got, err := MCPServers()
	if err != nil {
		t.Fatalf("MCPServers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("servers after add: got %d, want 2", len(got))
	}
	data, err := os.ReadFile(MCPPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"topLevel"`, `"timeout"`, `"env"`, `"added"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("rewritten mcp.json lost %s:\n%s", want, data)
		}
	}
}

func TestMCPServerString(t *testing.T) {
	cases := map[string]struct {
		server MCPServer
		want   string
	}{
		"command and args": {
			MCPServer{Command: "npx", Args: []string{"@p/mcp", "--headless"}},
			"npx @p/mcp --headless",
		},
		"command only": {MCPServer{Command: "uvx"}, "uvx"},
		"unreadable":   {MCPServer{}, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := c.server.String(); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// A server whose entry cjx cannot decode still has to be listed, or removing it
// would be impossible from the menu.
func TestMCPServersKeepsUndecodableEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(MCPPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	doc := map[string]json.RawMessage{
		"mcpServers": json.RawMessage(`{"odd":{"command":42}}`),
	}
	data, _ := json.Marshal(doc)
	if err := os.WriteFile(MCPPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := MCPServers()
	if err != nil {
		t.Fatalf("MCPServers: %v", err)
	}
	if _, ok := servers["odd"]; !ok {
		t.Errorf("dropped the entry it could not decode: %v", servers)
	}
}
