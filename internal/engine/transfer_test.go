package engine

import (
	"strings"
	"testing"

	"github.com/hornc-greedy/cjx-sandbox/internal/engine/enginetest"
)

// The transfer is a tar streamed between two containers; nothing lands on disk
// in between.
func TestCopyVolume(t *testing.T) {
	c := enginetest.Fake(t, "run --rm=>ok")
	from, to := podman(), podman()
	if err := CopyVolume(t.Context(), from, to, "cjx-home-x", "img"); err != nil {
		t.Fatal(err)
	}
	calls := c.All()
	if len(calls) != 2 {
		t.Fatalf("want a read and a write container, got %v", calls)
	}
	var read, write string
	for _, call := range calls {
		if strings.Contains(call, "-cf") {
			read = call
		}
		if strings.Contains(call, "-xf") {
			write = call
		}
	}
	if read == "" || write == "" {
		t.Fatalf("no reading and writing pair: %v", calls)
	}
	if !strings.Contains(read, "cjx-home-x:/home/agent:ro") {
		t.Errorf("the source is not read-only: %s", read)
	}
	if !strings.Contains(write, "-i ") {
		t.Errorf("the target cannot read the stream: %s", write)
	}
}

// The sizes are read out of one du run over every volume at once.
func TestVolumeSizes(t *testing.T) {
	c := enginetest.Fake(t, "run --rm=>11G\t/vols/cjx-home-claude-subscription"+
		"|74M\t/vols/cjx-home-codex-subscription")
	sizes := VolumeSizes(t.Context(), podman(),
		[]string{"cjx-home-claude-subscription", "cjx-home-codex-subscription"}, "img")

	if sizes["cjx-home-claude-subscription"] != "11G" {
		t.Errorf("claude volume = %q, want 11G: %v", sizes["cjx-home-claude-subscription"], sizes)
	}
	if sizes["cjx-home-codex-subscription"] != "74M" {
		t.Errorf("codex volume = %q, want 74M: %v", sizes["cjx-home-codex-subscription"], sizes)
	}
	call := c.All()[0]
	if !strings.Contains(call, "cjx-home-codex-subscription:/vols/cjx-home-codex-subscription:ro") {
		t.Errorf("the volumes are not mounted read-only under /vols: %s", call)
	}
	if !strings.Contains(call, "du -sh /vols/*") {
		t.Errorf("nothing measured the volumes: %s", call)
	}
}
