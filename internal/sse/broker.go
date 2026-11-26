package sse

import (
	"encoding/json"
	"fmt"
	"sync"
)

type Event struct {
	ID   uint64
	Type string
	Data json.RawMessage
}
type Broker struct {
	mu      sync.Mutex
	next    uint64
	clients map[chan Event]struct{}
}

func New() *Broker { return &Broker{clients: make(map[chan Event]struct{})} }
func (b *Broker) Subscribe() (chan Event, func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.clients) >= 64 {
		return nil, nil, fmt.Errorf("sse: connection limit reached")
	}
	ch := make(chan Event, 32)
	b.clients[ch] = struct{}{}
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.clients[ch]; ok {
			delete(b.clients, ch)
			close(ch)
		}
	}, nil
}
func (b *Broker) Publish(kind string, data interface{}) error {
	switch kind {
	case "new_email", "flag_updated", "sync_status", "mcp_approval_required", "background_activity":
	default:
		return fmt.Errorf("sse: unknown event")
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("sse encode: %w", err)
	}
	if len(encoded) > 256*1024 {
		return fmt.Errorf("sse: event exceeds 256KiB")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	event := Event{ID: b.next, Type: kind, Data: encoded}
	for ch := range b.clients {
		select {
		case ch <- event:
		default:
			delete(b.clients, ch)
			close(ch)
		}
	}
	return nil
}
