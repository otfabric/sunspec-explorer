// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"
	"runtime"
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
	for range n {
		h.publish(model.Event{Kind: model.EventError})
	}
}

func TestHubReplay(t *testing.T) {
	h := newHub(4)
	publishN(h, 6) // the ring holds 3..6
	ctx := t.Context()

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

// fullSamples counts the sample events in the ring that still carry their
// sample.
func fullSamples(h *hub) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, ev := range h.ring {
		if ev.Sample != nil {
			n++
		}
	}
	return n
}

func heapInUse() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}

// Of the sample events in the ring, only the newest of each open session
// keeps its sample: what the ring retains does not grow with the number of
// samples or their size. A replay sends those samples and every other event.
func TestHubKeepsNewestSamplePerSession(t *testing.T) {
	const registers = 4 << 10 // 8 KiB a sample
	h := newHub(DefaultEventBuffer)
	before := heapInUse()
	for i := range 2 * DefaultEventBuffer {
		sid := []string{"a", "b"}[i%2]
		h.publish(model.Event{Kind: model.EventSample, SessionID: sid, Sample: &model.Sample{
			SessionID: sid, Seq: uint64(i/2 + 1),
			Models: []model.ModelResult{{RawRegisters: make([]uint16, registers)}},
		}, Recording: &model.RecordingInfo{Samples: uint64(i)}})
	}
	h.publish(model.Event{Kind: model.EventError, SessionID: "a"})
	retained := heapInUse() - before
	if n := fullSamples(h); n != 2 {
		t.Errorf("%d sample events in the ring carry their sample, want 2", n)
	}
	// Two samples of 8 KiB and the ring itself; every sample kept would be
	// 8 MiB.
	if budget := int64(2 << 20); retained > budget {
		t.Errorf("the ring retains %d bytes, more than %d", retained, budget)
	}

	replay := func(since uint64) (kinds []string) {
		for _, ev := range drain(h.subscribe(t.Context(), since)) {
			kind := ev.Kind
			if ev.Kind == model.EventSample {
				kind = fmt.Sprintf("%s:%s:%d", ev.Kind, ev.SessionID, ev.Seq)
				if ev.Sample == nil || ev.Recording == nil || len(ev.Sample.Models[0].RawRegisters) != registers {
					t.Errorf("replayed sample %d is incomplete", ev.Seq)
				}
			}
			kinds = append(kinds, kind)
		}
		return kinds
	}
	last := uint64(2 * DefaultEventBuffer)
	want := fmt.Sprint([]string{model.EventResync, fmt.Sprintf("sample:a:%d", last-1), fmt.Sprintf("sample:b:%d", last), model.EventError})
	if got := fmt.Sprint(replay(1)); got != want {
		t.Errorf("replay = %.300s, want %s", got, want)
	}

	// A closed session keeps no sample in the ring.
	h.publish(model.Event{Kind: model.EventSessionStatus, SessionID: "a",
		Session: &model.SessionInfo{ID: "a", Status: model.SessionClosed}})
	if n := fullSamples(h); n != 1 {
		t.Errorf("%d sample events carry their sample after a session closed, want 1", n)
	}
	want = fmt.Sprint([]string{fmt.Sprintf("sample:b:%d", last), model.EventError, model.EventSessionStatus})
	if got := fmt.Sprint(replay(last - 2)); got != want {
		t.Errorf("replay after the close = %s, want %s", got, want)
	}
	runtime.KeepAlive(h)
}

// The same through the engine: while a session polls, the ring holds its
// newest sample only, and none once the session is closed.
func TestEventRingWhilePolling(t *testing.T) {
	d := newSim(t)
	e := New(Options{EventBuffer: 16})
	e.second = time.Millisecond
	t.Cleanup(e.Close)
	ch := events(t, e)
	info := mustOpen(t, e, d)
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "more samples than the ring holds", func(ev model.Event) bool {
		return isSample(ev) && ev.Sample.Seq > 40
	})
	if n := fullSamples(e.hub); n != 1 {
		t.Errorf("%d sample events in the ring carry their sample while polling, want 1", n)
	}
	if err := e.CloseSession(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if n := fullSamples(e.hub); n != 0 {
		t.Errorf("%d sample events carry their sample after the close, want 0", n)
	}
}
