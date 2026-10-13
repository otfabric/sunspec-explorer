// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	sunspec "github.com/otfabric/go-sunspec"

	"github.com/otfabric/sunspec-explorer/internal/model"
)

// Chain positions of the models of stdModels.
const (
	idxCommon = iota
	idxInverter
	idxMPPT1
	idxMPPT2
	idxVoltVar
	idxVendor
)

const vendorModelID = 64900

// stdModels is the simulated device most tests read: Common, a three phase
// inverter, two MPPT models (the same ID twice) with two modules each, a
// volt-var model with nested curve groups, and a vendor model without schema.
func stdModels() []sunspec.ServerModel {
	common := (&sunspec.Values{}).
		Set("Mn", "OT Fabric").Set("Md", "SimInverter").Set("Opt", "opt").
		Set("Vr", "1.2.3").Set("SN", "SN-0001").Set("DA", 1)

	inverter := (&sunspec.Values{}).
		Set("A_SF", -1).Set("A", 12.3).Set("AphA", 4.1).
		Set("V_SF", -1).Set("PhVphA", 230.5).
		Set("W_SF", 1).Set("W", 4200).
		Set("Hz_SF", -2).Set("Hz", 50.01).
		Set("WH_SF", 0).Set("WH", 123456).
		Set("St", "MPPT").Set("Evt1", []string{"GROUND_FAULT", "OVER_TEMP"})

	mppt := func(first string) *sunspec.Values {
		v := (&sunspec.Values{}).Set("DCA_SF", -1).Set("DCV_SF", -1).Set("DCW_SF", 0).Set("DCWH_SF", 0)
		v.Add("module", (&sunspec.Values{}).Set("ID", 1).Set("IDStr", first).Set("DCA", 5.5).Set("DCV", 400.0).Set("DCW", 2200))
		v.Add("module", (&sunspec.Values{}).Set("ID", 2).Set("IDStr", "B").Set("DCA", 4.5).Set("DCV", 390.0).Set("DCW", 1755))
		return v
	}

	voltVar := (&sunspec.Values{}).Set("Ena", 1).Set("V_SF", -2).Set("DeptRef_SF", -2).Set("RspTms_SF", 0)
	for c := 0; c < 2; c++ {
		crv := voltVar.Add("Crv", (&sunspec.Values{}).Set("ActPt", 3).Set("VRef", 1.0))
		for p := 0; p < 3; p++ {
			crv.Add("Pt", (&sunspec.Values{}).Set("V", 0.9+0.05*float64(p)).Set("Var", 0.1*float64(p)))
		}
	}

	return []sunspec.ServerModel{
		{ID: 1, Values: common},
		{ID: 103, Values: inverter},
		{ID: 160, Values: mppt("A")},
		{ID: 160, Values: mppt("second")},
		{ID: 705, Values: voltVar},
		{ID: vendorModelID, Registers: []uint16{0x0001, 0x0203, 0xFFFF, 0x8000}},
	}
}

// readFilter may replace the answer to a read request.
type readFilter func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error)

// sim is a simulated SunSpec device on a loopback port. Its handler can be
// swapped for another device and its answers filtered while it runs.
type sim struct {
	t    testing.TB
	addr string

	mu      sync.Mutex
	srv     *sunspec.Server
	filter  readFilter
	stopped bool
	ms      *modbus.Server

	requests atomic.Int64
	inFlight atomic.Int32
	// maxInFlight is the highest number of requests served at the same time.
	maxInFlight atomic.Int32
}

func newSim(t testing.TB, models ...sunspec.ServerModel) *sim {
	t.Helper()
	if len(models) == 0 {
		models = stdModels()
	}
	d := &sim{t: t}
	d.setModels(models...)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d.addr = l.Addr().String()
	_ = l.Close()
	d.start()
	t.Cleanup(d.stop)
	return d
}

func (d *sim) url() string { return "tcp://" + d.addr }

func (d *sim) cfg() model.TargetConfig {
	return model.TargetConfig{URL: d.url(), UnitID: 1, TimeoutMS: 500}
}

// setModels replaces the device behind the address.
func (d *sim) setModels(models ...sunspec.ServerModel) {
	d.t.Helper()
	srv, err := sunspec.NewServer(&sunspec.ServerOptions{UnitID: 1, BaseAddress: 40000}, models...)
	if err != nil {
		d.t.Fatalf("sunspec.NewServer: %v", err)
	}
	d.mu.Lock()
	d.srv = srv
	d.mu.Unlock()
}

func (d *sim) setFilter(f readFilter) {
	d.mu.Lock()
	d.filter = f
	d.mu.Unlock()
}

func (d *sim) server() *sunspec.Server {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.srv
}

func (d *sim) start() {
	d.t.Helper()
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		var ms *modbus.Server
		ms, err = modbus.NewServer(&modbus.ServerConfig{
			URL: d.url(), MaxClients: 16, Logger: modbus.NopLogger(), Timeout: 30 * time.Second,
		}, d)
		if err != nil {
			d.t.Fatalf("modbus.NewServer: %v", err)
		}
		if err = ms.Start(); err == nil {
			d.mu.Lock()
			d.ms, d.stopped = ms, false
			d.mu.Unlock()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	d.t.Fatalf("server start: %v", err)
}

func (d *sim) stop() {
	d.mu.Lock()
	ms, stopped := d.ms, d.stopped
	d.stopped = true
	d.mu.Unlock()
	if ms != nil && !stopped {
		_ = ms.Stop()
	}
}

// failRange makes every read that touches [from, to] fail with err.
func failRange(from, to uint16, err error) readFilter {
	return func(addr, quantity uint16, regs []uint16, rerr error) ([]uint16, error) {
		if int(addr) <= int(to) && int(addr)+int(quantity)-1 >= int(from) {
			return nil, err
		}
		return regs, rerr
	}
}

func (d *sim) HandleCoils(ctx context.Context, req *modbus.CoilsRequest) ([]bool, error) {
	return d.server().HandleCoils(ctx, req)
}

func (d *sim) HandleDiscreteInputs(ctx context.Context, req *modbus.DiscreteInputsRequest) ([]bool, error) {
	return d.server().HandleDiscreteInputs(ctx, req)
}

func (d *sim) HandleHoldingRegisters(ctx context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	d.enter()
	defer d.inFlight.Add(-1)
	d.mu.Lock()
	srv, f := d.srv, d.filter
	d.mu.Unlock()
	regs, err := srv.HandleHoldingRegisters(ctx, req)
	if f != nil && !req.IsWrite {
		return f(req.Addr, req.Quantity, regs, err)
	}
	return regs, err
}

func (d *sim) HandleInputRegisters(ctx context.Context, req *modbus.InputRegistersRequest) ([]uint16, error) {
	d.enter()
	defer d.inFlight.Add(-1)
	return d.server().HandleInputRegisters(ctx, req)
}

func (d *sim) enter() {
	d.requests.Add(1)
	n := d.inFlight.Add(1)
	for {
		m := d.maxInFlight.Load()
		if n <= m || d.maxInFlight.CompareAndSwap(m, n) {
			return
		}
	}
}

// newEngine returns an engine whose "second" lasts unit, closed with the test.
func newEngine(t testing.TB, unit time.Duration) *Engine {
	t.Helper()
	e := New(Options{})
	e.second = unit
	t.Cleanup(e.Close)
	return e
}

// events subscribes to e and returns the stream, ended with the test.
func events(t testing.TB, e *Engine) <-chan model.Event {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ch, err := e.Subscribe(ctx, 0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return ch
}

// waitEvent returns the first event that matches, failing after a timeout.
func waitEvent(t testing.TB, ch <-chan model.Event, what string, match func(model.Event) bool) model.Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
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

func isSample(ev model.Event) bool { return ev.Kind == model.EventSample }

// findModel returns the result at a chain position.
func findModel(t testing.TB, models []model.ModelResult, index int) model.ModelResult {
	t.Helper()
	for _, m := range models {
		if m.Index == index {
			return m
		}
	}
	t.Fatalf("no model at index %d in %d results", index, len(models))
	return model.ModelResult{}
}

func findPoint(t testing.TB, g *model.GroupView, name string) model.PointView {
	t.Helper()
	if g == nil {
		t.Fatalf("no group while looking for point %s", name)
	}
	for _, p := range g.Points {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no point %s in group %s", name, g.Name)
	return model.PointView{}
}
