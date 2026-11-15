// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// Lifecycle contract tests of the engine and its sessions: the rows of the
// matrix in TESTING.md that close or stop while other calls run. They decide
// the interleaving with events (a handler that holds a log call, a device
// that holds a read, the I/O token held by the test) and with the state of
// the goroutines involved, never with sleeps.

// stacks returns the stack of every goroutine, one block each.
func stacks() []string {
	buf := make([]byte, 64<<10)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Split(string(buf[:n]), "\n\n")
		}
		buf = make([]byte, 2*len(buf))
	}
}

// pollers counts the poll goroutines, started or not: startPolling starts
// nothing else.
func pollers() int {
	n := 0
	for _, g := range stacks() {
		if strings.Contains(g, "created by github.com/otfabric/sunspec-explorer/internal/engine.(*session).startPolling ") {
			n++
		}
	}
	return n
}

// blocked reports whether a goroutine waits in state (as the runtime names
// it: "sync.Mutex.Lock", "chan send") with fn on its stack.
func blocked(fn, state string) bool {
	for _, g := range stacks() {
		header, _, _ := strings.Cut(g, "\n")
		if strings.Contains(header, "["+state) && strings.Contains(g, fn) {
			return true
		}
	}
	return false
}

// waitFor waits until cond holds, failing after a bound.
func waitFor(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

// waitNoPollers fails unless every poll goroutine has ended. One that was
// told to stop may still be returning when the call that stopped it does.
func waitNoPollers(t testing.TB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for pollers() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d poll goroutines still run", pollers())
		}
		time.Sleep(time.Millisecond)
	}
}

func receive[T any](t testing.TB, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// holdLog is a log handler that holds the first record with a given message
// until the test releases it, and discards everything.
type holdLog struct {
	msg     string
	fired   atomic.Bool
	held    chan struct{}
	release chan struct{}
}

func newHoldLog(msg string) *holdLog {
	return &holdLog{msg: msg, held: make(chan struct{}), release: make(chan struct{})}
}

func (h *holdLog) Enabled(context.Context, slog.Level) bool { return true }
func (h *holdLog) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *holdLog) WithGroup(string) slog.Handler            { return h }

func (h *holdLog) Handle(_ context.Context, r slog.Record) error {
	if r.Message == h.msg && h.fired.CompareAndSwap(false, true) {
		close(h.held)
		<-h.release
	}
	return nil
}

// Two StartPolling calls at once, then StopPolling: exactly one poller runs
// in between, and none afterwards. The first call is held where it has
// stopped the old poller and not yet started its own (its "polling stopped"
// log line); the second starts meanwhile. That one must wait for the first
// instead of starting a poller the first then replaces without stopping.
func TestStartPollingConcurrent(t *testing.T) {
	d := newSim(t)
	logs := newHoldLog("polling stopped")
	e := New(Options{Logger: slog.New(logs)})
	e.second = time.Hour // one cycle per poller: the next is an hour away
	t.Cleanup(e.Close)
	ch := events(t, e)
	info := mustOpen(t, e, d)

	cfg := model.PollConfig{IntervalS: 1}
	if err := e.StartPolling(ctx, info.ID, cfg); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "the first sample", isSample)

	first := make(chan error, 1)
	go func() { first <- e.StartPolling(ctx, info.ID, cfg) }()
	receive(t, logs.held, "the first restart to stop the running poller")

	second := make(chan error, 1)
	go func() { second <- e.StartPolling(ctx, info.ID, cfg) }()
	waitFor(t, "the second restart to return or to wait for the first", func() bool {
		return len(second) > 0 || blocked("engine.(*session).startPolling(", "sync.Mutex.Lock")
	})
	close(logs.release)

	for _, ch := range []chan error{first, second} {
		if err := receive(t, ch, "StartPolling"); err != nil {
			t.Fatalf("StartPolling: %v", err)
		}
	}
	if n := pollers(); n != 1 {
		t.Errorf("%d poll goroutines after two restarts, want 1", n)
	}
	if err := e.StopPolling(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	waitNoPollers(t)
	if st, err := e.State(ctx, info.ID); err != nil || st.Polling.Active {
		t.Errorf("polling = %+v, %v", st.Polling, err)
	}
}

// openHeld opens a session and takes its I/O token, as a read in progress
// would hold it, then starts CloseSession and waits until it waits for that
// token. The returned function releases the token and waits for the close.
func openHeld(t *testing.T, e *Engine, d *sim) (*session, func()) {
	t.Helper()
	info := mustOpen(t, e, d)
	s, err := e.session(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- e.CloseSession(ctx, info.ID) }()
	waitFor(t, "CloseSession to wait for the I/O token", func() bool {
		return blocked("engine.(*session).close(", "chan send")
	})
	return s, func() {
		s.release()
		if err := receive(t, closed, "CloseSession"); err != nil {
			t.Errorf("CloseSession: %v", err)
		}
	}
}

// A StartPolling that found the session before CloseSession removed it, and
// runs while the close waits for a read to end, starts nothing.
func TestStartPollingRacesClose(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, time.Hour)
	s, finish := openHeld(t, e, d)

	err := s.startPolling(model.PollConfig{IntervalS: 1})
	finish()
	if service.CodeOf(err) != service.CodeNotFound {
		t.Errorf("startPolling on a closing session = %v, want not_found", err)
	}
	waitNoPollers(t)
	e.Close()
	waitNoPollers(t)
}

// The same for a recording: none runs after CloseSession.
func TestStartRecordingRacesClose(t *testing.T) {
	d := newSim(t)
	e := recEngine(t, t.TempDir(), RecordingLimits{})
	s, finish := openHeld(t, e, d)

	_, err := s.startRecording(model.RecordingConfig{Name: "late"})
	finish()
	if service.CodeOf(err) != service.CodeNotFound {
		t.Errorf("startRecording on a closing session = %v, want not_found", err)
	}
	list, _ := e.Recordings(ctx)
	for _, r := range list {
		if r.State == model.RecordingRunning {
			t.Errorf("recording %s runs after its session closed", r.Name)
		}
	}
}

// L1, L2: CloseSession twice and Engine.Close twice, all at once, while the
// session polls and records. Every call returns; the session closes once;
// nothing of it is left. Closing again changes nothing.
func TestCloseConcurrently(t *testing.T) {
	d := newSim(t)
	e := recEngine(t, t.TempDir(), RecordingLimits{})
	ch := events(t, e)
	info := mustOpen(t, e, d)
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "run"}); err != nil {
		t.Fatal(err)
	}
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "a recorded sample", recordingSamples(1))

	start := make(chan struct{})
	sessions := make(chan error, 2)
	engines := make(chan struct{}, 2)
	for range 2 {
		go func() { <-start; sessions <- e.CloseSession(ctx, info.ID) }()
		go func() { <-start; e.Close(); engines <- struct{}{} }()
	}
	close(start)
	ok := 0
	for range 2 {
		switch err := receive(t, sessions, "CloseSession"); {
		case err == nil:
			ok++
		case service.CodeOf(err) != service.CodeNotFound:
			t.Errorf("CloseSession = %v, want nil or not_found", err)
		}
	}
	for range 2 {
		receive(t, engines, "Engine.Close")
	}
	if ok > 1 {
		t.Errorf("%d CloseSession calls closed the session", ok)
	}

	// L1: again, one after the other.
	if err := e.CloseSession(ctx, info.ID); service.CodeOf(err) != service.CodeNotFound {
		t.Errorf("CloseSession after close = %v, want not_found", err)
	}
	e.Close()
	if list, _ := e.Sessions(ctx); len(list) != 0 {
		t.Errorf("sessions after close = %+v", list)
	}
	waitNoPollers(t)
	recs, _ := e.Recordings(ctx)
	if len(recs) != 1 || recs[0].State != model.RecordingStopped {
		t.Errorf("recordings after close = %+v", recs)
	}
	// The stream says "closed" once; everything was published before the
	// calls returned.
	closedEvents := 0
	for _, ev := range drain(ch) {
		if ev.Kind == model.EventSessionStatus && ev.Session.Status == model.SessionClosed {
			closedEvents++
		}
	}
	if closedEvents != 1 {
		t.Errorf("%d closed events, want 1", closedEvents)
	}
}

// holdFirstRead makes the device keep the next read request unanswered
// until release is called (or the test ends). entered is closed when that
// request reaches the device.
func holdFirstRead(t *testing.T, d *sim) (entered <-chan struct{}, release func()) {
	in, gate := make(chan struct{}), make(chan struct{})
	var held, released sync.Once
	release = func() { released.Do(func() { close(gate) }) }
	t.Cleanup(release)
	d.setFilter(func(_, _ uint16, regs []uint16, err error) ([]uint16, error) {
		held.Do(func() {
			close(in)
			<-gate
		})
		return regs, err
	})
	return in, release
}

// L3: CloseSession while a read is blocked in the device. The close waits
// for the read, which the request timeout ends, and returns after it.
func TestCloseSessionDuringBlockedRead(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, time.Second)
	cfg := d.cfg()
	cfg.TimeoutMS = 200
	info, err := e.OpenSession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	entered, _ := holdFirstRead(t, d)

	type result struct {
		snap model.Snapshot
		err  error
	}
	read := make(chan result, 1)
	go func() {
		snap, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{})
		read <- result{snap, err}
	}()
	receive(t, entered, "the read to reach the device")
	closed := make(chan error, 1)
	go func() { closed <- e.CloseSession(ctx, info.ID) }()

	r := receive(t, read, "the blocked read to end")
	if r.err != nil || len(r.snap.Models) != len(stdModels()) || r.snap.Models[0].Status != model.ModelError {
		t.Fatalf("Snapshot = %d models, %v; want all, the first failed", len(r.snap.Models), r.err)
	}
	if err := receive(t, closed, "CloseSession"); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if _, err := e.State(ctx, info.ID); service.CodeOf(err) != service.CodeNotFound {
		t.Errorf("State after close = %v", err)
	}
}

// L5, L14: Engine.Close while OpenSession is still discovering. The open
// fails, no session is listed, and nothing it opened is left.
func TestEngineCloseDuringOpen(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, time.Second)
	entered, release := holdFirstRead(t, d)
	cfg := d.cfg()
	cfg.TimeoutMS = 10000 // the held read ends by release, not by the timeout
	base := takeBaseline()

	open := make(chan error, 1)
	go func() {
		_, err := e.OpenSession(ctx, cfg)
		open <- err
	}()
	receive(t, entered, "discovery to reach the device")
	e.Close()
	release()

	if err := receive(t, open, "OpenSession"); service.CodeOf(err) != service.CodeInternal || !strings.Contains(err.Error(), "closed") {
		t.Errorf("OpenSession during Close = %v, want the engine closed", err)
	}
	if list, _ := e.Sessions(ctx); len(list) != 0 {
		t.Errorf("sessions = %+v", list)
	}
	base.check(t)
}
