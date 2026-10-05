package prompt_test

import (
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/prompt"
)

func TestLayersInOrderWithCacheAfterStablePrefix(t *testing.T) {
	blocks := prompt.System("CORE", "be strict", "reviews drain me", "today is Monday")

	if len(blocks) != 4 {
		t.Fatalf("got %d blocks, want 4", len(blocks))
	}
	for i, want := range []string{"CORE", "be strict", "reviews drain me", "today is Monday"} {
		if !strings.HasSuffix(blocks[i].Text, want) {
			t.Errorf("block %d = %q, want it to end with %q", i, blocks[i].Text, want)
		}
	}
	for i, wantCache := range []bool{false, false, true, false} {
		if blocks[i].Cache != wantCache {
			t.Errorf("block %d cache = %v, want %v", i, blocks[i].Cache, wantCache)
		}
	}
}

func TestEmptyLayersAreSkipped(t *testing.T) {
	blocks := prompt.System("CORE", "", "", "")
	if len(blocks) != 1 || blocks[0].Text != "CORE" || !blocks[0].Cache {
		t.Errorf("core only: %+v", blocks)
	}

	blocks = prompt.System("CORE", "", "reviews drain me", "today")
	if len(blocks) != 3 || !blocks[1].Cache || blocks[2].Cache {
		t.Errorf("no style: %+v", blocks)
	}
}
