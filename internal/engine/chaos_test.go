// SPDX-License-Identifier: MIT

package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// fault is what the chaos proxy does to one request.
type fault int

const (
	faultNone      fault = iota
	faultDrop            // the request is swallowed: no answer, a timeout
	faultDelay           // the answer comes late, within the timeout
	faultLate            // the answer comes after the timeout
	faultCorrupt         // one data byte of the answer is flipped
	faultTruncate        // the answer stops halfway and the connection closes
	faultClose           // the connection closes instead of the answer
	faultWrongTxn        // the answer carries another transaction number
	faultGarbage         // bytes that are no frame come first
	faultException       // a Modbus exception instead of the data
	faultSplit           // the answer arrives in two pieces
	numFaults
)

var faultNames = [numFaults]string{
	"none", "drop", "delay", "late", "corrupt", "truncate", "close", "wrong-txn", "garbage", "exception", "split",
}

func (f fault) String() string { return faultNames[f] }

// proxyMode is the state of the path to the device.
type proxyMode int32

const (
	modeClean   proxyMode = iota // every frame passes
	modeFaulty                   // the seed's faults apply
	modeSilent                   // nothing is answered
	modeRefused                  // connections are closed as they arrive
)

// chaosProxy stands between the engine and a simulated device and misbehaves
// on the Modbus/TCP frames that pass. A rand.Rand seeded by the test decides
// the fate of each request in the order they arrive, so that the N-th request
// of a seed always meets the same fault, however long the machine took to get
// there. It also records what the engine put on the wire.
type chaosProxy struct {
	listener net.Listener
	upstream string
	mode     atomic.Int32
	// lateBy is how long faultLate waits: longer than the client's timeout.
	lateBy time.Duration

	mu        sync.Mutex
	rng       *rand.Rand
	conns     map[net.Conn]struct{}
	faults    [numFaults]int
	functions map[byte]int
	requests  int
	accepted  int

	wg sync.WaitGroup
}

func newChaosProxy(t testing.TB, upstream string, seed uint64, lateBy time.Duration) *chaosProxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &chaosProxy{
		listener: l, upstream: upstream, lateBy: lateBy,
		rng:       rand.New(rand.NewPCG(seed, 0x5375_6e53)),
		conns:     make(map[net.Conn]struct{}),
		functions: make(map[byte]int),
	}
	p.wg.Add(1)
	go p.accept()
	return p
}

func (p *chaosProxy) url() string { return "tcp://" + p.listener.Addr().String() }

func (p *chaosProxy) setMode(m proxyMode) { p.mode.Store(int32(m)) }

// dropConns closes every connection, as a device that restarts would.
func (p *chaosProxy) dropConns() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for c := range p.conns {
		_ = c.Close()
	}
}

func (p *chaosProxy) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests
}

func (p *chaosProxy) close() {
	_ = p.listener.Close()
	p.dropConns()
	p.wg.Wait()
}

func (p *chaosProxy) track(c net.Conn) {
	p.mu.Lock()
	p.conns[c] = struct{}{}
	p.mu.Unlock()
}

func (p *chaosProxy) forget(c net.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
	_ = c.Close()
}

func (p *chaosProxy) accept() {
	defer p.wg.Done()
	for {
		c, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		p.accepted++
		p.mu.Unlock()
		if proxyMode(p.mode.Load()) == modeRefused {
			_ = c.Close()
			continue
		}
		p.track(c)
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer p.forget(c)
			p.serve(c)
		}()
	}
}

// pick draws the fate of a request. The weights are percentages.
func (p *chaosProxy) pick(function byte) fault {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests++
	p.functions[function]++
	f := faultNone
	switch proxyMode(p.mode.Load()) {
	case modeClean:
	case modeSilent, modeRefused:
		f = faultDrop
	case modeFaulty:
		// The draw happens whatever the outcome, one per request.
		switch r := p.rng.IntN(100); {
		case r < 4:
			f = faultDrop
		case r < 12:
			f = faultDelay
		case r < 15:
			f = faultLate
		case r < 25:
			f = faultCorrupt
		case r < 28:
			f = faultTruncate
		case r < 31:
			f = faultClose
		case r < 34:
			f = faultWrongTxn
		case r < 37:
			f = faultGarbage
		case r < 42:
			f = faultException
		case r < 47:
			f = faultSplit
		}
	}
	p.faults[f]++
	return f
}

// readFrame reads one Modbus/TCP frame.
func readFrame(r io.Reader) ([]byte, error) {
	frame := make([]byte, 6, 32)
	if _, err := io.ReadFull(r, frame); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(frame[4:]))
	frame = append(frame, make([]byte, n)...)
	if _, err := io.ReadFull(r, frame[6:]); err != nil {
		return nil, err
	}
	return frame, nil
}

func (p *chaosProxy) serve(client net.Conn) {
	device, err := net.Dial("tcp", p.upstream)
	if err != nil {
		return
	}
	p.track(device)
	defer p.forget(device)

	for {
		req, err := readFrame(client)
		if err != nil || len(req) < 8 {
			return
		}
		f := p.pick(req[7])
		if f == faultDrop {
			continue
		}
		if f == faultClose {
			return
		}
		if _, err := device.Write(req); err != nil {
			return
		}
		res, err := readFrame(device)
		if err != nil {
			return
		}
		if !p.answer(client, f, res) {
			return
		}
	}
}

// answer sends res to the client with the fault applied. It reports false
// when the connection must close.
func (p *chaosProxy) answer(client net.Conn, f fault, res []byte) bool {
	switch f {
	case faultDelay:
		time.Sleep(p.lateBy / 8)
	case faultLate:
		time.Sleep(p.lateBy)
	case faultCorrupt:
		// Which byte follows from the frame, so that it takes no second draw
		// from the shared generator. Nothing in Modbus/TCP protects any of them.
		h := binary.BigEndian.Uint16(res[:2])
		res[6+int(h)%(len(res)-6)] ^= byte(1 << (h % 8))
	case faultTruncate:
		_, _ = client.Write(res[:len(res)/2])
		return false
	case faultWrongTxn:
		res[1] ^= 0x55
	case faultGarbage:
		if _, err := client.Write([]byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x03, 0x01}); err != nil {
			return false
		}
	case faultException:
		res = append(res[:4:4], 0, 3, res[6], res[7]|0x80, 4)
	case faultSplit:
		if _, err := client.Write(res[:3]); err != nil {
			return false
		}
		time.Sleep(time.Millisecond)
		res = res[3:]
	default:
	}
	_, err := client.Write(res)
	return err == nil
}

// chaosSeeds returns the seeds of a run: SUNSPEC_EXPLORER_CHAOS_SEED replays
// one, SUNSPEC_EXPLORER_CHAOS_ITERATIONS says how many run (default 2).
func chaosSeeds(t *testing.T) []uint64 {
	t.Helper()
	if s := os.Getenv("SUNSPEC_EXPLORER_CHAOS_SEED"); s != "" {
		seed, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			t.Fatalf("SUNSPEC_EXPLORER_CHAOS_SEED: %v", err)
		}
		return []uint64{seed}
	}
	n := 2
	if s := os.Getenv("SUNSPEC_EXPLORER_CHAOS_ITERATIONS"); s != "" {
		var err error
		if n, err = strconv.Atoi(s); err != nil {
			t.Fatalf("SUNSPEC_EXPLORER_CHAOS_ITERATIONS: %v", err)
		}
	}
	seeds := make([]uint64, n)
	for i := range seeds {
		seeds[i] = uint64(1000 + i)
	}
	return seeds
}

// baseline is what a test started with, to tell what it left behind.
type baseline struct {
	goroutines int
	files      int
}

func takeBaseline() baseline {
	return baseline{goroutines: runtime.NumGoroutine(), files: openFiles()}
}

// openFiles counts this process's open descriptors, or -1 where that cannot be
// told.
func openFiles() int {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return -1
	}
	return len(entries)
}

// check fails when goroutines or descriptors outlive what started them. They
// get a moment: a goroutine that was told to stop may not have run yet, and a
// closed socket's poller entry goes with the next garbage collection.
func (b baseline) check(t testing.TB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		g, f := runtime.NumGoroutine(), openFiles()
		if g <= b.goroutines && f <= b.files {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			buf = buf[:runtime.Stack(buf, true)]
			t.Fatalf("leak: %d goroutines (%d before), %d descriptors (%d before)\n%s", g, b.goroutines, f, b.files, buf)
		}
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
}

// watcher checks the event stream of an engine as it arrives.
type watcher struct {
	t       testing.TB
	session string

	eventSeq  uint64
	sampleSeq uint64
	lastOK    map[int]time.Time

	mu       sync.Mutex
	samples  int
	statuses map[string]int
}

func newWatcher(t testing.TB, session string) *watcher {
	return &watcher{t: t, session: session, lastOK: make(map[int]time.Time), statuses: make(map[string]int)}
}

func (w *watcher) sampleCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.samples
}

func (w *watcher) sawStatus(status string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.statuses[status]
}

// watch reads events until the stream ends.
func (w *watcher) watch(ch <-chan model.Event) {
	for ev := range ch {
		w.event(ev)
	}
}

func (w *watcher) event(ev model.Event) {
	t := w.t
	if ev.Kind == model.EventResync {
		// A resync says what was missed: the numbers go on from there.
		w.eventSeq, w.sampleSeq = ev.Seq, 0
		clear(w.lastOK)
		return
	}
	if w.eventSeq != 0 && ev.Seq != w.eventSeq+1 {
		t.Errorf("event %d follows %d without a resync", ev.Seq, w.eventSeq)
	}
	w.eventSeq = ev.Seq
	switch ev.Kind {
	case model.EventSessionStatus:
		if ev.Session != nil && ev.SessionID == w.session {
			w.mu.Lock()
			w.statuses[ev.Session.Status]++
			w.mu.Unlock()
		}
	case model.EventSample:
		w.sample(ev.Sample)
	}
}

func (w *watcher) sample(s *model.Sample) {
	t := w.t
	if s.SessionID != w.session {
		return
	}
	// A poller numbers its cycles from 1; a restart begins again.
	if s.Seq != 1 && w.sampleSeq != 0 && s.Seq != w.sampleSeq+1 {
		t.Errorf("sample %d follows %d", s.Seq, w.sampleSeq)
	}
	w.sampleSeq = s.Seq
	for i := range s.Models {
		m := &s.Models[i]
		switch m.Status {
		case model.ModelOK:
			if m.Error != nil || m.Stale {
				t.Errorf("sample %d model %d is ok with error %v, stale %v", s.Seq, m.Index, m.Error, m.Stale)
			}
		case model.ModelError, model.ModelChanged:
			if m.Error == nil || *m.Error == "" || m.Group != nil || m.RawRegisters != nil {
				t.Errorf("sample %d model %d is %s but has no error, or has data: %+v", s.Seq, m.Index, m.Status, m)
			}
		default:
			t.Errorf("sample %d model %d has status %q", s.Seq, m.Index, m.Status)
		}
		if m.LastOKAt != nil {
			if at, ok := w.lastOK[m.Index]; ok && m.LastOKAt.Before(at) {
				t.Errorf("sample %d model %d: last good read went back from %v to %v", s.Seq, m.Index, at, *m.LastOKAt)
			}
			w.lastOK[m.Index] = *m.LastOKAt
		}
	}
	w.mu.Lock()
	w.samples++
	w.mu.Unlock()
}

// waitUntil polls cond until it holds or the deadline passes.
func waitUntil(t testing.TB, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// bounded runs f and fails when it takes longer than d: whatever the device
// does, a call into the engine comes back.
func bounded(t testing.TB, what string, d time.Duration, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(d):
		buf := make([]byte, 1<<20)
		t.Fatalf("%s did not return within %v\n%s", what, d, buf[:runtime.Stack(buf, true)])
	}
}

// operator is the user at the other end while the device misbehaves: it reads,
// exports, rediscovers and restarts polling. Its calls may fail; they may not
// fail with anything but a service error that says so, and they must return.
func operator(ctx context.Context, t testing.TB, e *Engine, id string, rng *rand.Rand, tick time.Duration) {
	acceptable := func(what string, err error) {
		if err == nil || ctx.Err() != nil {
			return
		}
		switch service.CodeOf(err) {
		case service.CodeConnectFailed, service.CodeDiscoveryFailed, service.CodeNotSunSpec:
		default:
			t.Errorf("%s: %v", what, err)
		}
	}
	for ctx.Err() == nil {
		// One draw per step, so that a seed's operator does the same things.
		op, wait := rng.IntN(100), time.Duration(rng.IntN(8)+1)*tick
		switch {
		case op < 40:
			_, err := e.Snapshot(ctx, id, model.SnapshotRequest{IncludeRaw: op%2 == 0})
			acceptable("Snapshot", err)
		case op < 60:
			if _, err := e.State(ctx, id); err != nil {
				t.Errorf("State: %v", err)
			}
		case op < 70:
			_, err := e.Rediscover(ctx, id)
			acceptable("Rediscover", err)
		case op < 80:
			_, err := e.ExportSession(ctx, id, model.ExportCSV)
			acceptable("CSV export", err)
		case op < 85:
			_, err := e.ExportSession(ctx, id, model.ExportDeviceFile)
			acceptable("device file", err)
		case op < 95:
			if err := e.StopPolling(ctx, id); err != nil {
				t.Errorf("StopPolling: %v", err)
			}
			if err := e.StartPolling(ctx, id, model.PollConfig{IntervalS: 1}); err != nil {
				t.Errorf("StartPolling: %v", err)
			}
		default:
			if err := e.StartPolling(ctx, id, model.PollConfig{IntervalS: 2}); err != nil {
				t.Errorf("StartPolling: %v", err)
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

// checkRecording reads a stopped recording back. It validates every line
// against the published schema, and holds the header, the footer and the
// sidecar's counters to what the lines say.
func checkRecording(t testing.TB, e *Engine, sch *schemas, info model.RecordingInfo) {
	t.Helper()
	raw, lines := readRecording(t, e, info.ID)
	rows := bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n"))
	if int64(len(raw)) != info.SizeBytes {
		t.Errorf("recording has %d bytes, its counter says %d", len(raw), info.SizeBytes)
	}
	if len(rows) != len(lines) || len(lines) < 2 {
		t.Fatalf("%d rows, %d lines", len(rows), len(lines))
	}
	if lines[0].Type != model.LineHeader || lines[len(lines)-1].Type != model.LineFooter {
		t.Errorf("first line %q, last line %q", lines[0].Type, lines[len(lines)-1].Type)
	}
	var samples, failures uint64
	for i, row := range rows {
		if err := sch.raw("", row); err != nil {
			t.Errorf("line %d (%s): %v\n%.500s", i+1, lines[i].Type, err, row)
		}
		if lines[i].Type != model.LineSample {
			continue
		}
		samples++
		if slices.ContainsFunc(lines[i].Models, func(m model.ModelResult) bool { return m.Status != model.ModelOK }) {
			failures++
		}
	}
	footer := lines[len(lines)-1]
	if footer.Samples != samples || footer.Failures != failures || info.Samples != samples || info.Failures != failures {
		t.Errorf("%d samples and %d failures in the file; footer %d, %d; counters %d, %d",
			samples, failures, footer.Samples, footer.Failures, info.Samples, info.Failures)
	}
}

// TestChaosPolling drives a session through a device that drops, delays and
// corrupts its answers, while an operator keeps using the session. Everything
// random comes from the seed: replay a failure with
// SUNSPEC_EXPLORER_CHAOS_SEED=<seed>. Corrupted answers are not always
// detectable: Modbus/TCP has no checksum, so a flipped data byte is a wrong
// value, which no reader can tell. The invariants are what must hold anyway:
//
//  1. Read-only: only FC03 reaches the wire, and the guard counts no refused
//     request (R11).
//  2. Event numbers rise by one, and sample numbers by one within a poller,
//     unless a resync says otherwise.
//  3. A model result is either ok, without an error, or failed, with an error
//     and no data. Its last good read never goes back in time.
//  4. No call into the engine hangs, in a fault or in a full outage: all of
//     them return within a fixed bound, and so do stopping and closing.
//  5. An outage is seen: the session goes to reconnecting, and while nothing
//     answers the requests stay spaced by the timeout and the backoff.
//  6. It heals by itself: once the faults end the session is connected and
//     delivers samples in which every model is ok, with the device's real
//     chain after at most one re-discovery, without a new session.
//  7. The recording survives all of it: every line is complete and matches the
//     published schema, and the footer and the counters agree with the lines.
//  8. Nothing is left behind: goroutines and descriptors are back to where
//     they were once the session and the proxy are closed.
func TestChaosPolling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped with -short")
	}
	for _, seed := range chaosSeeds(t) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { chaosRun(t, seed) })
	}
}

func chaosRun(t *testing.T, seed uint64) {
	const (
		unit     = 5 * time.Millisecond // the engine's "second"
		timeout  = 100 * time.Millisecond
		bound    = 15 * time.Second // the longest any call may take
		faultReq = 250              // requests served in the faulty phase
	)
	t.Logf("replay with SUNSPEC_EXPLORER_CHAOS_SEED=%d", seed)
	rng := rand.New(rand.NewPCG(seed, 1))
	sch := newSchemas(t, recordingSchemaFile)
	base := takeBaseline()

	d := newSim(t)
	proxy := newChaosProxy(t, d.addr, seed, 2*timeout)
	e := New(Options{DataDir: t.TempDir(), Recording: RecordingLimits{RotateBytes: 64 << 10, MinFreeBytes: -1}})
	e.second = unit
	cfg := model.TargetConfig{URL: proxy.url(), UnitID: 1, TimeoutMS: int(timeout / time.Millisecond)}

	// Opening through the faults either works or says why.
	proxy.setMode(modeFaulty)
	for range 3 {
		bounded(t, "OpenSession", bound, func() {
			info, err := e.OpenSession(ctx, cfg)
			switch {
			case err == nil:
				if err := e.CloseSession(ctx, info.ID); err != nil {
					t.Errorf("CloseSession: %v", err)
				}
			case !slices.Contains([]string{service.CodeConnectFailed, service.CodeDiscoveryFailed, service.CodeNotSunSpec}, service.CodeOf(err)):
				t.Errorf("OpenSession through faults: %v", err)
			}
		})
	}

	proxy.setMode(modeClean)
	info, err := e.OpenSession(ctx, cfg)
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
	rec, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "chaos"})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the first samples", bound, func() bool { return w.sampleCount() >= 3 })

	// The faulty phase counts requests, not time: a seed means the same
	// fault plan on a loaded machine.
	opCtx, stopOperator := context.WithCancel(context.Background())
	var operating sync.WaitGroup
	operating.Add(1)
	go func() {
		defer operating.Done()
		operator(opCtx, t, e, info.ID, rand.New(rand.NewPCG(seed, 2)), unit)
	}()
	proxy.setMode(modeFaulty)
	start := proxy.requestCount()
	waitUntil(t, "the faulty phase", 2*time.Minute, func() bool { return proxy.requestCount()-start >= faultReq })

	// A full outage, of a kind the seed picks: silence, or a device that hangs
	// up.
	outage := modeSilent
	if rng.IntN(2) == 0 {
		outage = modeRefused
	}
	stopOperator()
	bounded(t, "the operator", bound, operating.Wait)
	bounded(t, "StopPolling", bound, func() {
		if err := e.StopPolling(ctx, info.ID); err != nil {
			t.Errorf("StopPolling: %v", err)
		}
	})
	proxy.setMode(outage)
	proxy.dropConns()
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "reconnecting", bound, func() bool { return w.sawStatus(model.SessionReconnecting) > 0 })
	before, began := proxy.requestCount(), time.Now()
	time.Sleep(40 * unit)
	// One model in a poll cycle meets the timeout at a time; the backoff only
	// slows that down. Silent, each request costs the timeout; refused, it
	// costs a connection that never carries a frame.
	if outage == modeSilent {
		sent, most := proxy.requestCount()-before, int(time.Since(began)/timeout)+2
		if sent > most {
			t.Errorf("%d requests in %v of silence, at most %d expected", sent, time.Since(began), most)
		}
	}
	bounded(t, "a snapshot during the outage", bound, func() {
		snap, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{})
		if err != nil {
			t.Errorf("Snapshot during the outage: %v", err)
			return
		}
		for _, m := range snap.Models {
			if m.Status != model.ModelError {
				t.Errorf("model %d is %s during the outage", m.Index, m.Status)
			}
		}
	})

	// The device comes back.
	proxy.setMode(modeClean)
	proxy.dropConns()
	allOK := func(ms []model.ModelResult) bool {
		return !slices.ContainsFunc(ms, func(m model.ModelResult) bool { return m.Status != model.ModelOK })
	}
	want := stdChain()
	healed := func() bool {
		st, err := e.State(ctx, info.ID)
		if err != nil {
			t.Fatalf("State: %v", err)
		}
		return st.Session.Status == model.SessionConnected && st.LastSample != nil &&
			len(st.LastSample.Models) > 0 && allOK(st.LastSample.Models)
	}
	waitUntil(t, "the session to heal", bound, healed)

	// A corrupted chain walk can have ended early, and then nothing tells a
	// reader the chain is short: re-discovery puts it right.
	bounded(t, "Rediscover", bound, func() {
		st, err := e.Rediscover(ctx, info.ID)
		if err != nil {
			t.Errorf("Rediscover after the faults: %v", err)
			return
		}
		if !sameChain(st.Models, want) {
			t.Errorf("chain after healing = %+v, want %+v", st.Models, want)
		}
	})
	bounded(t, "a snapshot after the faults", bound, func() {
		snap, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{})
		if err != nil {
			t.Errorf("Snapshot: %v", err)
			return
		}
		if len(snap.Models) != len(want) || !allOK(snap.Models) {
			t.Errorf("snapshot after the faults: %+v", snap.Models)
		}
		if snap.Device == nil || snap.Device.Serial != "SN-0001" {
			t.Errorf("identity after the faults: %+v", snap.Device)
		}
	})

	// Stopping is prompt whatever the device does, and it is misbehaving again
	// while the session closes.
	proxy.setMode(modeSilent)
	bounded(t, "StopRecording", bound, func() {
		if _, err := e.StopRecording(ctx, rec.ID); err != nil {
			t.Errorf("StopRecording: %v", err)
		}
	})
	bounded(t, "CloseSession", bound, func() {
		if err := e.CloseSession(ctx, info.ID); err != nil {
			t.Errorf("CloseSession: %v", err)
		}
	})
	unsubscribe()
	bounded(t, "the event stream", bound, watching.Wait)

	recs, err := e.Recordings(ctx)
	if err != nil || len(recs) != 1 {
		t.Fatalf("Recordings = %+v, %v", recs, err)
	}
	if recs[0].Samples == 0 || recs[0].State != model.RecordingStopped {
		t.Errorf("recording = %+v", recs[0])
	}
	checkRecording(t, e, sch, recs[0])

	proxy.mu.Lock()
	functions, faults := proxy.functions, proxy.faults
	accepted, requests := proxy.accepted, proxy.requests
	proxy.mu.Unlock()
	if len(functions) != 1 || functions[3] == 0 {
		t.Errorf("function codes on the wire: %v, want FC03 only", functions)
	}
	if v := e.Guard().Violations(); v != 0 {
		t.Errorf("%d requests refused by the allow-list", v)
	}
	var seen []string
	injected := 0
	for f := faultNone + 1; f < numFaults; f++ {
		seen = append(seen, fmt.Sprintf("%s=%d", f, faults[f]))
		injected += faults[f]
	}
	if injected < faultReq/4 {
		t.Errorf("only %d faults in %d requests: the run proves little", injected, requests)
	}
	t.Logf("%d requests on %d connections, %d samples, faults %v", requests, accepted, recs[0].Samples, seen)

	e.Close()
	proxy.close()
	d.stop()
	base.check(t)
}

// stdChain is what discovery finds on the device of stdModels.
func stdChain() []model.ModelInfo {
	ids := []uint16{1, 103, 160, 160, 705, vendorModelID}
	out := make([]model.ModelInfo, len(ids))
	for i, id := range ids {
		out[i] = model.ModelInfo{Index: i, ID: id}
	}
	return out
}

func sameChain(got, want []model.ModelInfo) bool {
	return slices.EqualFunc(got, want, func(a, b model.ModelInfo) bool { return a.Index == b.Index && a.ID == b.ID })
}

// The proxy itself: without faults it is transparent, and each fault does what
// its name says to a plain Modbus exchange. A chaos test whose proxy does
// nothing would pass.
func TestChaosProxyFaults(t *testing.T) {
	d := newSim(t)
	proxy := newChaosProxy(t, d.addr, 1, 50*time.Millisecond)
	defer proxy.close()

	c, err := net.Dial("tcp", proxy.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	req := []byte{0, 7, 0, 0, 0, 6, 1, 3, 0x9c, 0x40, 0, 2}
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	res, err := readFrame(bufio.NewReader(c))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(res[:2], req[:2]) || res[7] != 3 || !bytes.Equal(res[9:], []byte("SunS")) {
		t.Fatalf("answer through the clean proxy: % x", res)
	}

	for f, check := range map[fault]func(sent, got []byte) error{
		faultCorrupt: func(sent, got []byte) error {
			if bytes.Equal(sent, got) {
				return errors.New("nothing changed")
			}
			return nil
		},
		faultWrongTxn: func(sent, got []byte) error {
			if bytes.Equal(sent[:2], got[:2]) {
				return errors.New("same transaction")
			}
			return nil
		},
		faultException: func(_, got []byte) error {
			if got[7] != 0x83 || got[8] != 4 {
				return fmt.Errorf("no exception: % x", got)
			}
			return nil
		},
		faultNone: func(sent, got []byte) error {
			if !bytes.Equal(sent, got) {
				return errors.New("changed")
			}
			return nil
		},
	} {
		sent := []byte{0, 7, 0, 0, 0, 7, 1, 3, 4, 0x53, 0x75, 0x6e, 0x53}
		var buf bytes.Buffer
		pr, pw := net.Pipe()
		go func() {
			defer func() { _ = pw.Close() }()
			proxy.answer(pw, f, slices.Clone(sent))
		}()
		if _, err := io.Copy(&buf, pr); err != nil {
			t.Fatal(err)
		}
		if err := check(sent, buf.Bytes()); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}
