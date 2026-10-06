package stream_test

import (
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/stream"
)

func TestPublishReachesEverySubscriber(t *testing.T) {
	hub := stream.NewHub()
	a, unsubA := hub.Subscribe()
	defer unsubA()
	b, unsubB := hub.Subscribe()
	defer unsubB()

	hub.Publish(stream.Message{Type: "batch.applied", Data: 1})

	for name, ch := range map[string]<-chan stream.Message{"a": a, "b": b} {
		select {
		case m := <-ch:
			if m.Type != "batch.applied" {
				t.Errorf("%s got %q", name, m.Type)
			}
		case <-time.After(time.Second):
			t.Errorf("%s got nothing", name)
		}
	}
}

func TestUnsubscribeClosesAndIsSafeTwice(t *testing.T) {
	hub := stream.NewHub()
	ch, unsub := hub.Subscribe()
	unsub()
	unsub()

	if _, open := <-ch; open {
		t.Error("channel still open after unsubscribe")
	}
	hub.Publish(stream.Message{Type: "after"}) // must not panic on the closed channel
}

func TestSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	hub := stream.NewHub()
	_, unsub := hub.Subscribe() // never read
	defer unsub()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			hub.Publish(stream.Message{Type: "spam"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a subscriber that isn't reading")
	}
}

func TestPublishBatchSendsTypedPayload(t *testing.T) {
	hub := stream.NewHub()
	ch, unsub := hub.Subscribe()
	defer unsub()

	hub.PublishBatch(7, stream.SourceCleanup, []domain.Activity{{ID: 1, BatchID: 7}})

	m := <-ch
	applied, ok := m.Data.(stream.BatchApplied)
	if m.Type != "batch.applied" || !ok || applied.BatchID != 7 || applied.Source != stream.SourceCleanup || len(applied.Activity) != 1 {
		t.Errorf("message = %+v", m)
	}
}

func TestSubscribeToSomeTypes(t *testing.T) {
	hub := stream.NewHub()
	batches, unsub := hub.Subscribe("batch.applied", "batch.undone")
	defer unsub()

	for range 100 { // more than the buffer holds
		hub.Publish(stream.Message{Type: "chat.delta", Data: "hi"})
	}
	hub.PublishUndone(7)

	select {
	case m := <-batches:
		if m.Type != "batch.undone" {
			t.Errorf("got %q, want batch.undone", m.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("the undo was crowded out")
	}
}
