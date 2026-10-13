// SPDX-License-Identifier: MIT

package api_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/api"
	"github.com/otfabric/sunspec-explorer/internal/engine"
	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

var ctx = context.Background()

// next returns the first event that matches, failing after a timeout.
func next(t testing.TB, ch <-chan model.Event, what string, match func(model.Event) bool) model.Event {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("event stream closed while waiting for %s", what)
			}
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// A10: the same contract holds for the engine and for the engine behind the
// HTTP API.
func TestContract(t *testing.T) {
	t.Run("engine", func(t *testing.T) {
		eng := engine.New(engine.Options{DataDir: t.TempDir()})
		t.Cleanup(eng.Close)
		contract(t, eng)
	})
	t.Run("http", func(t *testing.T) {
		contract(t, newDaemon(t, engine.Options{DataDir: t.TempDir()}, api.Options{}).svc)
	})
}

func contract(t *testing.T, svc service.Service) {
	dev := newSim(t)

	h, err := svc.Health(ctx)
	if err != nil || h.Status != "ok" || h.APIVersion != model.APIVersion || h.RegistryModels < 100 {
		t.Fatalf("Health = %+v, %v", h, err)
	}

	sub, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := svc.Subscribe(sub, 0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Errors keep their code, and a failed open its probe attempts.
	bad := dev.cfg()
	bad.UnitID = 9
	_, err = svc.OpenSession(ctx, bad)
	var se *service.Error
	if !errors.As(err, &se) || se.Code != service.CodeConnectFailed || len(se.Attempts) != 8 || se.Attempts[0].Error == "" {
		t.Fatalf("open with a wrong unit = %v", err)
	}
	bad = dev.cfg()
	bad.BaseAddresses = []uint16{40002}
	if _, err = svc.OpenSession(ctx, bad); service.CodeOf(err) != service.CodeNotSunSpec {
		t.Errorf("open without marker = %v", err)
	}
	if _, err = svc.OpenSession(ctx, model.TargetConfig{URL: "http://x"}); service.CodeOf(err) != service.CodeInvalidArgument {
		t.Errorf("open with a bad URL = %v", err)
	}
	for name, call := range map[string]func() error{
		"State":        func() error { _, e := svc.State(ctx, "nope"); return e },
		"Snapshot":     func() error { _, e := svc.Snapshot(ctx, "nope", model.SnapshotRequest{}); return e },
		"Rediscover":   func() error { _, e := svc.Rediscover(ctx, "nope"); return e },
		"StartPolling": func() error { return svc.StartPolling(ctx, "nope", model.PollConfig{}) },
		"StopPolling":  func() error { return svc.StopPolling(ctx, "nope") },
		"CloseSession": func() error { return svc.CloseSession(ctx, "nope") },
	} {
		if err := call(); service.CodeOf(err) != service.CodeNotFound {
			t.Errorf("%s on an unknown session = %v, want not_found", name, err)
		}
	}

	// Open and discover.
	info, err := svc.OpenSession(ctx, dev.cfg())
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	ids := make([]uint16, len(info.Models))
	for i, m := range info.Models {
		ids[i] = m.ID
	}
	if !slices.Equal(ids, []uint16{1, 103, 160, 160, 705, vendorModelID}) || info.Target.BaseAddress != 40000 ||
		info.Device == nil || info.Device.Serial != "SN-0001" || len(info.Attempts) == 0 || info.Status != model.SessionConnected {
		t.Fatalf("session = %+v", info)
	}
	opened := next(t, events, "session_status", func(ev model.Event) bool { return ev.Kind == model.EventSessionStatus })
	if opened.SessionID != info.ID || opened.Session == nil || opened.Seq == 0 {
		t.Errorf("event = %+v", opened)
	}
	if list, err := svc.Sessions(ctx); err != nil || len(list) != 1 || list[0].ID != info.ID {
		t.Errorf("Sessions = %+v, %v", list, err)
	}

	// Single read.
	snap, err := svc.Snapshot(ctx, info.ID, model.SnapshotRequest{})
	if err != nil || snap.Schema != model.SnapshotSchema || len(snap.Models) != 6 || snap.Metadata != model.MetadataFull {
		t.Fatalf("Snapshot = %+v, %v", snap, err)
	}
	inv := snap.Models[1]
	var a *model.PointView
	for i := range inv.Group.Points {
		if inv.Group.Points[i].Name == "A" {
			a = &inv.Group.Points[i]
		}
	}
	if a == nil || a.Scaled == nil || *a.Scaled < 12.29 || *a.Scaled > 12.31 || a.Label == "" || a.SFRaw == nil || *a.SFRaw != -1 {
		t.Errorf("point A = %+v", a)
	}
	if len(snap.Models[2].Group.Groups) != 2 || len(snap.Models[4].Group.Groups[0].Groups) != 2 {
		t.Errorf("nested groups lost")
	}
	if v := snap.Models[5]; v.Group != nil || !slices.Equal(v.RawRegisters, []uint16{1, 2, 3, 4}) {
		t.Errorf("vendor model = %+v", v)
	}
	if _, err := svc.Snapshot(ctx, info.ID, model.SnapshotRequest{ModelIndexes: []int{9}}); service.CodeOf(err) != service.CodeInvalidArgument {
		t.Errorf("Snapshot of index 9 = %v", err)
	}
	sel, err := svc.Snapshot(ctx, info.ID, model.SnapshotRequest{ModelIndexes: []int{1}, Metadata: model.MetadataCompact})
	if err != nil || len(sel.Models) != 1 || sel.Models[0].Group.Points[2].Label != "" {
		t.Errorf("compact selection = %+v, %v", sel.Models, err)
	}

	// Polling: samples arrive as events, numbered, compact.
	if err := svc.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 3601}); service.CodeOf(err) != service.CodeInvalidArgument {
		t.Errorf("StartPolling with a bad interval = %v", err)
	}
	if err := svc.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatalf("StartPolling: %v", err)
	}
	for seq := uint64(1); seq <= 2; seq++ {
		ev := next(t, events, "sample", func(ev model.Event) bool { return ev.Kind == model.EventSample })
		if ev.Sample == nil || ev.Sample.Seq != seq || ev.Sample.Metadata != model.MetadataCompact || ev.Sample.SessionID != info.ID {
			t.Fatalf("sample = %+v, want seq %d", ev.Sample, seq)
		}
		want := 5 // the static-only Common model is read in the first cycle only
		if seq == 1 {
			want = 6
		}
		if len(ev.Sample.Models) != want {
			t.Errorf("sample %d has %d models, want %d", seq, len(ev.Sample.Models), want)
		}
	}
	st, err := svc.State(ctx, info.ID)
	if err != nil || !st.Polling.Active || st.Polling.IntervalS != 1 || st.Polling.Cycles < 2 || st.LastSample == nil || st.Session.ID != info.ID {
		t.Errorf("State = %+v, %v", st, err)
	}
	if err := svc.StopPolling(ctx, info.ID); err != nil {
		t.Errorf("StopPolling: %v", err)
	}

	// Re-discovery of a changed device.
	dev.setModels(t, stdModels()[:2]...)
	again, err := svc.Rediscover(ctx, info.ID)
	if err != nil || len(again.Models) != 2 {
		t.Fatalf("Rediscover = %+v, %v", again, err)
	}
	changed := next(t, events, "discovery_changed", func(ev model.Event) bool { return ev.Kind == model.EventDiscoveryChanged })
	if changed.Session == nil || len(changed.Session.Models) != 2 {
		t.Errorf("event = %+v", changed)
	}

	// Recording: start, counters on the sample events, stop, list, read,
	// delete.
	if _, err := svc.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "../x"}); service.CodeOf(err) != service.CodeInvalidArgument {
		t.Errorf("StartRecording with a path = %v", err)
	}
	rec, err := svc.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "contract"})
	if err != nil || rec.ID != "contract" || rec.State != model.RecordingRunning || rec.SessionID != info.ID {
		t.Fatalf("StartRecording = %+v, %v", rec, err)
	}
	next(t, events, "recording_status", func(ev model.Event) bool {
		return ev.Kind == model.EventRecordingStatus && ev.Recording != nil && ev.Recording.State == model.RecordingRunning
	})
	if err := svc.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	counted := next(t, events, "a recorded sample", func(ev model.Event) bool {
		return ev.Kind == model.EventSample && ev.Recording != nil && ev.Recording.Samples >= 1
	})
	if counted.Recording.ID != "contract" || counted.Recording.SizeBytes <= rec.SizeBytes {
		t.Errorf("counters = %+v", counted.Recording)
	}
	if st, err := svc.State(ctx, info.ID); err != nil || st.Recording == nil || st.Recording.ID != "contract" {
		t.Errorf("State.Recording = %+v, %v", st.Recording, err)
	}
	if err := svc.DeleteRecording(ctx, "contract"); service.CodeOf(err) != service.CodeInvalidArgument {
		t.Errorf("DeleteRecording while running = %v", err)
	}
	stopped, err := svc.StopRecording(ctx, "contract")
	if err != nil || stopped.State != model.RecordingStopped || stopped.StopReason != model.StopUser || stopped.Samples == 0 {
		t.Fatalf("StopRecording = %+v, %v", stopped, err)
	}
	if err := svc.StopPolling(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if recs, err := svc.Recordings(ctx); err != nil || len(recs) != 1 || recs[0].ID != "contract" || recs[0].Samples != stopped.Samples {
		t.Errorf("Recordings = %+v, %v", recs, err)
	}
	body, download, err := svc.OpenRecording(ctx, "contract", 0)
	if err != nil {
		t.Fatalf("OpenRecording: %v", err)
	}
	whole, _ := io.ReadAll(body)
	_ = body.Close()
	if int64(len(whole)) != stopped.SizeBytes || download.SizeBytes != stopped.SizeBytes ||
		!bytes.HasPrefix(whole, []byte(`{"type":"header","schema":"`+model.RecordingSchema+`"`)) ||
		!bytes.Contains(whole, []byte(`"type":"footer"`)) {
		t.Errorf("recording: %d bytes (size %d), starts %.80s", len(whole), stopped.SizeBytes, whole)
	}
	tail, _, err := svc.OpenRecording(ctx, "contract", 100)
	if err != nil {
		t.Fatalf("OpenRecording at 100: %v", err)
	}
	rest, _ := io.ReadAll(tail)
	_ = tail.Close()
	if !bytes.Equal(rest, whole[100:]) {
		t.Errorf("from offset 100: %d bytes, want %d", len(rest), len(whole)-100)
	}
	if _, _, err := svc.OpenRecording(ctx, "contract", int64(len(whole))+5); service.CodeOf(err) != service.CodeInvalidArgument {
		t.Errorf("OpenRecording beyond the end = %v", err)
	}
	if err := svc.DeleteRecording(ctx, "contract"); err != nil {
		t.Errorf("DeleteRecording: %v", err)
	}
	for name, call := range map[string]func() error{
		"StopRecording":   func() error { _, e := svc.StopRecording(ctx, "contract"); return e },
		"OpenRecording":   func() error { _, _, e := svc.OpenRecording(ctx, "contract", 0); return e },
		"DeleteRecording": func() error { return svc.DeleteRecording(ctx, "contract") },
	} {
		if err := call(); service.CodeOf(err) != service.CodeNotFound {
			t.Errorf("%s of a deleted recording = %v, want not_found", name, err)
		}
	}
	if recs, err := svc.Recordings(ctx); err != nil || len(recs) != 0 {
		t.Errorf("Recordings = %v, %v", recs, err)
	}
	if ports, err := svc.SerialPorts(ctx); err != nil || len(ports) != 0 {
		t.Errorf("SerialPorts = %v, %v", ports, err)
	}

	if err := svc.CloseSession(ctx, info.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	closed := next(t, events, "closed", func(ev model.Event) bool {
		return ev.Kind == model.EventSessionStatus && ev.Session != nil && ev.Session.Status == model.SessionClosed
	})
	if closed.Seq <= changed.Seq {
		t.Errorf("sequence numbers do not increase: %d after %d", closed.Seq, changed.Seq)
	}
	if list, _ := svc.Sessions(ctx); len(list) != 0 {
		t.Errorf("sessions after close = %+v", list)
	}
}
