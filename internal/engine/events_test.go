// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/model"
)

func drain(ch <-chan model.Event) []model.Event {
	var out []model.Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}

func publishN(h *hub, n int) {
	for i := 0; i < n; i++ {
		h.publish(model.Event{Kind: model.EventError})
	}
}

func TestHubReplay(t *testing.T) {
	h := newHub(4)
	publishN(h, 6) // the ring holds 3..6
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// New events only.
	fresh := h.subscribe(ctx, 0)
	if got := drain(fresh); len(got) != 0 {
		t.Errorf("since 0 replayed %d events", len(got))
	}

	// Everything after 4 is still there.
	got := drain(h.subscribe(ctx, 4))
	if len(got) != 2 || got[0].Seq != 5 || got[1].Seq != 6 || got[0].At.IsZero() {
		t.Errorf("since 4 = %+v", got)
	}

	// Event 2 fell out of the ring: resync first, then what is left.
	got = drain(h.subscribe(ctx, 1))
	if len(got) != 5 || got[0].Kind != model.EventResync || got[1].Seq != 3 {
		t.Errorf("since 1 = %+v", got)
	}

	// The oldest event in the ring is the next one: nothing was missed.
	got = drain(h.subscribe(ctx, 2))
	if len(got) != 4 || got[0].Kind == model.EventResync {
		t.Errorf("since 2 = %+v", got)
	}

	// Up to date, and a number from another run of the engine.
	if got := drain(h.subscribe(ctx, 6)); len(got) != 0 {
		t.Errorf("since 6 = %+v", got)
	}
	if got := drain(h.subscribe(ctx, 99)); len(got) != 1 || got[0].Kind != model.EventResync {
		t.Errorf("since 99 = %+v", got)
	}

	h.publish(model.Event{Kind: model.EventError})
	if got := drain(fresh); len(got) != 1 || got[0].Seq != 7 {
		t.Errorf("live = %+v", got)
	}
}

func TestHubSlowSubscriber(t *testing.T) {
	h := newHub(8)
	ctx, cancel := context.WithCancel(context.Background())
	ch := h.subscribe(ctx, 0)

	// Nobody reads: the buffer fills and the rest is dropped, without
	// blocking the publisher.
	publishN(h, subscriberBuffer+10)
	got := drain(ch)
	if len(got) != subscriberBuffer || got[len(got)-1].Seq != subscriberBuffer {
		t.Fatalf("got %d events, last %d", len(got), got[len(got)-1].Seq)
	}

	// The subscriber reads again: it is told that it missed events.
	h.publish(model.Event{Kind: model.EventError})
	got = drain(ch)
	if len(got) != 2 || got[0].Kind != model.EventResync || got[1].Seq != subscriberBuffer+11 {
		t.Errorf("after the gap = %+v", got)
	}

	cancel()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				h.publish(model.Event{Kind: model.EventError}) // no subscriber left: must not panic
				return
			}
		case <-deadline:
			t.Fatal("channel not closed after cancel")
		}
	}
}
