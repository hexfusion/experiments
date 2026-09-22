package main

import "sync"

// Decision broadcast. Same shape kvevents already uses: replicas converge from a
// shared stream instead of replicating each other's scoring work.

type EventKind int

const (
	Started EventKind = iota
	Finished
)

type Event struct {
	Kind      EventKind
	Source    string // replica that published it, so subscribers can skip their own
	RequestID string
	Endpoint  string

	// PrefixHash lets peers learn which pod went warm without scoring anything.
	PrefixHash uint64
}

type Bus struct {
	mu   sync.Mutex
	subs []chan Event
}

func NewBus() *Bus { return &Bus{} }

func (b *Bus) Subscribe() <-chan Event {
	ch := make(chan Event, 4096)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default: // never block a routing decision on a slow subscriber
		}
	}
}
