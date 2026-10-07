// Package stream fans out messages to every connected SSE client.
package stream

import (
	"sync"

	"github.com/jameynakama/whatnow/internal/domain"
)

type Message struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type Hub struct {
	mu sync.Mutex
	// subs maps each subscriber to the message types it wants, or nil for every type.
	subs map[chan Message]map[string]bool
}

func NewHub() *Hub {
	return &Hub{subs: map[chan Message]map[string]bool{}}
}

const buffer = 64

// Subscribe returns a channel of messages and a function that ends the subscription and closes
// the channel. The function is safe to call more than once. Naming types limits the channel to
// them, so a busy stream of other messages can't crowd them out of its buffer.
func (h *Hub) Subscribe(types ...string) (<-chan Message, func()) {
	ch := make(chan Message, buffer)
	var wanted map[string]bool
	if len(types) > 0 {
		wanted = map[string]bool{}
		for _, t := range types {
			wanted[t] = true
		}
	}
	h.mu.Lock()
	h.subs[ch] = wanted
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
			close(ch)
		})
	}
}

// Publish sends m to every subscriber. A subscriber whose buffer is full misses the message
// instead of stalling every write; the web client refetches when it reconnects.
func (h *Hub) Publish(m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, wanted := range h.subs {
		if wanted != nil && !wanted[m.Type] {
			continue
		}
		select {
		case ch <- m:
		default:
		}
	}
}

// Source is what made a batch. The web client uses it to pick where the batch's undo appears.
type Source string

const (
	SourceUser    Source = "user"
	SourceChat    Source = "chat"
	SourceCleanup Source = "cleanup"
	SourceTriage  Source = "triage"
)

// BatchApplied is the batch.applied payload, sent after every write.
type BatchApplied struct {
	BatchID  int64             `json:"batch_id"`
	Source   Source            `json:"source"`
	Activity []domain.Activity `json:"activity"`
}

// PublishBatch announces a batch's changes to every client.
func (h *Hub) PublishBatch(batch int64, source Source, activity []domain.Activity) {
	h.Publish(Message{Type: "batch.applied", Data: BatchApplied{BatchID: batch, Source: source, Activity: activity}})
}

// BatchUndone is the batch.undone payload.
type BatchUndone struct {
	BatchID int64 `json:"batch_id"`
}

// PublishUndone announces that a batch was undone.
func (h *Hub) PublishUndone(batch int64) {
	h.Publish(Message{Type: "batch.undone", Data: BatchUndone{BatchID: batch}})
}

// WorkKind names a slow model call that clients show as in progress.
type WorkKind string

const (
	WorkReplan  WorkKind = "replan"
	WorkCleanup WorkKind = "cleanup"
)

// Work is the work.started and work.finished payload.
type Work struct {
	Kind WorkKind `json:"kind"`
}

// StartWork announces work.started and returns the function that announces work.finished.
func (h *Hub) StartWork(kind WorkKind) (finish func()) {
	h.Publish(Message{Type: "work.started", Data: Work{Kind: kind}})
	return func() { h.Publish(Message{Type: "work.finished", Data: Work{Kind: kind}}) }
}
