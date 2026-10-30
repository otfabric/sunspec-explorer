// SPDX-License-Identifier: MIT

package engine

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"

	"github.com/otfabric/sunspec-explorer/internal/model"
)

// falseEnd answers the chain walk's header read at addr with an end marker,
// once. No read after it can tell: Modbus/TCP has no checksum, and the walk
// ended the way a good one does.
func falseEnd(addr uint16) readFilter {
	var fired atomic.Bool
	return func(a, quantity uint16, regs []uint16, err error) ([]uint16, error) {
		if a == addr && quantity == 2 && fired.CompareAndSwap(false, true) {
			return []uint16{0xFFFF, 0}, nil
		}
		return regs, err
	}
}

// stdAddress returns the chain address of a model of stdModels.
func stdAddress(t *testing.T, index int) uint16 {
	t.Helper()
	d := newSim(t)
	e := newEngine(t, time.Second)
	return mustOpen(t, e, d).Models[index].StartAddress
}

func chainIDs(models []model.ModelInfo) []uint16 {
	out := make([]uint16, len(models))
	for i, m := range models {
		out[i] = m.ID
	}
	return out
}

var stdIDs = []uint16{1, 103, 160, 160, 705, vendorModelID}

// A walk cut short by one bad answer looks finished: every model it found
// reads well. Polling alone finds the rest of the chain, with no Rediscover.
func TestChainCutShortHealsWhilePolling(t *testing.T) {
	addr := stdAddress(t, idxMPPT1)
	d := newSim(t)
	d.setFilter(falseEnd(addr))
	e := newEngine(t, 2*time.Millisecond)
	ch := events(t, e)
	info := mustOpen(t, e, d)
	if got := chainIDs(info.Models); !slices.Equal(got, stdIDs[:2]) {
		t.Fatalf("chain after the bad answer = %v, want it cut short", got)
	}
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, ch, "discovery_changed", kindIs(model.EventDiscoveryChanged))
	if got := chainIDs(ev.Session.Models); !slices.Equal(got, stdIDs) {
		t.Errorf("chain after healing = %v, want %v", got, stdIDs)
	}
}

// Without polling, the next read finds out, once a check is due.
func TestChainCutShortHealsOnSnapshot(t *testing.T) {
	addr := stdAddress(t, idxMPPT1)
	d := newSim(t)
	d.setFilter(falseEnd(addr))
	e := newEngine(t, time.Millisecond)
	info := mustOpen(t, e, d)
	time.Sleep(chainCheckEvery / time.Second * e.second)
	snap, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(snap.Warnings, func(w string) bool { return strings.Contains(w, "discovered again") }) {
		t.Errorf("no word of the re-discovery: %v", snap.Warnings)
	}
	st, err := e.State(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := chainIDs(st.Session.Models); !slices.Equal(got, stdIDs) {
		t.Errorf("chain = %v, want %v", got, stdIDs)
	}
}

// A walk that never reached an end marker says so, and the models it made up
// do not survive being read: polling walks the chain again.
func TestChainWithoutEndHeals(t *testing.T) {
	addr := stdAddress(t, idxMPPT1)
	d := newSim(t)
	// Every header after addr reads as a short model, for one whole walk.
	var walks atomic.Int32
	d.setFilter(func(a, quantity uint16, regs []uint16, err error) ([]uint16, error) {
		if quantity == 2 && a >= addr && walks.Load() == 0 {
			return []uint16{64901, 1}, nil
		}
		return regs, err
	})
	e := newEngine(t, 2*time.Millisecond)
	ch := events(t, e)
	info := mustOpen(t, e, d)
	if !slices.ContainsFunc(info.Warnings, func(w string) bool { return strings.Contains(w, "not terminated") }) {
		t.Fatalf("warnings = %v", info.Warnings)
	}
	walks.Store(1)
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, ch, "discovery_changed", kindIs(model.EventDiscoveryChanged))
	if got := chainIDs(ev.Session.Models); !slices.Equal(got, stdIDs) {
		t.Errorf("chain = %v", got)
	}
	if slices.ContainsFunc(ev.Session.Warnings, func(w string) bool { return strings.Contains(w, "not terminated") }) {
		t.Errorf("warnings = %v", ev.Session.Warnings)
	}
}

// A chain that is cut on purpose with MaxModels is not walked again, and a
// healthy chain raises no event.
func TestChainCheckIsQuiet(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, 2*time.Millisecond)
	ch := events(t, e)
	cfg := d.cfg()
	cfg.MaxModels = 2
	capped, err := e.OpenSession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	info := mustOpen(t, e, d)
	for _, id := range []string{capped.ID, info.ID} {
		if err := e.StartPolling(ctx, id, model.PollConfig{IntervalS: 1}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.After(3 * chainCheckEvery / time.Second * e.second)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == model.EventDiscoveryChanged || ev.Kind == model.EventError {
				t.Fatalf("event on a healthy chain: %+v", ev)
			}
			continue
		case <-deadline:
		}
		break
	}
	st, err := e.State(ctx, capped.ID)
	if err != nil || len(st.Session.Models) != 2 {
		t.Errorf("capped chain = %+v, %v", st.Session.Models, err)
	}
}

// The identity is read with discovery. One that failed or came back wrong is
// put right the same way.
func TestIdentityHeals(t *testing.T) {
	addr := stdAddress(t, idxCommon)
	d := newSim(t)
	var fired atomic.Bool
	d.setFilter(func(a, quantity uint16, regs []uint16, err error) ([]uint16, error) {
		if a == addr && quantity > 2 && fired.CompareAndSwap(false, true) {
			return nil, modbus.ErrServerDeviceBusy
		}
		return regs, err
	})
	e := newEngine(t, 2*time.Millisecond)
	ch := events(t, e)
	info := mustOpen(t, e, d)
	if info.Device != nil {
		t.Fatalf("identity = %+v, want none after the failed read", info.Device)
	}
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, ch, "discovery_changed", kindIs(model.EventDiscoveryChanged))
	if ev.Session.Device == nil || ev.Session.Device.Serial != "SN-0001" {
		t.Errorf("identity = %+v", ev.Session.Device)
	}
}
