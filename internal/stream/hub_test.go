package stream_test

import (
	"testing"
	"time"

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
