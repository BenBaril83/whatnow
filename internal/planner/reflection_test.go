package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWrapUpWritesASummaryThenAReflection(t *testing.T) {
	f := newFixture(t, "They shipped it.", "You shipped it.")
	f.start(t, 3, "")
	ctx := context.Background()

	_, wrapped, err := f.planner.WrapUp(ctx, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if wrapped.Summary != "They shipped it." || wrapped.Reflection != "You shipped it." {
		t.Errorf("wrapped = %+v", wrapped)
	}
	if stored, _ := f.svc.GetDay(ctx, today); stored.Reflection != "You shipped it." {
		t.Errorf("stored reflection = %q", stored.Reflection)
	}
	summary, reflection := f.model.sent[0], f.model.sent[1]
	if strings.Contains(system(summary), "dry humor") {
		t.Error("the summary prompt carries the user's style")
	}
	if !strings.Contains(system(reflection), "dry humor") {
		t.Error("the reflection prompt leaves out the user's style")
	}
	if reflection.User != "They shipped it." {
		t.Errorf("reflection is written from %q, want the summary", reflection.User)
	}
}

func TestCloseWritesNoReflection(t *testing.T) {
	f := newFixture(t, "They shipped it.")
	day := f.start(t, 3, "")
	if _, wrapped, err := f.planner.Close(context.Background(), day, nil, ""); err != nil || wrapped.Reflection != "" {
		t.Errorf("wrapped = %+v, err %v", wrapped, err)
	}
	if len(f.model.sent) != 1 {
		t.Errorf("Close made %d model calls, want 1", len(f.model.sent))
	}
}

func TestAFailedReflectionStillWrapsTheDay(t *testing.T) {
	f := newFixture(t, "They shipped it.", errors.New("overloaded"))
	f.start(t, 3, "")
	_, wrapped, err := f.planner.WrapUp(context.Background(), nil, "")
	if err != nil || wrapped.WrappedAt == nil || wrapped.Summary != "They shipped it." || wrapped.Reflection != "" {
		t.Errorf("wrapped = %+v, err %v", wrapped, err)
	}
}

func TestAFailedSummaryMakesNoReflectionCall(t *testing.T) {
	f := newFixture(t, errors.New("overloaded"))
	f.start(t, 3, "")
	if _, _, err := f.planner.WrapUp(context.Background(), nil, ""); err != nil {
		t.Fatal(err)
	}
	if len(f.model.sent) != 1 {
		t.Errorf("WrapUp made %d model calls, want 1", len(f.model.sent))
	}
}
