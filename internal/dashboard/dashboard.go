package dashboard

import (
	"sync"
	"time"
)

const maxEvents = 50

type Kind string

const (
	KindEnqueued Kind = "enqueued"
	KindActive   Kind = "active"
	KindDone     Kind = "done"
)

type Event struct {
	ID        string
	Priority  string
	Kind      Kind
	LatencyMs float64 // total time enqueue→done, only set when Kind==KindDone
	Status    int     // HTTP status, only set when Kind==KindDone
	Timestamp time.Time
}

// Dashboard records request lifecycle events and serves them to the UI.
// It keeps the latest state per request ID (enqueued → active → done).
type Dashboard struct {
	mu     sync.Mutex
	events []Event // newest first, capped at maxEvents
}

func New() *Dashboard {
	return &Dashboard{}
}

// Record updates the event for req.ID if it exists, otherwise prepends it.
// This way each row in the UI shows the latest known state of a request.
func (d *Dashboard) Record(e Event) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for i, ev := range d.events {
		if ev.ID == e.ID {
			d.events[i] = e
			return
		}
	}

	d.events = append([]Event{e}, d.events...)
	if len(d.events) > maxEvents {
		d.events = d.events[:maxEvents]
	}
}

// Snapshot returns a copy of current events, newest first.
func (d *Dashboard) Snapshot() []Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Event, len(d.events))
	copy(out, d.events)
	return out
}
