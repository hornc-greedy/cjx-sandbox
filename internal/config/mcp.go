package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// An MCPServer is one entry of the mcpServers object in mcp.json. Fields cjx
// does not model survive in every entry except the one SetMCPServer replaces.
type MCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// String renders the server the way a user would have typed it on a shell.
func (s MCPServer) String() string {
	return strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
}

// MCPPath returns the mcp.json cjx merges into the agent on every run.
func MCPPath() string { return Path("mcp-server", "mcp.json") }

// MCPServers returns the configured servers, empty if the file does not exist.
func MCPServers() (map[string]MCPServer, error) {
	_, raw, err := readMCPDoc()
	if err != nil {
		return nil, err
	}
	servers := make(map[string]MCPServer, len(raw))
	for name, entry := range raw {
		var s MCPServer
		// A server cjx cannot read still counts; it just has nothing to show.
		_ = json.Unmarshal(entry, &s)
		servers[name] = s
	}
	return servers, nil
}

// SetMCPServer writes one server, replacing an entry of that name. Keys cjx
// does not model are dropped from that server, the rest of the document is
// carried over untouched.
func SetMCPServer(name string, s MCPServer) error {
	doc, servers, err := readMCPDoc()
	if err != nil {
		return err
	}
	entry, err := json.Marshal(s)
	if err != nil {
		return err
	}
	servers[name] = entry
	return writeMCPDoc(doc, servers)
}

// RemoveMCPServer deletes one server and leaves the rest of the document alone.
func RemoveMCPServer(name string) error {
	doc, servers, err := readMCPDoc()
	if err != nil {
		return err
	}
	delete(servers, name)
	return writeMCPDoc(doc, servers)
}

// Both levels stay raw JSON so editing never drops keys cjx does not model.
func readMCPDoc() (doc, servers map[string]json.RawMessage, err error) {
	doc = map[string]json.RawMessage{}
	servers = map[string]json.RawMessage{}
	data, err := os.ReadFile(MCPPath())
	if errors.Is(err, fs.ErrNotExist) {
		return doc, servers, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", MCPPath(), err)
	}
	if raw, ok := doc["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, nil, fmt.Errorf("parse mcpServers in %s: %w", MCPPath(), err)
		}
	}
	return doc, servers, nil
}

func writeMCPDoc(doc, servers map[string]json.RawMessage) error {
	raw, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	doc["mcpServers"] = raw
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(MCPPath()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(MCPPath(), append(out, '\n'), 0o600)
}
