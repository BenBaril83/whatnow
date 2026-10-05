package cleanup

import (
	"encoding/json"
	"strings"
	"testing"
)

// Constrained decoding writes fields in schema order, so the order is part of the prompt: a task
// has to exist (ref, title) before anything refers to it, and blockers come last.
func TestSchemaPropertiesComeInWritingOrder(t *testing.T) {
	b, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	inOrder := func(keys ...string) {
		t.Helper()
		last := -1
		for _, k := range keys {
			i := strings.Index(s[last+1:], `"`+k+`"`)
			if i < 0 {
				t.Fatalf("%q missing or out of order in %s", k, s)
			}
			last += 1 + i
		}
	}
	inOrder("original", "keep", "title", "new_tasks", "ref", "title", "events", "title", "starts_at", "blockers", "task", "blocked_by")
}
