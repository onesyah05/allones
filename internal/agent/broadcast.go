package agent

import "sync"

// The engine broadcasts every turn event to per-session subscribers, so a surface
// can watch a session (including from another tab) and so side-channel events like
// approval requests reach the live stream.

type broadcaster struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{}
}

func newBroadcaster() *broadcaster { return &broadcaster{subs: map[string]map[chan Event]struct{}{}} }

func (b *broadcaster) subscribe(sessionID string) chan Event {
	ch := make(chan Event, 256)
	b.mu.Lock()
	if b.subs[sessionID] == nil {
		b.subs[sessionID] = map[chan Event]struct{}{}
	}
	b.subs[sessionID][ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *broadcaster) unsubscribe(sessionID string, ch chan Event) {
	b.mu.Lock()
	if m := b.subs[sessionID]; m != nil {
		delete(m, ch)
		if len(m) == 0 {
			delete(b.subs, sessionID)
		}
	}
	b.mu.Unlock()
	close(ch)
}

func (b *broadcaster) publish(sessionID string, ev Event) {
	b.mu.RLock()
	for ch := range b.subs[sessionID] {
		select {
		case ch <- ev:
		default:
		}
	}
	b.mu.RUnlock()
}

// Subscribe lets a caller watch a session's events.
func (e *Engine) Subscribe(sessionID string) chan Event {
	if e.bcast == nil {
		e.bcast = newBroadcaster()
	}
	return e.bcast.subscribe(sessionID)
}

func (e *Engine) Unsubscribe(sessionID string, ch chan Event) {
	if e.bcast != nil {
		e.bcast.unsubscribe(sessionID, ch)
	}
}

// Emit publishes a side-channel event to a session's subscribers.
func (e *Engine) Emit(sessionID string, ev Event) {
	if e.bcast != nil {
		e.bcast.publish(sessionID, ev)
	}
}
