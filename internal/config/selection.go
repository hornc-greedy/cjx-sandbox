package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// A Selection is what the last run was made of: agent, mode and the engine it
// ran on.
type Selection struct {
	Agent  string `json:"agent"`
	Mode   string `json:"mode"`
	Engine string `json:"engine,omitempty"`
}

func selectionPath() string {
	return filepath.Join(CacheDir(), "last-selection.json")
}

func readSelection() Selection {
	var s Selection
	if data, err := os.ReadFile(selectionPath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

// writeSelection is silent about failures: a run must not break because the
// cache dir is unwritable.
func writeSelection(s Selection) {
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := os.MkdirAll(CacheDir(), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(selectionPath(), data, 0o600)
}

// LoadSelection reports ok false when nothing is saved or the file is unreadable;
// a damaged file is not an error, the caller simply prompts again.
func LoadSelection() (Selection, bool) {
	s := readSelection()
	if s.Agent == "" || s.Mode == "" {
		return Selection{}, false
	}
	return s, true
}

// SaveSelection records the choice, leaving the remembered engine alone.
func SaveSelection(a Agent, m Mode) {
	s := readSelection()
	s.Agent, s.Mode = a.Name, m.Name
	writeSelection(s)
}

// SaveEngine records the runtime to use when neither a flag nor CJX_ENGINE names
// one, so a switch made in the menu holds for the next run too.
func SaveEngine(name string) {
	s := readSelection()
	s.Engine = name
	writeSelection(s)
}

// SavedEngine is that runtime, "" when none was ever chosen.
func SavedEngine() string { return readSelection().Engine }

// FindSelection resolves s against the current agents. ok is false once the agent
// or mode has been dropped from the agent list.
func FindSelection(agents []Agent, s Selection) (Agent, Mode, bool) {
	for _, a := range agents {
		if a.Name != s.Agent {
			continue
		}
		for _, m := range a.Modes {
			if m.Name == s.Mode {
				return a, m, true
			}
		}
	}
	return Agent{}, Mode{}, false
}
