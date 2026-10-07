package planner_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/llm"
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

// gate is a model that answers the first hold calls at once with the summary, and holds every
// later call until release closes, then answers with the reflection, or fails if fail is set.
type gate struct {
	noChat
	hold    int32
	fail    bool
	calls   atomic.Int32
	release chan struct{}
}

func (g *gate) Complete(context.Context, llm.Request) (string, error) {
	if g.calls.Add(1) <= g.hold {
		return "They shipped it.", nil
	}
	<-g.release
	if g.fail {
		return "", errors.New("bad key")
	}
	return "You shipped it.", nil
}

// eventually polls cond for up to two seconds.
func eventually(t *testing.T, cond func() bool, what string) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// wrapped closes today with the summary "They shipped it." and no reflection, as a day wrapped
// before reflections existed.
func (f *fixture) wrapped(t *testing.T) {
	t.Helper()
	f.model.replies = append(f.model.replies, "They shipped it.")
	day := f.start(t, 3, "")
	if _, _, err := f.planner.Close(context.Background(), day, nil, ""); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) reflection(t *testing.T) string {
	t.Helper()
	day, err := f.svc.GetDay(context.Background(), today)
	if err != nil {
		t.Fatal(err)
	}
	return day.Reflection
}

func TestTodayFillsAMissingReflectionOnce(t *testing.T) {
	f := newFixture(t)
	f.wrapped(t)
	g := &gate{release: make(chan struct{})}
	f.slow = g
	for range 3 {
		if _, err := f.planner.Today(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	close(g.release)
	eventually(t, func() bool { return f.reflection(t) == "You shipped it." }, "the reflection")
	if _, err := f.planner.Today(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := g.calls.Load(); n != 1 {
		t.Errorf("%d model calls, want 1", n)
	}
}

func TestAFailedFillIsNotRetried(t *testing.T) {
	f := newFixture(t)
	f.wrapped(t)
	g := &gate{fail: true, release: make(chan struct{})}
	close(g.release)
	f.slow = g
	if _, err := f.planner.Today(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return g.calls.Load() == 1 }, "the fill")
	time.Sleep(50 * time.Millisecond) // let the failed fill give the day up
	for range 3 {
		if _, err := f.planner.Today(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := g.calls.Load(); n != 1 {
		t.Errorf("%d model calls, want 1", n)
	}
}

func TestTodayDuringWrapUpStartsNoSecondReflection(t *testing.T) {
	f := newFixture(t)
	g := &gate{hold: 1, release: make(chan struct{})}
	f.slow = g
	f.start(t, 3, "")
	done := make(chan error)
	go func() {
		_, _, err := f.planner.WrapUp(context.Background(), nil, "")
		done <- err
	}()
	eventually(t, func() bool { return g.calls.Load() == 2 }, "wrap-up's reflection call")
	if _, err := f.planner.Today(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := g.calls.Load(); n != 2 {
		t.Errorf("%d model calls, want 2: one summary, one reflection", n)
	}
}

func TestAnEmptyReflectionIsTriedOnceMoreLazily(t *testing.T) {
	f := newFixture(t, "They shipped it.", "   ")
	f.start(t, 3, "")
	if _, wrapped, err := f.planner.WrapUp(context.Background(), nil, ""); err != nil || wrapped.Reflection != "" {
		t.Fatalf("wrapped = %+v, err %v", wrapped, err)
	}
	g := &gate{release: make(chan struct{})}
	close(g.release)
	f.slow = g
	if _, err := f.planner.Today(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return f.reflection(t) == "You shipped it." }, "the second try")
}

func TestADayWithNoSummaryGetsNoReflection(t *testing.T) {
	f := newFixture(t, errors.New("overloaded"))
	day := f.start(t, 3, "")
	if _, _, err := f.planner.Close(context.Background(), day, nil, ""); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := f.planner.Today(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if len(f.model.sent) != 1 {
		t.Errorf("%d model calls, want only the failed summary", len(f.model.sent))
	}
}
