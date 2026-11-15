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
//
// A sample is as large as the device makes it. Of the sample events in the
// ring, only the newest of each open session keeps its sample (the one the
// session holds as its last sample anyway); the others keep their place and
// number but nothing else, and a replay skips them. What the ring retains
// therefore does not grow with the number of samples.
type hub struct {
	mu   sync.Mutex
	seq  uint64
	ring []model.Event // oldest first, at most size entries; numbered without gaps
	size int
	// latest is the number of each session's newest sample event in the ring.
	latest map[string]uint64
	subs   map[*subscriber]struct{}
}

type subscriber struct {
	ch chan model.Event
	// lagged is set when an event could not be delivered; the next event that
	// fits is preceded by a resync.
	lagged bool
}

func newHub(size int) *hub {
	return &hub{size: size, latest: make(map[string]uint64), subs: make(map[*subscriber]struct{})}
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
		if old := h.ring[0]; old.Kind == model.EventSample && h.latest[old.SessionID] == old.Seq {
			delete(h.latest, old.SessionID)
		}
		copy(h.ring, h.ring[1:])
		h.ring = h.ring[:h.size-1]
	}
	switch {
	case ev.Kind == model.EventSample:
		h.strip(ev.SessionID)
		h.latest[ev.SessionID] = ev.Seq
	case ev.Kind == model.EventSessionStatus && ev.Session != nil && ev.Session.Status == model.SessionClosed:
		h.strip(ev.SessionID)
	}
	h.ring = append(h.ring, ev)
	for s := range h.subs {
		s.deliver(ev)
	}
}

// strip drops the sample, and the recording counters that came with it, from
// the session's newest sample event in the ring. The caller holds h.mu.
func (h *hub) strip(sessionID string) {
	seq, ok := h.latest[sessionID]
	if !ok {
		return
	}
	delete(h.latest, sessionID)
	if len(h.ring) == 0 || seq < h.ring[0].Seq {
		return
	}
	if i := seq - h.ring[0].Seq; i < uint64(len(h.ring)) {
		h.ring[i].Sample, h.ring[i].Recording = nil, nil
	}
}

// replayable reports whether a replay sends ev: every event but a sample
// event whose sample was stripped.
func replayable(ev model.Event) bool {
	return ev.Kind != model.EventSample || ev.Sample != nil
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
// are still in the ring, of the sample events only the newest of each open
// session. When some events are gone, a resync comes first.
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
			if ev.Seq > sinceSeq && replayable(ev) {
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
