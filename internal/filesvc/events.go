package filesvc

import (
	"sync"

	"github.com/bprendie/weazlcloud/internal/library"
)

type Event struct {
	Version uint64
	Change  library.Change
}

// Hub is a small per-user fan-out channel. A subscriber always receives a
// resync event first; this makes reconnects safe even when an earlier event
// was missed while the browser was offline.
type Hub struct {
	mu      sync.Mutex
	version uint64
	next    uint64
	subs    map[uint64]chan Event
}

func NewHub() *Hub { return &Hub{subs: make(map[uint64]chan Event)} }

func (h *Hub) CurrentVersion() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.version
}

func (h *Hub) Subscribe() (<-chan Event, func()) {
	h.mu.Lock()
	h.next++
	id := h.next
	ch := make(chan Event, 8)
	ch <- Event{Version: h.version, Change: library.Change{Kind: "resync"}}
	h.subs[id] = ch
	h.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			if current := h.subs[id]; current != nil {
				delete(h.subs, id)
				close(current)
			}
			h.mu.Unlock()
		})
	}
	return ch, cancel
}

func (h *Hub) Publish(change library.Change) {
	h.mu.Lock()
	h.version++
	event := Event{Version: h.version, Change: change}
	for _, ch := range h.subs {
		select {
		case ch <- event:
		default:
			// A slow client can safely resync instead of making a writer wait.
			select {
			case ch <- Event{Version: h.version, Change: library.Change{Kind: "resync"}}:
			default:
			}
		}
	}
	h.mu.Unlock()
}

func (h *Hub) Close() {
	h.mu.Lock()
	for id, ch := range h.subs {
		delete(h.subs, id)
		close(ch)
	}
	h.mu.Unlock()
}
