// Package stream fans out messages to every connected SSE client.
package stream

import "sync"

type Message struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type Hub struct {
	mu   sync.Mutex
	subs map[chan Message]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[chan Message]struct{}{}}
}

const buffer = 64

// Subscribe returns a channel of messages and a function that ends the subscription and closes
// the channel. The function is safe to call more than once.
func (h *Hub) Subscribe() (<-chan Message, func()) {
	ch := make(chan Message, buffer)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
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
	for ch := range h.subs {
		select {
		case ch <- m:
		default:
		}
	}
}
