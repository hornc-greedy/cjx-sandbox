package config

import (
	"os"
	"testing"
)

func testAgents() []Agent {
	return []Agent{
		{Name: "claude", Display: "Claude Code", Modes: []Mode{
			{Name: "Subscription", Display: "Anthropic Subscription"},
			{Name: "Work", Display: "Work"},
		}},
		{Name: "pi", Display: "pi.dev", Modes: []Mode{
			{Name: "default", Display: "Default"},
		}},
	}
}

func TestFindSaved(t *testing.T) {
	agents := testAgents()

	if a, m, ok := FindSelection(agents, Selection{Agent: "claude", Mode: "Work"}); !ok ||
		a.Name != "claude" || m.Name != "Work" {
		t.Errorf("findSaved valid = %v/%v ok=%v", a.Name, m.Name, ok)
	}
	if _, _, ok := FindSelection(agents, Selection{Agent: "gone", Mode: "Work"}); ok {
		t.Error("findSaved should fail for unknown agent")
	}
	if _, _, ok := FindSelection(agents, Selection{Agent: "claude", Mode: "gone"}); ok {
		t.Error("findSaved should fail for unknown mode")
	}
}

func TestSaveLoadSelection(t *testing.T) {
	// HOME too: os.UserCacheDir ignores XDG_CACHE_HOME on macOS and uses
	// $HOME/Library/Caches, so HOME is what isolates the test there.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if _, ok := LoadSelection(); ok {
		t.Error("loadSelection should fail when no file exists")
	}

	SaveSelection(Agent{Name: "claude"}, Mode{Name: "Subscription"})
	s, ok := LoadSelection()
	if !ok || s.Agent != "claude" || s.Mode != "Subscription" {
		t.Errorf("loadSelection after save = %+v ok=%v", s, ok)
	}

	// The engine is remembered beside the selection, and neither overwrites the
	// other: the menu switches engines without touching agent or mode.
	SaveEngine("docker")
	if got := SavedEngine(); got != "docker" {
		t.Errorf("SavedEngine = %q, want docker", got)
	}
	SaveSelection(Agent{Name: "pi"}, Mode{Name: "default"})
	if got := SavedEngine(); got != "docker" {
		t.Errorf("saving a selection dropped the engine: %q", got)
	}
	if s, ok := LoadSelection(); !ok || s.Agent != "pi" {
		t.Errorf("selection after the engine was saved: %+v ok=%v", s, ok)
	}

	// A malformed file is treated as absent, not an error.
	if err := os.WriteFile(selectionPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadSelection(); ok {
		t.Error("loadSelection should fail on malformed file")
	}
}
