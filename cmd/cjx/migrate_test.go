package main

import (
	"strings"
	"testing"
)

// A volume the target already has is offered like any other - migrating back
// and forth is the point - but the line says what ticking it costs.
func TestMigrateOptionsNameWhatTheyReplace(t *testing.T) {
	volumes := []string{"cjx-home-claude-subscription", "cjx-home-codex-subscription"}
	onTarget := map[string]bool{"cjx-home-codex-subscription": true}
	got := migrateOptions(volumes, onTarget, "podman")
	if len(got) != len(volumes) {
		t.Fatalf("offered %d volume(s), want %d: %v", len(got), len(volumes), got)
	}
	if strings.Contains(got[0].Label, "replaces") {
		t.Errorf("a volume podman lacks is labelled as a replacement: %q", got[0].Label)
	}
	if !strings.Contains(got[1].Label, "replaces the one on podman") {
		t.Errorf("label %q does not say it replaces podman's copy", got[1].Label)
	}
	if got[1].Value != volumes[1] {
		t.Errorf("value = %q, want the volume name %q", got[1].Value, volumes[1])
	}
}

// Replacing drops a login, so it takes an answer rather than a tick alone.
func TestConfirmReplaceOnlyAsksAboutVolumesOnTheTarget(t *testing.T) {
	picked := []string{"cjx-home-claude-subscription"}
	ok, err := confirmReplace(picked, map[string]bool{}, "podman")
	if err != nil || !ok {
		t.Errorf("copying into empty space asked anyway: ok=%v err=%v", ok, err)
	}
}

// The list is the whole interface, so a run with nothing to show it on stops
// instead of copying volumes nobody picked.
func TestChooseVolumesWithoutATerminalStops(t *testing.T) {
	_, err := chooseVolumes([]string{"cjx-home-claude-subscription"}, map[string]bool{}, "podman")
	if err == nil {
		t.Fatal("migrate went ahead with no terminal to ask on")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("error does not say what is missing: %v", err)
	}
}
