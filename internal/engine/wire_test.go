// SPDX-License-Identifier: MIT

package engine

import (
	"slices"
	"testing"

	"github.com/otfabric/go-modbus"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/readonly"
)

// A11: across every workflow a user can reach, only FC03 (and FC04 for an
// input-register target) appear on the wire.
func TestWireOnlyReads(t *testing.T) {
	d := newSim(t)
	tap, err := readonly.NewWireTap(d.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer tap.Close()

	e := newEngine(t, tick)
	ch := events(t, e)
	cfg := model.TargetConfig{URL: "tcp://" + tap.Addr(), UnitID: 1, TimeoutMS: 500}

	// Connect, discover, read, poll, re-discover, a failing model, a changed
	// chain, close.
	info, err := e.OpenSession(ctx, cfg)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if _, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{IncludeRaw: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Rediscover(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "sample", isSample)
	inv := info.Models[idxInverter]
	d.setFilter(failRange(inv.StartAddress, inv.StartAddress+1, modbus.ErrServerDeviceBusy))
	waitEvent(t, ch, "failed sample", func(ev model.Event) bool {
		return isSample(ev) && findModel(t, ev.Sample.Models, idxInverter).Status == model.ModelError
	})
	d.setFilter(nil)
	d.setModels(stdModels()[:3]...)
	waitEvent(t, ch, "discovery_changed", func(ev model.Event) bool { return ev.Kind == model.EventDiscoveryChanged })
	waitEvent(t, ch, "sample", isSample)
	if _, err := e.State(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.StopPolling(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.CloseSession(ctx, info.ID); err != nil {
		t.Fatal(err)
	}

	// An input-register target: the device refuses, and FC04 is all it saw.
	cfg.RegType = model.RegTypeInput
	if _, err := e.OpenSession(ctx, cfg); err == nil {
		t.Error("input registers on a holding-register device: no error")
	}
	// A wrong unit ID.
	cfg.RegType, cfg.UnitID = "", 7
	if _, err := e.OpenSession(ctx, cfg); err == nil {
		t.Error("wrong unit: no error")
	}

	if got := tap.FunctionCodes(); !slices.Equal(got, []uint8{3, 4}) {
		t.Errorf("function codes on the wire = %v, want [3 4]", got)
	}
	if tap.Requests() < 20 {
		t.Errorf("only %d requests seen: the tap is not in the path", tap.Requests())
	}
	if e.Guard().Violations() != 0 || e.Guard().Requests() != uint64(tap.Requests()) {
		t.Errorf("guard: %d violations, %d requests; wire: %d requests",
			e.Guard().Violations(), e.Guard().Requests(), tap.Requests())
	}
}
