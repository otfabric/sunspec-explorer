// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"sync"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/model"
)

// subscriberBuffer is the number of events a subscriber may lag behind before
// it loses events and gets a resync instead.
const subscriberBuffer = 256

// hub numbers events, keeps the most recent ones for replay and fans them out
// to subscribers without ever blocking the publisher.
type hub struct {
	mu   sync.Mutex
	seq  uint64
	ring []model.Event // oldest first, at most size entries
	size int
	subs map[*subscriber]struct{}
}

type subscriber struct {
	ch chan model.Event
	// lagged is set when an event could not be delivered; the next event that
	// fits is preceded by a resync.
	lagged bool
}

func newHub(size int) *hub {
	return &hub{size: size, subs: make(map[*subscriber]struct{})}
}

// publish assigns the next sequence number to ev and delivers it.
func (h *hub) publish(ev model.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	ev.Seq = h.seq
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	if len(h.ring) == h.size {
		copy(h.ring, h.ring[1:])
		h.ring = h.ring[:h.size-1]
	}
	h.ring = append(h.ring, ev)
	for s := range h.subs {
		s.deliver(ev)
	}
}

// deliver sends ev without blocking. A subscriber that is full misses the
// event and is told so with a resync as soon as it has room for two events.
func (s *subscriber) deliver(ev model.Event) {
	if s.lagged {
		if cap(s.ch)-len(s.ch) < 2 {
			return
		}
		s.ch <- model.Event{Seq: ev.Seq - 1, Kind: model.EventResync, At: ev.At}
		s.lagged = false
	}
	select {
	case s.ch <- ev:
	default:
		s.lagged = true
	}
}

// subscribe registers a subscriber and replays the events after sinceSeq that
// are still in the ring. When some of them are gone, a resync comes first.
func (h *hub) subscribe(ctx context.Context, sinceSeq uint64) <-chan model.Event {
	h.mu.Lock()
	defer h.mu.Unlock()

	var replay []model.Event
	switch {
	case sinceSeq > h.seq:
		// A sequence number this engine never issued: the client last talked
		// to an earlier run.
		replay = []model.Event{{Seq: h.seq, Kind: model.EventResync, At: time.Now().UTC()}}
	case sinceSeq > 0 && sinceSeq < h.seq:
		for _, ev := range h.ring {
			if ev.Seq > sinceSeq {
				replay = append(replay, ev)
			}
		}
		oldest := h.seq + 1
		if len(h.ring) > 0 {
			oldest = h.ring[0].Seq
		}
		if sinceSeq+1 < oldest {
			replay = append([]model.Event{{Seq: sinceSeq, Kind: model.EventResync, At: time.Now().UTC()}}, replay...)
		}
	}

	s := &subscriber{ch: make(chan model.Event, subscriberBuffer+len(replay))}
	for _, ev := range replay {
		s.ch <- ev
	}
	h.subs[s] = struct{}{}

	go func() {
		<-ctx.Done()
		h.mu.Lock()
		delete(h.subs, s)
		close(s.ch)
		h.mu.Unlock()
	}()
	return s.ch
}
