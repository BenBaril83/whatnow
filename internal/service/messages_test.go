package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestMessagesAreKeptByDate(t *testing.T) {
	svc, clock := newService(t)
	ctx := context.Background()
	sent, err := svc.AddMessage(ctx, domain.Message{Date: "2026-10-05", Role: domain.RoleUser, Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(1)
	reply, err := svc.AddMessage(ctx, domain.Message{Date: "2026-10-05", Role: domain.RoleAssistant, Text: "Morning!",
		Tools: []domain.ToolUse{{Tool: "what_now", Label: `Picked #3 "RFC"`}}, BatchID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddMessage(ctx, domain.Message{Date: "2026-10-06", Role: domain.RoleUser, Text: "tomorrow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddMessage(ctx, domain.Message{Date: "2026-10-05", Role: "system", Text: "x"}); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("bad role: err = %v, want ErrInvalid", err)
	}

	got, err := svc.Messages(ctx, "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]domain.Message{sent, reply}, got); diff != "" {
		t.Errorf("messages (-want +got):\n%s", diff)
	}
	if sent.Tools == nil || len(got[0].Tools) != 0 {
		t.Errorf("a message without tools should hold an empty list: %+v", sent)
	}
}

func TestChatFoldIsReplaced(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	if f, err := svc.ChatFold(ctx, "2026-10-05"); err != nil || f != (domain.ChatFold{}) {
		t.Fatalf("no fold yet: %+v, %v", f, err)
	}
	for _, f := range []domain.ChatFold{{Summary: "first", ThroughID: 4}, {Summary: "second", ThroughID: 9}} {
		if err := svc.SetChatFold(ctx, "2026-10-05", f); err != nil {
			t.Fatal(err)
		}
	}
	if f, _ := svc.ChatFold(ctx, "2026-10-05"); f != (domain.ChatFold{Summary: "second", ThroughID: 9}) {
		t.Errorf("fold = %+v", f)
	}
}

func TestDaysBetween(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	for _, d := range []domain.Date{"2026-09-28", "2026-09-29", "2026-10-04", "2026-10-05"} {
		if _, err := svc.StartDay(ctx, d, 3, ""); err != nil {
			t.Fatal(err)
		}
	}
	days, err := svc.DaysBetween(ctx, "2026-09-29", "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	var dates []domain.Date
	for _, d := range days {
		dates = append(dates, d.Date)
	}
	if diff := cmp.Diff([]domain.Date{"2026-09-29", "2026-10-04"}, dates); diff != "" {
		t.Errorf("dates (-want +got):\n%s", diff)
	}
}
