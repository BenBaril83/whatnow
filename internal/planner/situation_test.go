package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/settings"
)

func TestSituationIsTheLiveStateWithoutTheLastDay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	last, err := f.svc.StartDay(ctx, "2026-10-02", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.WrapDay(ctx, last.ID, "start with the RFC"); err != nil {
		t.Fatal(err)
	}
	f.start(t, 2, "tired")

	sit, err := f.planner.Situation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sit.Model == nil || sit.Config.MainModel != "claude-sonnet-5-5" || sit.Settings.Style != "dry humor" {
		t.Errorf("situation = %+v", sit)
	}
	if !strings.Contains(sit.Context, `Pace today: 2 of 5, "gentle".`) || strings.Contains(sit.Context, "start with the RFC") {
		t.Errorf("context:\n%s", sit.Context)
	}

	f.configured = false
	if sit, err = f.planner.Situation(ctx); err != nil || sit.Model != nil || !errors.Is(sit.ModelErr, settings.ErrNotConfigured) {
		t.Errorf("without a provider: %+v, %v", sit, err)
	}
}
