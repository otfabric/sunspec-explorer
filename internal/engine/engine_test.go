// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	sunspec "github.com/otfabric/go-sunspec"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

var ctx = context.Background()

func mustOpen(t *testing.T, e *Engine, d *sim) model.SessionInfo {
	t.Helper()
	info, err := e.OpenSession(ctx, d.cfg())
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	return info
}

// A1, A3, A4: discovery lists every model instance in chain order.
func TestOpenSessionDiscovers(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, time.Second)
	info := mustOpen(t, e, d)

	if info.Status != model.SessionConnected || info.Target.BaseAddress != 40000 ||
		info.Target.RegType != model.RegTypeHolding || info.Target.URL != d.url() {
		t.Errorf("session = %+v", info)
	}
	wantIDs := []uint16{1, 103, 160, 160, 705, vendorModelID}
	if len(info.Models) != len(wantIDs) {
		t.Fatalf("models = %d, want %d", len(info.Models), len(wantIDs))
	}
	next := uint16(40002)
	for i, m := range info.Models {
		if m.Index != i || m.ID != wantIDs[i] || m.StartAddress != next {
			t.Errorf("model %d = %+v, want ID %d at %d", i, m, wantIDs[i], next)
		}
		next = m.StartAddress + 2 + m.Length
	}
	if !info.Models[idxCommon].StaticOnly || info.Models[idxInverter].StaticOnly || info.Models[idxVendor].StaticOnly {
		t.Errorf("static-only flags wrong: %+v", info.Models)
	}
	if v := info.Models[idxVendor]; v.SchemaKnown || v.DecodingSupported || v.Name != "unknown_64900" {
		t.Errorf("vendor model = %+v", v)
	}
	if !slices.ContainsFunc(info.Warnings, func(w string) bool { return strings.Contains(w, "64900") }) {
		t.Errorf("no warning for the vendor model: %v", info.Warnings)
	}
	if info.Device == nil || info.Device.Manufacturer != "OT Fabric" || info.Device.Serial != "SN-0001" {
		t.Errorf("device = %+v", info.Device)
	}
	// The default probe order starts at 0, then finds the marker at 40000.
	if len(info.Attempts) != 2 || info.Attempts[0].Matched || !info.Attempts[1].Matched || info.Attempts[1].BaseAddress != 40000 {
		t.Errorf("attempts = %+v", info.Attempts)
	}

	list, _ := e.Sessions(ctx)
	if len(list) != 1 || list[0].ID != info.ID {
		t.Errorf("Sessions = %+v", list)
	}
	if err := e.CloseSession(ctx, info.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if err := e.CloseSession(ctx, info.ID); service.CodeOf(err) != service.CodeNotFound {
		t.Errorf("second close = %v, want not_found", err)
	}
}

// A1, A2, A4, A8: a snapshot holds fully decoded points and is valid JSON.
func TestSnapshotDecodes(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, time.Second)
	info := mustOpen(t, e, d)

	snap, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Schema != model.SnapshotSchema || snap.Metadata != model.MetadataFull || snap.App.Name != model.AppName ||
		snap.Libs.RegistryModels == 0 || snap.Target.BaseAddress != 40000 || snap.Device == nil || snap.CapturedAt.IsZero() {
		t.Errorf("snapshot header = %+v", snap)
	}
	if len(snap.Models) != 6 {
		t.Fatalf("models = %d", len(snap.Models))
	}
	for _, m := range snap.Models {
		if m.Status != model.ModelOK || m.Error != nil {
			t.Errorf("model %d status %s error %v", m.Index, m.Status, m.Error)
		}
	}

	inv := findModel(t, snap.Models, idxInverter)
	a := findPoint(t, inv.Group, "A")
	if a.Label == "" || a.Description == "" || a.Units != "A" || a.Access != "R" || a.Mandatory == nil || !*a.Mandatory ||
		a.Static == nil || *a.Static || !a.Implemented || a.Scaled == nil || *a.Scaled < 12.29 || *a.Scaled > 12.31 ||
		a.SFName != "A_SF" || a.SFRaw == nil || *a.SFRaw != -1 || a.Type != "uint16" {
		t.Errorf("point A = %+v", a)
	}
	if raw, ok := a.Raw.(uint16); !ok || raw != 123 {
		t.Errorf("A raw = %#v", a.Raw)
	}
	if a.Offset != 2 || a.Address != int(inv.StartAddress)+2 {
		t.Errorf("A address %d offset %d, model at %d", a.Address, a.Offset, inv.StartAddress)
	}
	if w := findPoint(t, inv.Group, "W"); w.Scaled == nil || *w.Scaled != 4200 {
		t.Errorf("W = %+v", w)
	}
	if st := findPoint(t, inv.Group, "St"); !slices.Equal(st.Symbols, []string{"MPPT"}) {
		t.Errorf("St symbols = %v", st.Symbols)
	}
	if evt := findPoint(t, inv.Group, "Evt1"); !slices.Equal(evt.Symbols, []string{"GROUND_FAULT", "OVER_TEMP"}) {
		t.Errorf("Evt1 symbols = %v", evt.Symbols)
	}
	if pad := findPoint(t, findModel(t, snap.Models, idxCommon).Group, "Pad"); pad.Implemented {
		t.Errorf("padding shown as a value: %+v", pad)
	}
	// A point the device does not implement is labeled, with no scaled value.
	if va := findPoint(t, inv.Group, "VA"); va.Implemented || va.Scaled != nil {
		t.Errorf("VA = %+v, want not implemented", va)
	}

	// Two instances of model 160, each with two repeating modules.
	for i, first := range map[int]string{idxMPPT1: "A", idxMPPT2: "second"} {
		m := findModel(t, snap.Models, i)
		if m.ID != 160 || len(m.Group.Groups) != 2 {
			t.Fatalf("model %d = ID %d with %d groups", i, m.ID, len(m.Group.Groups))
		}
		g1, g2 := m.Group.Groups[0], m.Group.Groups[1]
		if g1.Name != "module" || g1.Index != 1 || g2.Index != 2 {
			t.Errorf("module groups = %s[%d], %s[%d]", g1.Name, g1.Index, g2.Name, g2.Index)
		}
		if s := findPoint(t, &g1, "IDStr"); s.Raw != first {
			t.Errorf("model %d module 1 IDStr = %v, want %s", i, s.Raw, first)
		}
		// The scale factor of a module point lives in the enclosing group.
		if dca := findPoint(t, &g2, "DCA"); dca.Scaled == nil || *dca.Scaled != 4.5 || dca.Address != int(m.StartAddress)+dca.Offset {
			t.Errorf("module 2 DCA = %+v", dca)
		}
	}

	// Model 705: curves, each holding its points.
	vv := findModel(t, snap.Models, idxVoltVar)
	if len(vv.Group.Groups) != 2 {
		t.Fatalf("705 curves = %d", len(vv.Group.Groups))
	}
	crv := vv.Group.Groups[1]
	if crv.Name != "Crv" || crv.Index != 2 || len(crv.Groups) != 3 || crv.Groups[2].Name != "Pt" || crv.Groups[2].Index != 3 {
		t.Errorf("705 curve 2 = %s[%d] with %d points", crv.Name, crv.Index, len(crv.Groups))
	}
	if v := findPoint(t, &crv.Groups[2], "V"); v.Scaled == nil || *v.Scaled != 1.0 {
		t.Errorf("705 Crv[2] Pt[3] V = %+v", v)
	}

	// The vendor model is read as raw registers, labeled as such.
	vendor := findModel(t, snap.Models, idxVendor)
	if vendor.SchemaKnown || vendor.Group != nil || !slices.Equal(vendor.RawRegisters, []uint16{1, 0x0203, 0xFFFF, 0x8000}) ||
		len(vendor.Warnings) == 0 {
		t.Errorf("vendor model = %+v", vendor)
	}
	if inv.RawRegisters != nil {
		t.Errorf("decoded model carries raw registers without being asked")
	}

	// The export is the snapshot as JSON; it reads back into the same types
	// with no field unaccounted for.
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var back model.Snapshot
	if err := dec.Decode(&back); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(back.Models) != 6 || back.Models[idxVendor].RawRegisters == nil || back.Libs != snap.Libs {
		t.Errorf("round trip lost data")
	}
	for _, key := range []string{`"schema":"otfabric.sunspec-explorer.snapshot/1"`, `"base_address":40000`, `"warnings":[`, `"raw_registers":[1,515,65535,32768]`} {
		if !bytes.Contains(raw, []byte(key)) {
			t.Errorf("export lacks %s", key)
		}
	}
}

func TestSnapshotSelectionAndLevels(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, time.Second)
	info := mustOpen(t, e, d)

	snap, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{
		ModelIndexes: []int{idxMPPT2, idxInverter, idxMPPT2}, Metadata: model.MetadataCompact, IncludeRaw: true,
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Models) != 2 || snap.Models[0].Index != idxMPPT2 || snap.Models[1].Index != idxInverter {
		t.Fatalf("selection = %+v", snap.Models)
	}
	inv := snap.Models[1]
	if a := findPoint(t, inv.Group, "A"); a.Label != "" || a.Description != "" || a.Access != "" || a.Mandatory != nil || a.Units != "A" {
		t.Errorf("compact point carries metadata: %+v", a)
	}
	if len(inv.RawRegisters) != int(inv.Length) {
		t.Errorf("raw registers = %d, want %d", len(inv.RawRegisters), inv.Length)
	}

	for _, req := range []model.SnapshotRequest{{ModelIndexes: []int{6}}, {ModelIndexes: []int{-1}}, {Metadata: "verbose"}} {
		if _, err := e.Snapshot(ctx, info.ID, req); service.CodeOf(err) != service.CodeInvalidArgument {
			t.Errorf("Snapshot(%+v) = %v, want invalid_argument", req, err)
		}
	}
	if _, err := e.Snapshot(ctx, "nope", model.SnapshotRequest{}); service.CodeOf(err) != service.CodeNotFound {
		t.Errorf("unknown session = %v", err)
	}
}

// R2: a failed connect shows every probe attempt.
func TestOpenSessionFailures(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, time.Second)

	t.Run("wrong unit", func(t *testing.T) {
		cfg := d.cfg()
		cfg.UnitID = 9
		_, err := e.OpenSession(ctx, cfg)
		var se *service.Error
		if !errors.As(err, &se) || se.Code != service.CodeConnectFailed {
			t.Fatalf("err = %v", err)
		}
		if len(se.Attempts) != 8 {
			t.Fatalf("attempts = %d, want one per default base address", len(se.Attempts))
		}
		for _, a := range se.Attempts {
			if a.Matched || a.Error == "" {
				t.Errorf("attempt = %+v", a)
			}
		}
	})

	t.Run("not sunspec", func(t *testing.T) {
		cfg := d.cfg()
		cfg.BaseAddresses = []uint16{40002, 40004}
		_, err := e.OpenSession(ctx, cfg)
		var se *service.Error
		if !errors.As(err, &se) || se.Code != service.CodeNotSunSpec || !errors.Is(err, sunspec.ErrNotSunSpec) {
			t.Fatalf("err = %v", err)
		}
		if len(se.Attempts) != 2 || se.Attempts[0].BaseAddress != 40002 || se.Attempts[0].Error != "" {
			t.Errorf("attempts = %+v", se.Attempts)
		}
	})

	t.Run("nothing listening", func(t *testing.T) {
		d2 := newSim(t)
		d2.stop()
		if _, err := e.OpenSession(ctx, d2.cfg()); service.CodeOf(err) != service.CodeConnectFailed {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("chain broken", func(t *testing.T) {
		d3 := newSim(t)
		d3.setFilter(failRange(40002, 40003, modbus.ErrServerDeviceFailure))
		_, err := e.OpenSession(ctx, d3.cfg())
		var se *service.Error
		if !errors.As(err, &se) || se.Code != service.CodeDiscoveryFailed || len(se.Attempts) == 0 {
			t.Fatalf("err = %v", err)
		}
	})

	if list, _ := e.Sessions(ctx); len(list) != 0 {
		t.Errorf("failed opens left sessions: %+v", list)
	}
}

func TestParseTarget(t *testing.T) {
	good := map[string]string{
		"tcp://10.0.0.5:1502":        "tcp://10.0.0.5:1502",
		" tcp://inverter.local ":     "tcp://inverter.local:502",
		"rtuovertcp://10.0.0.5:4001": "rtuovertcp://10.0.0.5:4001",
		"tcp://[::1]:502/":           "tcp://[::1]:502",
		"tcp+tls://10.0.0.5":         "tcp+tls://10.0.0.5:502",
		"rtu:///dev/ttyUSB0":         "rtu:///dev/ttyUSB0",
		"rtu://COM3":                 "rtu://COM3",
		"rtu:///dev/serial/by-id/usb-FTDI_USB-RS485-if00-port0": "rtu:///dev/serial/by-id/usb-FTDI_USB-RS485-if00-port0",
	}
	for in, want := range good {
		tg, err := parseTarget(model.TargetConfig{URL: in})
		if err != nil || tg.display != want || tg.unitID != 1 || tg.timeout != DefaultTimeout {
			t.Errorf("parseTarget(%q) = %+v, %v; want %s", in, tg, err, want)
		}
	}
	// A serial line: defaults, and what was asked for.
	tg, err := parseTarget(model.TargetConfig{URL: "rtu:///dev/ttyUSB0"})
	if err != nil || tg.device != "/dev/ttyUSB0" || tg.url != nil || tg.baud != 9600 || tg.dataBits != 8 ||
		tg.parity != modbus.ParityNone || tg.stopBits != 1 {
		t.Errorf("serial defaults = %+v, %v", tg, err)
	}
	tg, err = parseTarget(model.TargetConfig{URL: "rtu://COM3", Serial: &model.SerialConfig{Baud: 19200, DataBits: 7, Parity: "even", StopBits: 2}})
	if err != nil || tg.device != "COM3" || tg.baud != 19200 || tg.dataBits != 7 || tg.parity != modbus.ParityEven || tg.stopBits != 2 {
		t.Errorf("serial settings = %+v, %v", tg, err)
	}
	if tg, _ := parseTarget(model.TargetConfig{URL: "rtu://COM3", Serial: &model.SerialConfig{Parity: "odd"}}); tg.parity != modbus.ParityOdd {
		t.Errorf("odd parity = %+v", tg)
	}
	bad := []model.TargetConfig{
		{URL: ""},
		{URL: "http://10.0.0.5"},
		{URL: "rtu://"},
		{URL: "rtu:///dev/tty USB0"},
		{URL: "rtu:///dev/ttyUSB0?x=1"},
		{URL: "rtu:///dev/ttyUSB0", Serial: &model.SerialConfig{Baud: 12}},
		{URL: "rtu:///dev/ttyUSB0", Serial: &model.SerialConfig{DataBits: 5}},
		{URL: "rtu:///dev/ttyUSB0", Serial: &model.SerialConfig{Parity: "mark"}},
		{URL: "rtu:///dev/ttyUSB0", Serial: &model.SerialConfig{StopBits: 3}},
		{URL: "tcp://10.0.0.5", Serial: &model.SerialConfig{Baud: 9600}},
		{URL: "10.0.0.5:502"},
		{URL: "udp://10.0.0.5:502"},
		{URL: "tcp://user:secret@10.0.0.5:502"},
		{URL: "tcp://10.0.0.5:502/path"},
		{URL: "tcp://10.0.0.5:502?x=1"},
		{URL: "tcp://:502"},
		{URL: "tcp://10.0.0.5:99999"},
		{URL: "tcp://10.0.0.5:x"},
		{URL: "://"},
		{URL: "tcp://10.0.0.5", UnitID: 250},
		{URL: "tcp://10.0.0.5", RegType: "coil"},
		{URL: "tcp://10.0.0.5", TimeoutMS: 5},
		{URL: "tcp://10.0.0.5", MaxModels: -1},
		{URL: "tcp://10.0.0.5", BaseAddresses: make([]uint16, 17)},
	}
	for _, cfg := range bad {
		if _, err := parseTarget(cfg); service.CodeOf(err) != service.CodeInvalidArgument {
			t.Errorf("parseTarget(%+v) = %v, want invalid_argument", cfg, err)
		}
	}
	// A credential in a rejected URL must not be echoed.
	_, err = parseTarget(model.TargetConfig{URL: "tcp://user:secret@10.0.0.5:502"})
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error leaks the credential: %v", err)
	}
	tg, err = parseTarget(model.TargetConfig{URL: "tcp://h", UnitID: 255, RegType: model.RegTypeInput, TimeoutMS: 250})
	if err != nil || tg.regType != modbus.InputRegister || tg.timeout != 250*time.Millisecond || tg.unitID != 255 {
		t.Errorf("parseTarget = %+v, %v", tg, err)
	}
}

func TestSessionLimitAndAllowList(t *testing.T) {
	d := newSim(t)
	e := New(Options{MaxSessions: 1, AllowTarget: func(u *url.URL) error {
		if u.Hostname() != "127.0.0.1" {
			return errors.New("outside the allow-list")
		}
		return nil
	}})
	t.Cleanup(e.Close)

	if _, err := e.OpenSession(ctx, model.TargetConfig{URL: "tcp://192.0.2.1:502"}); service.CodeOf(err) != service.CodeNotAllowed {
		t.Errorf("err = %v, want not_allowed", err)
	}
	mustOpen(t, e, d)
	if _, err := e.OpenSession(ctx, d.cfg()); service.CodeOf(err) != service.CodeLimitExceeded {
		t.Errorf("err = %v, want limit_exceeded", err)
	}
	e.Close()
	if _, err := e.OpenSession(ctx, d.cfg()); service.CodeOf(err) != service.CodeInternal {
		t.Errorf("open on a closed engine = %v", err)
	}
}

func TestHealthAndStubs(t *testing.T) {
	e := newEngine(t, time.Second)
	h, err := e.Health(ctx)
	if err != nil || h.Status != "ok" || h.APIVersion != model.APIVersion || h.RegistryModels < 100 || h.Time.IsZero() ||
		h.GoSunspec == "" {
		t.Errorf("Health = %+v, %v", h, err)
	}
	if ports, err := e.SerialPorts(ctx); err != nil || len(ports) != 0 {
		t.Errorf("SerialPorts = %v, %v", ports, err)
	}
	for _, call := range []func() error{
		func() error { _, err := e.Rediscover(ctx, "x"); return err },
		func() error { return e.StartPolling(ctx, "x", model.PollConfig{}) },
		func() error { return e.StopPolling(ctx, "x") },
		func() error { _, err := e.State(ctx, "x"); return err },
	} {
		if err := call(); service.CodeOf(err) != service.CodeNotFound {
			t.Errorf("unknown session = %v, want not_found", err)
		}
	}
	if e.Guard() == nil {
		t.Error("no guard")
	}
}

func TestRediscover(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, time.Second)
	ch := events(t, e)
	info := mustOpen(t, e, d)

	d.setModels(stdModels()[:2]...)
	again, err := e.Rediscover(ctx, info.ID)
	if err != nil {
		t.Fatalf("Rediscover: %v", err)
	}
	if len(again.Models) != 2 || !again.DiscoveredAt.After(info.DiscoveredAt) {
		t.Errorf("rediscovered = %+v", again)
	}
	ev := waitEvent(t, ch, "discovery_changed", func(ev model.Event) bool { return ev.Kind == model.EventDiscoveryChanged })
	if ev.Session == nil || len(ev.Session.Models) != 2 || ev.SessionID != info.ID {
		t.Errorf("event = %+v", ev)
	}

	// A failed re-discovery keeps the session and what it knew.
	d.setFilter(failRange(40000, 40001, modbus.ErrIllegalDataAddress))
	if _, err := e.Rediscover(ctx, info.ID); service.CodeOf(err) != service.CodeNotSunSpec {
		t.Errorf("Rediscover with the marker gone = %v", err)
	}
	if st, _ := e.State(ctx, info.ID); len(st.Session.Models) != 2 {
		t.Errorf("failed re-discovery changed the session: %+v", st.Session)
	}
}

// A context that ends while a read waits for the connection ends the call.
func TestSnapshotContext(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, time.Second)
	info := mustOpen(t, e, d)
	s, _ := e.session(info.ID)

	if err := s.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, err := e.Snapshot(short, info.ID, model.SnapshotRequest{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Snapshot = %v, want deadline exceeded", err)
	}
	if _, err := e.Rediscover(short, info.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Rediscover = %v, want deadline exceeded", err)
	}
	s.release()
}
