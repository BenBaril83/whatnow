package integrations_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/integrations"
	"github.com/jameynakama/whatnow/internal/service"
)

// setUpPushed turns the board on and reached from the user's computer.
func (e env) setUpPushed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.runner.SetVia(ctx, "board", integrations.ViaComputer); err != nil {
		t.Fatal(err)
	}
	if _, err := e.runner.SetEnabled(ctx, "board", true); err != nil {
		t.Fatal(err)
	}
}

func TestReceiveThenPullOffersPushedList(t *testing.T) {
	e := newEnv(t)
	e.setUpPushed(t)
	if err := e.runner.Receive(context.Background(), "board", []integrations.Ticket{ticket("FLEX1-1"), ticket("FLEX1-2")}, 3); err != nil {
		t.Fatal(err)
	}

	res := e.pullFrom(t, "board")

	if diff := cmp.Diff([]string{"FLEX1-1", "FLEX1-2"}, offered(res)); diff != "" {
		t.Errorf("offered (-want +got):\n%s", diff)
	}
	src := res.Sources[0]
	if src.Label != "Board" || src.More != 3 || src.SentAt == nil || !src.SentAt.Equal(now) {
		t.Errorf("source = %+v", src)
	}
	if e.board.pulls != 0 {
		t.Error("a pushed pull asked the integration")
	}
}

func TestPushedListIsFiltered(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.setUpPushed(t)
	e.task(t, "on the list", false, "FLEX1-1")
	if _, err := e.runner.Skip(ctx, "board", integrations.Skip{Key: "FLEX1-2", Title: "nope"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.runner.Receive(ctx, "board", []integrations.Ticket{ticket("FLEX1-1"), ticket("FLEX1-2"), ticket("FLEX1-3")}, 0); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"FLEX1-3"}, offered(e.pullFrom(t, "board"))); diff != "" {
		t.Errorf("offered (-want +got):\n%s", diff)
	}
}

func TestComputerModeNothingSent(t *testing.T) {
	e := newEnv(t)
	e.setUpPushed(t)

	src := e.pullFrom(t, "board").Sources[0]

	if src.Error != "Nothing sent from your computer yet — run `whatnow jira push`" || len(src.Tickets) != 0 {
		t.Errorf("source = %+v", src)
	}
}

func TestComputerModeIsReady(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.runner.SetEnabled(ctx, "board", true); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.runner.View(ctx, "board"); v.Ready || v.Via != integrations.ViaDirect {
		t.Errorf("direct view = %+v, want not ready", v)
	}
	v, err := e.runner.SetVia(ctx, "board", integrations.ViaComputer)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Ready || v.Via != integrations.ViaComputer {
		t.Errorf("computer view = %+v, want ready", v)
	}
}

func TestReceiveRefused(t *testing.T) {
	many := make([]integrations.Ticket, 51)
	for i := range many {
		many[i] = ticket(fmt.Sprintf("FLEX1-%d", i))
	}
	bad := func(edit func(*integrations.Ticket)) []integrations.Ticket {
		tk := ticket("FLEX1-1")
		edit(&tk)
		return []integrations.Ticket{tk}
	}
	tests := map[string]struct {
		via     string
		enabled bool
		tickets []integrations.Ticket
		want    error
	}{
		"off":            {integrations.ViaComputer, false, []integrations.Ticket{ticket("FLEX1-1")}, service.ErrConflict},
		"direct":         {integrations.ViaDirect, true, []integrations.Ticket{ticket("FLEX1-1")}, service.ErrConflict},
		"too many":       {integrations.ViaComputer, true, many, service.ErrInvalid},
		"no key":         {integrations.ViaComputer, true, bad(func(tk *integrations.Ticket) { tk.Key = " " }), service.ErrInvalid},
		"no title":       {integrations.ViaComputer, true, bad(func(tk *integrations.Ticket) { tk.Title = "" }), service.ErrInvalid},
		"script address": {integrations.ViaComputer, true, bad(func(tk *integrations.Ticket) { tk.URL = "javascript:alert(1)" }), service.ErrInvalid},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			if _, err := e.runner.SetVia(ctx, "board", tt.via); err != nil {
				t.Fatal(err)
			}
			if _, err := e.runner.SetEnabled(ctx, "board", tt.enabled); err != nil {
				t.Fatal(err)
			}
			if err := e.runner.Receive(ctx, "board", tt.tickets, 0); !errors.Is(err, tt.want) {
				t.Errorf("Receive = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSaveKeepsViaAndPushed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.setUpPushed(t)
	if err := e.runner.Receive(ctx, "board", []integrations.Ticket{ticket("FLEX1-1")}, 0); err != nil {
		t.Fatal(err)
	}

	v, err := e.runner.Save(ctx, "board", integrations.Update{Values: map[string]string{"jql": "project = Y"}})
	if err != nil {
		t.Fatal(err)
	}

	if v.Via != integrations.ViaComputer || v.SentAt == nil {
		t.Errorf("view = %+v", v)
	}
	if diff := cmp.Diff([]string{"FLEX1-1"}, offered(e.pullFrom(t, "board"))); diff != "" {
		t.Errorf("offered (-want +got):\n%s", diff)
	}
}

func TestDirectModeIgnoresPushed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.setUpPushed(t)
	if err := e.runner.Receive(ctx, "board", []integrations.Ticket{ticket("FLEX1-1")}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.runner.Save(ctx, "board", integrations.Update{Values: map[string]string{"jql": "project = X"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.runner.SetVia(ctx, "board", integrations.ViaDirect); err != nil {
		t.Fatal(err)
	}
	e.board.tickets = []integrations.Ticket{ticket("FLEX1-9")}

	res := e.pullFrom(t, "board")

	if diff := cmp.Diff([]string{"FLEX1-9"}, offered(res)); diff != "" {
		t.Errorf("offered (-want +got):\n%s", diff)
	}
	if res.Sources[0].SentAt != nil || e.board.pulls != 1 {
		t.Errorf("source = %+v, pulls = %d", res.Sources[0], e.board.pulls)
	}
}

func TestSetViaRefused(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.runner.SetVia(ctx, "board", "elsewhere"); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("unknown mode: %v", err)
	}
	if _, err := e.runner.SetVia(ctx, "fake", integrations.ViaComputer); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("events integration: %v", err)
	}
}

func TestHintCarried(t *testing.T) {
	e := newEnv(t)
	e.setUpBoard(t)
	e.board.err = errors.New("Can't reach Jira — on the VPN?")
	e.board.hint = "computer"

	if src := e.pullFrom(t, "board").Sources[0]; src.Hint != "computer" {
		t.Errorf("source = %+v", src)
	}
}
