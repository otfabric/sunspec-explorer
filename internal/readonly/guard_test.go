// SPDX-License-Identifier: MIT

package readonly

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	sunspec "github.com/otfabric/go-sunspec"
)

func TestAllowed(t *testing.T) {
	for _, fc := range modbus.KnownFunctionCodes() {
		want := fc == 0x03 || fc == 0x04
		if got := Allowed(fc); got != want {
			t.Errorf("Allowed(0x%02X) = %v, want %v", uint8(fc), got, want)
		}
	}
	if Allowed(0x41) {
		t.Error("a vendor function code is allowed")
	}
}

func TestDo(t *testing.T) {
	want := errors.New("plain")
	if err := Do(func() error { return want }); err != want {
		t.Errorf("Do = %v", err)
	}
	defer func() {
		if r := recover(); r != "other" {
			t.Errorf("recovered %v, want the foreign panic to continue", r)
		}
	}()
	_ = Do(func() error { panic("other") })
}

// device counts what reaches a served SunSpec device.
type device struct {
	*sunspec.Server
	writes atomic.Int64
}

func (d *device) HandleHoldingRegisters(ctx context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	if req.IsWrite {
		d.writes.Add(1)
	}
	return d.Server.HandleHoldingRegisters(ctx, req)
}

func serve(t *testing.T) (addr string, dev *device) {
	t.Helper()
	srv, err := sunspec.NewServer(&sunspec.ServerOptions{UnitID: 1, BaseAddress: 40000},
		sunspec.ServerModel{ID: 1, Values: (&sunspec.Values{}).Set("Mn", "x").Set("DA", 1)})
	if err != nil {
		t.Fatal(err)
	}
	dev = &device{Server: srv}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr = l.Addr().String()
	_ = l.Close()
	ms, err := modbus.NewServer(&modbus.ServerConfig{URL: "tcp://" + addr, MaxClients: 4, Logger: modbus.NopLogger()}, dev)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Stop() })
	return addr, dev
}

// The guard stops every request other than FC03/FC04 before it is sent: the
// wire stays clean and the device sees no write, although the test calls the
// client's write and diagnostic methods directly.
func TestGuardBlocksEverythingButReads(t *testing.T) {
	addr, dev := serve(t)
	tap, err := NewWireTap(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer tap.Close()

	guard := &Guard{}
	client, err := modbus.New(modbus.Config{URL: "tcp://" + tap.Addr(), Timeout: time.Second, Metrics: guard})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Open(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	// DA (device address) of Common is writable on this device: the address
	// of its register is base + 2 (marker) + 2 (header) + 64.
	const da = 40000 + 2 + 2 + 64
	blocked := map[string]func() error{
		"FC06 write register":   func() error { return client.WriteRegister(ctx, 1, da, 7) },
		"FC16 write registers":  func() error { return client.WriteRegisters(ctx, 1, da, []uint16{7}) },
		"FC05 write coil":       func() error { return client.WriteCoil(ctx, 1, 0, true) },
		"FC15 write coils":      func() error { return client.WriteCoils(ctx, 1, 0, []bool{true}) },
		"FC22 mask write":       func() error { return client.MaskWriteRegister(ctx, 1, da, 0, 7) },
		"FC23 read/write":       func() error { _, e := client.ReadWriteMultipleRegisters(ctx, 1, da, 1, da, []uint16{7}); return e },
		"FC08 diagnostics":      func() error { return client.DiagnosticClearCounters(ctx, 1) },
		"FC01 read coils":       func() error { _, e := client.ReadCoils(ctx, 1, 0, 1); return e },
		"FC02 read inputs":      func() error { _, e := client.ReadDiscreteInputs(ctx, 1, 0, 1); return e },
		"FC43 device identity":  func() error { _, e := client.ReadAllDeviceIdentification(ctx, 1); return e },
		"FC17 report server ID": func() error { _, e := client.ReportServerID(ctx, 1); return e },
	}
	for name, call := range blocked {
		var v *Violation
		if err := Do(call); !errors.As(err, &v) {
			t.Errorf("%s: err = %v, want a Violation", name, err)
		} else if v.Error() == "" || Allowed(v.FunctionCode) {
			t.Errorf("%s: violation = %v", name, v)
		}
	}
	if got := guard.Violations(); got != uint64(len(blocked)) {
		t.Errorf("violations = %d, want %d", got, len(blocked))
	}

	// The client is still usable, and reads pass.
	var regs []uint16
	if err := Do(func() error {
		var e error
		regs, e = client.ReadRegisters(ctx, 1, da, 1, modbus.HoldingRegister)
		return e
	}); err != nil || len(regs) != 1 || regs[0] != 1 {
		t.Fatalf("read after the violations = %v, %v", regs, err)
	}
	if err := Do(func() error {
		_, e := client.ReadRegisters(ctx, 1, da, 1, modbus.InputRegister)
		return e
	}); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("FC04 = %v, want the device's own refusal", err)
	}

	if got := tap.FunctionCodes(); !slices.Equal(got, []uint8{3, 4}) {
		t.Errorf("function codes on the wire = %v, want [3 4]", got)
	}
	if tap.Requests() != 2 || guard.Requests() != 2 {
		t.Errorf("requests: wire %d, guard %d, want 2", tap.Requests(), guard.Requests())
	}
	if dev.writes.Load() != 0 {
		t.Errorf("the device saw %d writes", dev.writes.Load())
	}
}

func TestWireTapUpstreamDown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := l.Addr().String()
	_ = l.Close()
	tap, err := NewWireTap(dead)
	if err != nil {
		t.Fatal(err)
	}
	defer tap.Close()
	c, err := net.Dial("tcp", tap.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Error("read from a tap without upstream succeeded")
	}
	if len(tap.FunctionCodes()) != 0 {
		t.Error("codes recorded")
	}
}
