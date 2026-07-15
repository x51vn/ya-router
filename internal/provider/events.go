package provider

import (
	"sync"
	"time"
)

// EventType is a stable, machine-readable provider lifecycle event.
type EventType string

const (
	EventFactoryRegistered   EventType = "factory_registered"
	EventFactoryUnregistered EventType = "factory_unregistered"
	EventConstructing        EventType = "constructing"
	EventValidating          EventType = "validating"
	EventPublished           EventType = "published"
	EventRemoved             EventType = "removed"
	EventReconcileFailed     EventType = "reconcile_failed"
	EventDrainStarted        EventType = "drain_started"
	EventDrainTimedOut       EventType = "drain_timed_out"
	EventDrained             EventType = "drained"
)

// LifecycleEvent excludes arbitrary upstream error strings so lifecycle
// observability cannot accidentally publish provider credentials.
type LifecycleEvent struct {
	Sequence         uint64    `json:"sequence"`
	Type             EventType `json:"type"`
	ProviderID       ID        `json:"provider_id,omitempty"`
	Generation       uint64    `json:"generation,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	PendingSnapshots int       `json:"pending_snapshots,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
}

// EventBus publishes lifecycle events and keeps a bounded reconnect history.
// Slow subscribers are lossy by design; the bounded history is authoritative.
type EventBus struct {
	mu           sync.Mutex
	nextSequence uint64
	historyLimit int
	history      []LifecycleEvent
	nextSubID    uint64
	subscribers  map[uint64]chan LifecycleEvent
}

func NewEventBus(historyLimit int) *EventBus {
	if historyLimit <= 0 {
		historyLimit = 256
	}
	return &EventBus{
		historyLimit: historyLimit,
		subscribers:  make(map[uint64]chan LifecycleEvent),
	}
}

// Publish assigns sequence and timestamp fields and sends the event without
// allowing a slow observer to block provider replacement.
func (bus *EventBus) Publish(event LifecycleEvent) LifecycleEvent {
	if bus == nil {
		return event
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	bus.nextSequence++
	event.Sequence = bus.nextSequence
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	bus.history = append(bus.history, event)
	if overflow := len(bus.history) - bus.historyLimit; overflow > 0 {
		bus.history = append([]LifecycleEvent(nil), bus.history[overflow:]...)
	}
	for _, subscriber := range bus.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
	return event
}

// History returns events after sequence. The returned slice is isolated.
func (bus *EventBus) History(after uint64) []LifecycleEvent {
	if bus == nil {
		return nil
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	events := make([]LifecycleEvent, 0, len(bus.history))
	for _, event := range bus.history {
		if event.Sequence > after {
			events = append(events, event)
		}
	}
	return events
}

// Subscribe returns a best-effort live stream and an idempotent cancel func.
func (bus *EventBus) Subscribe(buffer int) (<-chan LifecycleEvent, func()) {
	if buffer <= 0 {
		buffer = 16
	}
	stream := make(chan LifecycleEvent, buffer)
	bus.mu.Lock()
	bus.nextSubID++
	id := bus.nextSubID
	bus.subscribers[id] = stream
	bus.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			bus.mu.Lock()
			delete(bus.subscribers, id)
			close(stream)
			bus.mu.Unlock()
		})
	}
	return stream, cancel
}
