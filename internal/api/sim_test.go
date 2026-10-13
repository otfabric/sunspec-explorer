// SPDX-License-Identifier: MIT

package api_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	sunspec "github.com/otfabric/go-sunspec"

	"github.com/otfabric/sunspec-explorer/internal/model"
)

const vendorModelID = 64900

// stdModels is the simulated device of the API tests: Common, a three phase
// inverter, two MPPT models, a volt-var model with nested groups and a vendor
// model without schema.
func stdModels() []sunspec.ServerModel {
	common := (&sunspec.Values{}).Set("Mn", "OT Fabric").Set("Md", "SimInverter").Set("Vr", "1.2.3").Set("SN", "SN-0001").Set("DA", 1)
	inverter := (&sunspec.Values{}).Set("A_SF", -1).Set("A", 12.3).Set("W_SF", 1).Set("W", 4200).Set("St", "MPPT")
	mppt := func() *sunspec.Values {
		v := (&sunspec.Values{}).Set("DCA_SF", -1).Set("DCV_SF", -1).Set("DCW_SF", 0).Set("DCWH_SF", 0)
		v.Add("module", (&sunspec.Values{}).Set("ID", 1).Set("DCA", 5.5))
		v.Add("module", (&sunspec.Values{}).Set("ID", 2).Set("DCA", 4.5))
		return v
	}
	voltVar := (&sunspec.Values{}).Set("Ena", 1).Set("V_SF", -2).Set("DeptRef_SF", -2).Set("RspTms_SF", 0)
	crv := voltVar.Add("Crv", (&sunspec.Values{}).Set("ActPt", 2))
	crv.Add("Pt", (&sunspec.Values{}).Set("V", 0.9))
	crv.Add("Pt", (&sunspec.Values{}).Set("V", 1.1))
	return []sunspec.ServerModel{
		{ID: 1, Values: common},
		{ID: 103, Values: inverter},
		{ID: 160, Values: mppt()},
		{ID: 160, Values: mppt()},
		{ID: 705, Values: voltVar},
		{ID: vendorModelID, Registers: []uint16{1, 2, 3, 4}},
	}
}

// sim is a simulated SunSpec device on a loopback port whose chain can be
// replaced while it runs.
type sim struct {
	addr string
	mu   sync.Mutex
	srv  *sunspec.Server
}

func newSim(t testing.TB, models ...sunspec.ServerModel) *sim {
	t.Helper()
	d := &sim{}
	d.setModels(t, models...)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d.addr = l.Addr().String()
	_ = l.Close()
	var ms *modbus.Server
	for attempt := 0; attempt < 20; attempt++ {
		ms, err = modbus.NewServer(&modbus.ServerConfig{
			URL: "tcp://" + d.addr, MaxClients: 16, Logger: modbus.NopLogger(), Timeout: 30 * time.Second,
		}, d)
		if err != nil {
			t.Fatal(err)
		}
		if err = ms.Start(); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("device start: %v", err)
	}
	t.Cleanup(func() { _ = ms.Stop() })
	return d
}

func (d *sim) setModels(t testing.TB, models ...sunspec.ServerModel) {
	t.Helper()
	if len(models) == 0 {
		models = stdModels()
	}
	srv, err := sunspec.NewServer(&sunspec.ServerOptions{UnitID: 1, BaseAddress: 40000}, models...)
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.srv = srv
	d.mu.Unlock()
}

func (d *sim) cfg() model.TargetConfig {
	return model.TargetConfig{URL: "tcp://" + d.addr, UnitID: 1, TimeoutMS: 500}
}

func (d *sim) server() *sunspec.Server {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.srv
}

func (d *sim) HandleCoils(ctx context.Context, req *modbus.CoilsRequest) ([]bool, error) {
	return d.server().HandleCoils(ctx, req)
}

func (d *sim) HandleDiscreteInputs(ctx context.Context, req *modbus.DiscreteInputsRequest) ([]bool, error) {
	return d.server().HandleDiscreteInputs(ctx, req)
}

func (d *sim) HandleHoldingRegisters(ctx context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	return d.server().HandleHoldingRegisters(ctx, req)
}

func (d *sim) HandleInputRegisters(ctx context.Context, req *modbus.InputRegistersRequest) ([]uint16, error) {
	return d.server().HandleInputRegisters(ctx, req)
}
