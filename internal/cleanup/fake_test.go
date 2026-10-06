package cleanup_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/llm/fake"
)

// TestTheFakeSplitsACapture guards the end-to-end fake: its cleanup reply must apply as is.
func TestTheFakeSplitsACapture(t *testing.T) {
	f := newFixture(t, func(req llm.Request) (string, error) { return fake.Client{}.Complete(context.Background(), req) })
	captured := f.capture(t, "buy milk and call mom")

	if _, err := f.runner.Clean(context.Background(), captured); err != nil {
		t.Fatal(err)
	}

	tasks, err := f.svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, task := range tasks {
		titles = append(titles, task.Title)
	}
	if diff := cmp.Diff([]string{"buy milk", "call mom"}, titles); diff != "" {
		t.Errorf("titles (-want +got):\n%s", diff)
	}
}
