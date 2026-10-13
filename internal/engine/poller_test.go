// SPDX-License-Identifier: MIT

package engine

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// tick is the length of a "second" in the polling tests.
const tick = 40 * time.Millisecond

func indexesOf(models []model.ModelResult) []int {
	out := make([]int, len(models))
	for i, m := range models {
		out[i] = m.Index
	}
	return out
}

// A5: polling emits numbered samples; static-only models are read once; a
// single read during polling shares the connection without overlapping.
func TestPolling(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, tick)
	ch := events(t, e)
	info := mustOpen(t, e, d)

	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatalf("StartPolling: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if _, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{}); err != nil {
				t.Errorf("Snapshot during polling: %v", err)
				return
			}
		}
	}()

	var prev time.Time
	for seq := uint64(1); seq <= 5; seq++ {
		ev := waitEvent(t, ch, "sample", isSample)
		s := ev.Sample
		if s.Seq != seq || s.SessionID != info.ID || s.Metadata != model.MetadataCompact || s.CapturedAt.IsZero() {
			t.Fatalf("sample = %+v, want seq %d", s, seq)
		}
		want := []int{idxInverter, idxMPPT1, idxMPPT2, idxVoltVar, idxVendor}
		if seq == 1 {
			want = []int{idxCommon, idxInverter, idxMPPT1, idxMPPT2, idxVoltVar, idxVendor}
		}
		if got := indexesOf(s.Models); !slices.Equal(got, want) {
			t.Errorf("sample %d models = %v, want %v", seq, got, want)
		}
		for _, m := range s.Models {
			if m.Status != model.ModelOK || m.Stale || m.LastOKAt == nil {
				t.Errorf("sample %d model %d = %s stale=%v", seq, m.Index, m.Status, m.Stale)
			}
		}
		if a := findPoint(t, findModel(t, s.Models, idxInverter).Group, "A"); a.Label != "" || a.Scaled == nil {
			t.Errorf("sample point = %+v, want compact with a value", a)
		}
		if !prev.IsZero() && s.CapturedAt.Sub(prev) < tick/2 {
			t.Errorf("samples %v apart, interval is %v", s.CapturedAt.Sub(prev), tick)
		}
		prev = s.CapturedAt
	}
	wg.Wait()

	if got := d.maxInFlight.Load(); got != 1 {
		t.Errorf("up to %d requests at the same time, want 1", got)
	}
	st, err := e.State(ctx, info.ID)
	if err != nil || !st.Polling.Active || st.Polling.IntervalS != 1 || st.Polling.Cycles < 5 || st.Polling.Failures != 0 ||
		st.LastSample == nil || st.Polling.LastSampleAt == nil || st.Polling.StartedAt == nil {
		t.Errorf("State = %+v, %v", st, err)
	}

	if err := e.StopPolling(ctx, info.ID); err != nil {
		t.Fatalf("StopPolling: %v", err)
	}
	if err := e.StopPolling(ctx, info.ID); err != nil {
		t.Errorf("second StopPolling: %v", err)
	}
	st, _ = e.State(ctx, info.ID)
	if st.Polling.Active {
		t.Error("still active after stop")
	}
	// Nothing reads once polling has stopped.
	before := d.requests.Load()
	time.Sleep(3 * tick)
	if after := d.requests.Load(); after != before {
		t.Errorf("%d requests after stop", after-before)
	}
}

func TestPollingExplicitScopeAndRestart(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, tick)
	ch := events(t, e)
	info := mustOpen(t, e, d)

	for _, cfg := range []model.PollConfig{{IntervalS: -1}, {IntervalS: 3601}, {IntervalS: 1, ModelIndexes: []int{6}}, {ModelIndexes: []int{-1}}} {
		if err := e.StartPolling(ctx, info.ID, cfg); service.CodeOf(err) != service.CodeInvalidArgument {
			t.Errorf("StartPolling(%+v) = %v, want invalid_argument", cfg, err)
		}
	}

	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1, ModelIndexes: []int{idxCommon, idxMPPT2, idxCommon}}); err != nil {
		t.Fatalf("StartPolling: %v", err)
	}
	for i := 0; i < 2; i++ {
		ev := waitEvent(t, ch, "sample", isSample)
		if got := indexesOf(ev.Sample.Models); !slices.Equal(got, []int{idxCommon, idxMPPT2}) {
			t.Errorf("models = %v", got)
		}
	}

	// Starting again replaces the poller: the sample numbers start over, and
	// the default interval applies.
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{ModelIndexes: []int{idxInverter}}); err != nil {
		t.Fatalf("restart: %v", err)
	}
	ev := waitEvent(t, ch, "first sample of the new run", func(ev model.Event) bool {
		return isSample(ev) && len(ev.Sample.Models) == 1 && ev.Sample.Models[0].Index == idxInverter
	})
	if ev.Sample.Seq != 1 {
		t.Errorf("seq = %d, want 1", ev.Sample.Seq)
	}
	if st, _ := e.State(ctx, info.ID); st.Polling.IntervalS != DefaultIntervalS || !slices.Equal(st.Polling.ModelIndexes, []int{idxInverter}) {
		t.Errorf("polling = %+v", st.Polling)
	}

	// Closing the session stops its poller.
	if err := e.CloseSession(ctx, info.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	waitEvent(t, ch, "closed", func(ev model.Event) bool {
		return ev.Kind == model.EventSessionStatus && ev.Session.Status == model.SessionClosed
	})
}

// A5: a cycle longer than the interval skips ticks and counts them.
func TestPollingOverruns(t *testing.T) {
	d := newSim(t, stdModels()[:2]...)
	e := newEngine(t, tick)
	ch := events(t, e)
	info := mustOpen(t, e, d)

	// Each request takes most of a tick; a cycle of two models overruns.
	d.setFilter(func(_, _ uint16, regs []uint16, err error) ([]uint16, error) {
		time.Sleep(tick * 3 / 4)
		return regs, err
	})
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1, ModelIndexes: []int{0, 1}}); err != nil {
		t.Fatalf("StartPolling: %v", err)
	}
	first := waitEvent(t, ch, "sample", isSample).Sample
	third := waitEvent(t, ch, "a later sample", func(ev model.Event) bool { return isSample(ev) && ev.Sample.Seq == 3 }).Sample
	if third.Overruns == 0 {
		t.Errorf("no overruns counted: %+v", third)
	}
	// No catching up: the cycles are at least an interval apart.
	if gap := third.CapturedAt.Sub(first.CapturedAt); gap < 2*tick {
		t.Errorf("3 cycles in %v", gap)
	}
	if d.maxInFlight.Load() != 1 {
		t.Errorf("overlapping requests")
	}
	if st, _ := e.State(ctx, info.ID); st.Polling.Overruns < third.Overruns {
		t.Errorf("state overruns = %d", st.Polling.Overruns)
	}
}

// A6: one failing model does not hide the others, and goes stale.
func TestPollingPerModelFailure(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, tick)
	ch := events(t, e)
	info := mustOpen(t, e, d)
	inv := info.Models[idxInverter]

	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatalf("StartPolling: %v", err)
	}
	waitEvent(t, ch, "a good sample", isSample)

	d.setFilter(failRange(inv.StartAddress, inv.StartAddress+inv.Length+1, modbus.ErrServerDeviceFailure))
	failed := func(ev model.Event) bool {
		return isSample(ev) && findModel(t, ev.Sample.Models, idxInverter).Status == model.ModelError
	}
	ev := waitEvent(t, ch, "a sample with the failure", failed)
	m := findModel(t, ev.Sample.Models, idxInverter)
	if m.Error == nil || !strings.Contains(*m.Error, "Device Failure") || m.Group != nil || m.LastOKAt == nil {
		t.Errorf("failed model = %+v", m)
	}
	for _, other := range ev.Sample.Models {
		if other.Index != idxInverter && (other.Status != model.ModelOK || other.Stale) {
			t.Errorf("model %d = %s stale=%v, want ok", other.Index, other.Status, other.Stale)
		}
	}

	// After two missed intervals the last good values are stale.
	ev = waitEvent(t, ch, "a stale model", func(ev model.Event) bool {
		return failed(ev) && findModel(t, ev.Sample.Models, idxInverter).Stale
	})
	m = findModel(t, ev.Sample.Models, idxInverter)
	if age := ev.Sample.CapturedAt.Sub(*m.LastOKAt); age < 2*tick-tick/2 {
		t.Errorf("stale after %v, interval %v", age, tick)
	}

	st, _ := e.State(ctx, info.ID)
	if st.Polling.Failures == 0 || st.Session.Status != model.SessionConnected {
		t.Errorf("state = %+v", st)
	}

	// The model recovers.
	d.setFilter(nil)
	ev = waitEvent(t, ch, "recovery", func(ev model.Event) bool {
		return isSample(ev) && findModel(t, ev.Sample.Models, idxInverter).Status == model.ModelOK
	})
	if m := findModel(t, ev.Sample.Models, idxInverter); m.Stale || m.Group == nil {
		t.Errorf("recovered model = %+v", m)
	}
}

// A7: a changed chain is discovered again and announced; the sample stream
// shows the gap.
func TestPollingModelChanged(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, tick)
	ch := events(t, e)
	info := mustOpen(t, e, d)

	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatalf("StartPolling: %v", err)
	}
	waitEvent(t, ch, "a good sample", isSample)

	// The device now holds Common, one MPPT model and the inverter.
	std := stdModels()
	d.setModels(std[idxCommon], std[idxMPPT1], std[idxInverter])

	gap := waitEvent(t, ch, "the gap", func(ev model.Event) bool {
		return isSample(ev) && slices.ContainsFunc(ev.Sample.Models, func(m model.ModelResult) bool { return m.Status == model.ModelChanged })
	})
	// Reading stopped at the model that changed.
	last := gap.Sample.Models[len(gap.Sample.Models)-1]
	if last.Index != idxInverter || last.Status != model.ModelChanged || last.Group != nil || last.Error == nil {
		t.Errorf("gap sample = %+v", gap.Sample.Models)
	}

	changed := waitEvent(t, ch, "discovery_changed", func(ev model.Event) bool { return ev.Kind == model.EventDiscoveryChanged })
	if changed.Seq <= gap.Seq || len(changed.Session.Models) != 3 || changed.Session.Models[1].ID != 160 {
		t.Errorf("discovery_changed = %+v", changed.Session)
	}

	next := waitEvent(t, ch, "a sample of the new chain", isSample)
	if got := indexesOf(next.Sample.Models); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("models = %v, want the two non-static ones", got)
	}
	for _, m := range next.Sample.Models {
		if m.Status != model.ModelOK {
			t.Errorf("model %d = %s", m.Index, m.Status)
		}
	}
	if next.Sample.Models[0].ID != 160 || next.Sample.Models[1].ID != 103 {
		t.Errorf("new chain read with the old layout: %+v", next.Sample.Models)
	}

	// A single read meets the same situation.
	d.setModels(std...)
	if err := e.StopPolling(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	snap, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !slices.ContainsFunc(snap.Warnings, func(w string) bool { return strings.Contains(w, "discovered again") }) {
		t.Errorf("warnings = %v", snap.Warnings)
	}
	if st, _ := e.State(ctx, info.ID); len(st.Session.Models) != 6 {
		t.Errorf("models after the single read = %d", len(st.Session.Models))
	}
}

// A device that stops answering is retried with backoff, and polling picks
// up again when it returns.
func TestPollingReconnect(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, tick)
	ch := events(t, e)
	info := mustOpen(t, e, d)

	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatalf("StartPolling: %v", err)
	}
	waitEvent(t, ch, "a good sample", isSample)

	d.stop()
	down := waitEvent(t, ch, "reconnecting", func(ev model.Event) bool {
		return ev.Kind == model.EventSessionStatus && ev.Session.Status == model.SessionReconnecting
	})
	if down.Session.StatusDetail == "" {
		t.Errorf("no detail: %+v", down.Session)
	}
	// Failures are recorded as samples, not replaced by old values.
	ev := waitEvent(t, ch, "a failed sample", isSample)
	for _, m := range ev.Sample.Models {
		if m.Status != model.ModelError || m.Group != nil {
			t.Errorf("model %d = %s", m.Index, m.Status)
		}
	}

	d.start()
	waitEvent(t, ch, "connected", func(ev model.Event) bool {
		return ev.Kind == model.EventSessionStatus && ev.Session.Status == model.SessionConnected
	})
	ev = waitEvent(t, ch, "a good sample again", func(ev model.Event) bool {
		return isSample(ev) && ev.Sample.Models[0].Status == model.ModelOK
	})
	if st, _ := e.State(ctx, info.ID); !st.Polling.Active || st.Polling.Failures == 0 || st.LastSample.Seq < ev.Sample.Seq {
		t.Errorf("state = %+v", st.Polling)
	}
}

func TestBackoffGrows(t *testing.T) {
	d := newSim(t, stdModels()[:1]...)
	e := newEngine(t, 4*time.Millisecond)
	ch := events(t, e)
	info := mustOpen(t, e, d)
	d.stop()

	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatalf("StartPolling: %v", err)
	}
	var at []time.Time
	for len(at) < 6 {
		at = append(at, waitEvent(t, ch, "sample", isSample).Sample.CapturedAt)
	}
	// 1, 2, 4, 8, 16 units: the last gap is far longer than the first.
	if first, last := at[1].Sub(at[0]), at[5].Sub(at[4]); last < 4*first || last < 8*e.second {
		t.Errorf("gaps %v then %v: no backoff", first, last)
	}
	if st, _ := e.State(ctx, info.ID); st.Polling.Overruns != 0 {
		t.Errorf("backoff counted as overruns: %d", st.Polling.Overruns)
	}
}
