package main

import (
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/internal/config"
)

func testAgents() []config.Agent {
	return []config.Agent{
		{Name: "claude", Display: "Claude Code", Modes: []config.Mode{
			{Name: "Subscription", Display: "Anthropic Subscription"},
			{Name: "Work", Display: "Work"},
		}},
		{Name: "pi", Display: "pi.dev", Modes: []config.Mode{
			{Name: "default", Display: "Default"},
		}},
	}
}

func TestResolveSelection(t *testing.T) {
	// HOME too: os.UserCacheDir ignores XDG_CACHE_HOME on macOS and uses
	// $HOME/Library/Caches, so HOME is what isolates the test there.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	agents := testAgents()

	// Saved selection is reused when no arg and no --select.
	config.SaveSelection(config.Agent{Name: "claude"}, config.Mode{Name: "Work"})
	a, m, err := resolveSelection(agents, "", false)
	if err != nil || a.Name != "claude" || m.Name != "Work" {
		t.Errorf("resolveSelection (saved) = %v/%v err=%v", a.Name, m.Name, err)
	}

	// --select ignores the saved selection. With a single agent/mode the
	// prompts auto-resolve, so a saved value pointing elsewhere must not win.
	solo := []config.Agent{{
		Name: "solo", Display: "Solo",
		Modes: []config.Mode{{Name: "only", Display: "Only"}},
	}}
	a, m, err = resolveSelection(solo, "", true)
	if err != nil || a.Name != "solo" || m.Name != "only" {
		t.Errorf("resolveSelection (--select) = %v/%v err=%v", a.Name, m.Name, err)
	}

	// A positional arg naming another agent ignores the saved selection.
	config.SaveSelection(config.Agent{Name: "claude"}, config.Mode{Name: "Work"})
	a, m, err = resolveSelection(agents, "pi", false)
	if err != nil || a.Name != "pi" || m.Name != "default" {
		t.Errorf("resolveSelection (other agent) = %v/%v err=%v", a.Name, m.Name, err)
	}

	// Naming the saved agent keeps its mode. claude has two, so without this the
	// call would open a menu - and there is no terminal here to open it on.
	a, m, err = resolveSelection(agents, "claude", false)
	if err != nil || a.Name != "claude" || m.Name != "Work" {
		t.Errorf("resolveSelection (saved agent named) = %v/%v err=%v", a.Name, m.Name, err)
	}

	// A saved selection that no longer exists falls back to prompting.
	config.SaveSelection(config.Agent{Name: "claude"}, config.Mode{Name: "Work"})
	a, m, err = resolveSelection(solo, "", false)
	if err != nil || a.Name != "solo" || m.Name != "only" {
		t.Errorf("resolveSelection (stale fallback) = %v/%v err=%v", a.Name, m.Name, err)
	}
}
