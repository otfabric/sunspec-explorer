// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// soakDuration returns the length of a soak run, from
// SUNSPEC_EXPLORER_SOAK; without it the test is skipped.
func soakDuration(t *testing.T) time.Duration {
	t.Helper()
	s := os.Getenv("SUNSPEC_EXPLORER_SOAK")
	if s == "" {
		t.Skip("set SUNSPEC_EXPLORER_SOAK to a duration to run the soak test (make soak)")
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatalf("SUNSPEC_EXPLORER_SOAK: %v", err)
	}
	return d
}

// memSample is what the soak measures at one moment.
type memSample struct {
	at         time.Duration
	heap       uint64
	goroutines int
	files      int
}

func measureMem(start time.Time) memSample {
	runtime.GC()
	runtime.GC() // the second one frees what the first one's finalizers released
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return memSample{at: time.Since(start), heap: m.HeapAlloc, goroutines: runtime.NumGoroutine(), files: openFiles()}
}

// TestSoak runs a session the way A14 describes, compressed: polling and
// recording as fast as the engine's unit of time allows, for as long as
// SUNSPEC_EXPLORER_SOAK says, through a device with a low rate of faults and an
// outage now and then. Recordings are stopped and deleted as they fill up, as a
// user would do, so that the recordings directory stays bounded.
//
// It fails when, after a warm-up that fills the event ring and every other
// fixed buffer:
//
//   - the live heap in the last quarter of the run is more than soakHeapSlack
//     above the median of the first quarter;
//   - goroutines or descriptors rise above the highest count of the first
//     quarter by more than soakCountSlack;
//   - the recordings or the event numbering break an invariant of
//     TestChaosPolling;
//   - anything is left behind at the end.
func TestSoak(t *testing.T) {
	const (
		unit           = 2 * time.Millisecond
		timeout        = 100 * time.Millisecond
		bound          = 30 * time.Second
		maxRecording   = 8 << 20
		soakHeapSlack  = 2 << 20
		soakCountSlack = 8
	)
	duration := soakDuration(t)
	seed := uint64(1)
	if seeds := chaosSeeds(t); len(seeds) > 0 {
		seed = seeds[0]
	}
	t.Logf("soak for %v, seed %d", duration, seed)
	rng := rand.New(rand.NewPCG(seed, 3))
	sch := newSchemas(t, recordingSchemaFile)
	base := takeBaseline()

	d := newSim(t)
	proxy := newChaosProxy(t, d.addr, seed, 2*timeout)
	e := New(Options{DataDir: t.TempDir(), Recording: RecordingLimits{
		RotateBytes: 1 << 20, MaxBytes: maxRecording, MaxTotalBytes: 4 * maxRecording, MinFreeBytes: -1,
	}})
	e.second = unit

	info, err := e.OpenSession(ctx, model.TargetConfig{URL: proxy.url(), UnitID: 1, TimeoutMS: int(timeout / time.Millisecond)})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	subCtx, unsubscribe := context.WithCancel(context.Background())
	ch, err := e.Subscribe(subCtx, 0)
	if err != nil {
		t.Fatal(err)
	}
	w := newWatcher(t, info.ID)
	var watching sync.WaitGroup
	watching.Add(1)
	go func() {
		defer watching.Done()
		w.watch(ch)
	}()
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}

	startRecording := func(n int) model.RecordingInfo {
		rec, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: fmt.Sprintf("soak-%d", n)})
		if err != nil {
			t.Fatalf("StartRecording: %v", err)
		}
		return rec
	}
	recordings := 1
	rec := startRecording(recordings)

	// The last recording's checks read everything back; the ones before it
	// are checked too, and that is part of the load.
	rollRecording := func() {
		got, err := e.StopRecording(ctx, rec.ID)
		if err != nil {
			t.Fatalf("StopRecording: %v", err)
		}
		checkRecording(t, e, sch, got)
		if err := e.DeleteRecording(ctx, rec.ID); err != nil {
			t.Fatalf("DeleteRecording: %v", err)
		}
		recordings++
		rec = startRecording(recordings)
	}

	start := time.Now()
	warmup := min(duration/4, 30*time.Second)
	measureEvery := max(duration/40, time.Second)
	nextMeasure := start.Add(warmup)
	var series []memSample
	for time.Since(start) < duration {
		// Mostly clean, with bursts of faults and now and then an outage.
		switch r := rng.IntN(100); {
		case r < 70:
			proxy.setMode(modeClean)
		case r < 95:
			proxy.setMode(modeFaulty)
		default:
			proxy.setMode(modeSilent)
		}
		time.Sleep(time.Duration(rng.IntN(200)+50) * time.Millisecond)
		proxy.setMode(modeClean)

		// A snapshot now and then, so that polling is not the only client.
		if _, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{}); err != nil {
			switch service.CodeOf(err) {
			case service.CodeConnectFailed, service.CodeDiscoveryFailed, service.CodeNotSunSpec:
			default:
				t.Fatalf("Snapshot: %v", err)
			}
		}
		st, err := e.State(ctx, info.ID)
		if err != nil {
			t.Fatalf("State: %v", err)
		}
		// The recording may have reached its limit by itself.
		if st.Recording == nil || st.Recording.SizeBytes > maxRecording/2 {
			rollRecording()
		}
		if now := time.Now(); now.After(nextMeasure) {
			nextMeasure = now.Add(measureEvery)
			series = append(series, measureMem(start))
		}
	}

	proxy.setMode(modeClean)
	waitUntil(t, "connected", bound, func() bool {
		st, err := e.State(ctx, info.ID)
		return err == nil && st.Session.Status == model.SessionConnected
	})
	bounded(t, "CloseSession", bound, func() {
		if err := e.CloseSession(ctx, info.ID); err != nil {
			t.Errorf("CloseSession: %v", err)
		}
	})
	got, err := e.StopRecording(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkRecording(t, e, sch, got)
	unsubscribe()
	bounded(t, "the event stream", bound, watching.Wait)
	if v := e.Guard().Violations(); v != 0 {
		t.Errorf("%d requests refused by the allow-list", v)
	}
	t.Logf("%d samples, %d recordings, %d requests, %d stable-state measurements",
		w.sampleCount(), recordings, proxy.requestCount(), len(series))
	if w.sampleCount() == 0 {
		t.Error("no samples")
	}
	checkStable(t, series, soakHeapSlack, soakCountSlack)

	e.Close()
	proxy.close()
	d.stop()
	base.check(t)
}

// checkStable compares the last quarter of a series with the first.
func checkStable(t testing.TB, series []memSample, heapSlack uint64, countSlack int) {
	t.Helper()
	if len(series) < 8 {
		t.Logf("only %d measurements: too few to compare", len(series))
		return
	}
	quarter := len(series) / 4
	early, late := series[:quarter], series[len(series)-quarter:]
	heaps := make([]uint64, len(early))
	maxGoroutines, maxFiles := 0, 0
	for i, m := range early {
		heaps[i] = m.heap
		maxGoroutines, maxFiles = max(maxGoroutines, m.goroutines), max(maxFiles, m.files)
	}
	slices.Sort(heaps)
	limit := heaps[len(heaps)/2] + heapSlack
	for _, m := range late {
		if m.heap > limit {
			t.Errorf("heap %d MiB at %v, limit %d MiB", m.heap>>20, m.at, limit>>20)
		}
		if m.goroutines > maxGoroutines+countSlack {
			t.Errorf("%d goroutines at %v, at most %d early on", m.goroutines, m.at, maxGoroutines)
		}
		if m.files > maxFiles+countSlack {
			t.Errorf("%d descriptors at %v, at most %d early on", m.files, m.at, maxFiles)
		}
	}
	t.Logf("heap %d KiB early, %d KiB late; goroutines %d, %d; descriptors %d, %d",
		heaps[len(heaps)/2]>>10, late[len(late)-1].heap>>10, maxGoroutines, late[len(late)-1].goroutines, maxFiles, late[len(late)-1].files)
}
