// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// The published schemas, next to snapshot-1.schema.json.
const (
	snapshotSchemaFile  = "snapshot-1.schema.json"
	recordingSchemaFile = "recording-1.schema.json"
	apiSchemaFile       = "api-1.schema.json"
)

func schemaPath(t testing.TB, file string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "docs", "schema", file))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// schemas compiles definitions of one published schema file on demand.
type schemas struct {
	t        testing.TB
	path     string
	compiler *jsonschema.Compiler
	compiled map[string]*jsonschema.Schema
}

func newSchemas(t testing.TB, file string) *schemas {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	return &schemas{t: t, path: schemaPath(t, file), compiler: c, compiled: make(map[string]*jsonschema.Schema)}
}

// def returns the schema of a named definition, or of the whole file for "".
func (s *schemas) def(name string) *jsonschema.Schema {
	s.t.Helper()
	if sch, ok := s.compiled[name]; ok {
		return sch
	}
	loc := s.path
	if name != "" {
		loc += "#/$defs/" + name
	}
	sch, err := s.compiler.Compile(loc)
	if err != nil {
		s.t.Fatalf("definition %q does not compile: %v", name, err)
	}
	s.compiled[name] = sch
	return sch
}

// raw validates a JSON document against a definition.
func (s *schemas) raw(name string, doc []byte) error {
	s.t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		s.t.Fatalf("not JSON: %v\n%.200s", err, doc)
	}
	return s.def(name).Validate(v)
}

// check marshals v as the engine's clients get it and validates the result.
// It returns the JSON, for the checks that edit it.
func (s *schemas) check(name, what string, v any) []byte {
	s.t.Helper()
	doc, err := json.Marshal(v)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := s.raw(name, doc); err != nil {
		s.t.Errorf("%s is not a valid %s: %v\n%.300s", what, name, err, doc)
	}
	return doc
}

// reject fails when the schema accepts a document that was broken on
// purpose: doc with old replaced by repl.
func (s *schemas) reject(name, what string, doc []byte, old, repl string) {
	s.t.Helper()
	if !bytes.Contains(doc, []byte(old)) {
		s.t.Errorf("%s: %q is not in the %s to break: %.300s", what, old, name, doc)
		return
	}
	if err := s.raw(name, bytes.Replace(doc, []byte(old), []byte(repl), 1)); err == nil {
		s.t.Errorf("%s: %s was accepted as a %s", what, name, name)
	}
}

func statusIs(status string) func(model.Event) bool {
	return func(ev model.Event) bool { return ev.Kind == model.EventSessionStatus && ev.Session.Status == status }
}

func kindIs(kind string) func(model.Event) bool {
	return func(ev model.Event) bool { return ev.Kind == kind }
}

// markerFails makes the probes for the SunSpec marker fail, at every base
// address: discovery then finds no SunSpec device.
func markerFails() readFilter {
	return func(addr, _ uint16, regs []uint16, err error) ([]uint16, error) {
		if addr == 40000 {
			return nil, modbus.ErrIllegalDataAddress
		}
		return regs, err
	}
}

// R7, A8: every line of a real recording validates against the published
// schema of the recording format: header, samples (good, with a failed model,
// with a changed model), every kind of event line, and the footer.
func TestRecordingMatchesPublishedSchema(t *testing.T) {
	sch := newSchemas(t, recordingSchemaFile)
	d := newSim(t)
	e := recEngine(t, t.TempDir(), RecordingLimits{})
	ch := events(t, e)
	info := mustOpen(t, e, d)

	// Recording starts before polling: the header has no interval, and a gap
	// line says why no sample follows.
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "run"}); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "2 recorded samples", recordingSamples(2))

	// A failing model.
	inv := info.Models[idxInverter]
	d.setFilter(failRange(inv.StartAddress, inv.StartAddress+1, modbus.ErrServerDeviceFailure))
	waitEvent(t, ch, "a recorded failure", func(ev model.Event) bool {
		return isSample(ev) && ev.Recording != nil && ev.Recording.Failures >= 1
	})
	d.setFilter(nil)

	// A changed chain.
	d.setModels(stdModels()[:3]...)
	waitEvent(t, ch, "discovery_changed", kindIs(model.EventDiscoveryChanged))
	waitEvent(t, ch, "a sample of the new chain", func(ev model.Event) bool {
		return isSample(ev) && len(ev.Sample.Models) == 2 && ev.Sample.Models[0].Status == model.ModelOK
	})

	// A device that goes away and comes back.
	d.stop()
	waitEvent(t, ch, "reconnecting", statusIs(model.SessionReconnecting))
	d.start()
	waitEvent(t, ch, "connected again", statusIs(model.SessionConnected))
	waitEvent(t, ch, "a sample after the outage", func(ev model.Event) bool {
		return isSample(ev) && ev.Sample.Models[0].Status == model.ModelOK
	})

	if err := e.StopPolling(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.StopRecording(ctx, "run"); err != nil {
		t.Fatalf("StopRecording: %v", err)
	}

	// A second recording, started while polling runs and ended by closing
	// the session.
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1, ModelIndexes: []int{idxInverter}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{}); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	second := waitEvent(t, ch, "a sample of the second recording", recordingSamples(1)).Recording.ID
	if err := e.CloseSession(ctx, info.ID); err != nil {
		t.Fatal(err)
	}

	seen := make(map[string][]byte) // one line of each type, and of each event kind
	for _, id := range []string{"run", second} {
		raw, lines := readRecording(t, e, id)
		rows := bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n"))
		if len(rows) != len(lines) {
			t.Fatalf("%s: %d rows, %d lines", id, len(rows), len(lines))
		}
		for i, row := range rows {
			if err := sch.raw("", row); err != nil {
				t.Errorf("%s line %d (%s): %v\n%.300s", id, i+1, typesOf(lines)[i], err, row)
			}
			key := typesOf(lines)[i]
			switch {
			case lines[i].Type != model.LineSample:
			case slices.ContainsFunc(lines[i].Models, func(m model.ModelResult) bool { return m.Status == model.ModelError }):
				key += ":error"
			case slices.ContainsFunc(lines[i].Models, func(m model.ModelResult) bool { return m.Status == model.ModelChanged }):
				key += ":changed"
			}
			if _, ok := seen[id+" "+key]; !ok {
				seen[id+" "+key] = row
			}
		}
	}
	for _, want := range []string{
		"run header", "run sample", "run sample:error", "run sample:changed", "run footer",
		"run event:gap", "run event:polling_started", "run event:discovery_changed",
		"run event:reconnecting", "run event:connected",
		second + " header", second + " sample", second + " footer",
	} {
		if _, ok := seen[want]; !ok {
			t.Errorf("no %q line was validated", want)
		}
	}
	if t.Failed() {
		return
	}
	// The two headers and footers differ where the format says they may.
	if !bytes.Contains(seen["run header"], []byte(`"interval_s":0`)) || !bytes.Contains(seen[second+" header"], []byte(`"interval_s":1`)) {
		t.Errorf("header intervals: %.200s / %.200s", seen["run header"], seen[second+" header"])
	}
	if !bytes.Contains(seen["run footer"], []byte(`"reason":"user"`)) || !bytes.Contains(seen[second+" footer"], []byte(`"reason":"session_closed"`)) {
		t.Errorf("footers: %s / %s", seen["run footer"], seen[second+" footer"])
	}

	// The schema is not vacuous: it refuses what is not a recording line.
	for _, c := range []struct{ what, line, old, repl string }{
		{"another schema id", "run header", "recording/1", "recording/2"},
		{"a header without models", "run header", `"models":`, `"model_list":`},
		{"a header with a text address", "run header", `"base_address":40000`, `"base_address":"40000"`},
		{"a header with a path as name", "run header", `"name":"run"`, `"name":"../run"`},
		{"an unknown line type", "run header", `"type":"header"`, `"type":"preamble"`},
		{"a sample with point metadata", "run sample", `"metadata":"compact"`, `"metadata":"full"`},
		{"a sample without number", "run sample", `"seq":`, `"sequence":`},
		{"a sample numbered 0", "run sample", `"seq":1,`, `"seq":0,`},
		{"an unknown model status", "run sample", `"status":"ok"`, `"status":"fine"`},
		{"a point field typo", "run sample", `"implemented":`, `"implementd":`},
		{"a sample typed as footer", "run sample", `"type":"sample"`, `"type":"footer"`},
		{"an event without time", "run event:gap", `"at":`, `"when":`},
		{"an event with an unknown field", "run event:gap", `"kind":`, `"password":"x","kind":`},
		{"an event with a date only", "run event:gap", `"at":"20`, `"at":"x20`},
		{"an unknown stop reason", "run footer", `"reason":"user"`, `"reason":"bored"`},
		{"a negative counter", "run footer", `"samples":`, `"samples":-`},
	} {
		sch.reject("", c.what, seen[c.line], c.old, c.repl)
	}
	for _, doc := range []string{`[]`, `"header"`, `{}`, `{"type":"sample"}`} {
		if err := sch.raw("", []byte(doc)); err == nil {
			t.Errorf("%s was accepted as a recording line", doc)
		}
	}
}

// A8: what the engine hands to its clients validates against the published
// schema of the API entities: every answer of the Service interface, every
// kind of event, and the requests the clients send.
func TestEntitiesMatchPublishedSchema(t *testing.T) {
	sch := newSchemas(t, apiSchemaFile)
	dir := t.TempDir()
	d := newSim(t)
	e := recEngine(t, dir, RecordingLimits{})
	ch := events(t, e)

	// Every event that arrives is validated, whether a step waits for it or
	// not; one of each kind is kept.
	kinds := make(map[string][]byte)
	wait := func(what string, match func(model.Event) bool) model.Event {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					t.Fatalf("event stream closed while waiting for %s", what)
				}
				doc := sch.check("Event", "a "+ev.Kind+" event", ev)
				if _, seen := kinds[ev.Kind]; !seen || ev.Kind == model.EventSample && ev.Recording != nil {
					kinds[ev.Kind] = doc
				}
				if match(ev) {
					return ev
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}

	// Health, with and without what an engine can be set up for.
	health, _ := e.Health(ctx)
	healthDoc := sch.check("Health", "Health of a recording engine", health)
	bare, _ := New(Options{NoSerial: true}).Health(ctx)
	if doc := sch.check("Health", "Health of a bare engine", bare); !bytes.Contains(doc, []byte(`"features":[]`)) {
		t.Errorf("bare health = %s", doc)
	}

	// Requests, as the Go types marshal them.
	target := sch.check("TargetConfig", "the target of the tests", d.cfg())
	sch.check("TargetConfig", "a target with every field", model.TargetConfig{
		URL: "rtu:///dev/ttyUSB0", UnitID: 255, TimeoutMS: 60000, RegType: model.RegTypeInput,
		BaseAddresses: []uint16{0, 40000, 65535}, MaxModels: 1024,
		Serial: &model.SerialConfig{Baud: 19200, DataBits: 7, Parity: model.ParityEven, StopBits: 2},
	})
	sch.check("TargetConfig", "a target with defaults only", model.TargetConfig{URL: "tcp://192.0.2.1:502"})
	sch.check("SerialConfig", "empty serial settings", model.SerialConfig{})
	sch.check("PollConfig", "an empty poll configuration", model.PollConfig{})
	sch.check("PollConfig", "a poll configuration", model.PollConfig{IntervalS: 3600, ModelIndexes: []int{0, 4}})
	sch.check("SnapshotRequest", "an empty snapshot request", model.SnapshotRequest{})
	sch.check("SnapshotRequest", "a snapshot request", model.SnapshotRequest{ModelIndexes: []int{1}, Metadata: model.MetadataCompact, IncludeRaw: true})
	sch.check("RecordingConfig", "an empty recording configuration", model.RecordingConfig{})
	sch.check("RecordingConfig", "a recording configuration", model.RecordingConfig{Name: "run-1", RotateBytes: 6000})
	export := sch.check("ExportRequest", "an export request", map[string]string{"format": model.ExportDeviceFile})

	// Errors, with and without probe attempts.
	wrongUnit := d.cfg()
	wrongUnit.UnitID = 9
	_, err := e.OpenSession(ctx, wrongUnit)
	var se *service.Error
	if !errors.As(err, &se) || len(se.Attempts) == 0 {
		t.Fatalf("open with a wrong unit = %v", err)
	}
	errDoc := sch.check("Error", "a failed open", se)
	sch.check("ErrorResponse", "a failed open", map[string]any{"error": se})
	noMarker := d.cfg()
	noMarker.BaseAddresses = []uint16{40002}
	if _, err = e.OpenSession(ctx, noMarker); !errors.As(err, &se) || se.Code != service.CodeNotSunSpec {
		t.Fatalf("open without marker = %v", err)
	}
	sch.check("Error", "not_sunspec", se)
	if _, err = e.State(ctx, "nope"); !errors.As(err, &se) {
		t.Fatalf("State of an unknown session = %v", err)
	}
	if doc := sch.check("Error", "not_found", se); bytes.Contains(doc, []byte("attempts")) {
		t.Errorf("not_found = %s", doc)
	}

	// A session, idle.
	info := mustOpen(t, e, d)
	infoDoc := sch.check("SessionInfo", "an opened session", info)
	wait("session_status", statusIs(model.SessionConnected))
	list, _ := e.Sessions(ctx)
	sch.check("SessionList", "Sessions", list)
	st, _ := e.State(ctx, info.ID)
	idle := sch.check("SessionState", "the state of an idle session", st)
	if !bytes.Contains(idle, []byte(`"last_sample":null,"recording":null`)) || !bytes.Contains(idle, []byte(`"model_indexes":[]`)) {
		t.Errorf("idle state = %.300s", idle)
	}
	sch.check("PollState", "polling never started", st.Polling)
	for _, m := range info.Models {
		sch.check("ModelInfo", "a discovered model", m)
	}
	for _, a := range info.Attempts {
		sch.check("ProbeAttempt", "a probe", a)
	}
	sch.check("Target", "the target of a session", info.Target)
	sch.check("DeviceIdentity", "the identity of a device", info.Device)

	// Single reads.
	for name, req := range map[string]model.SnapshotRequest{
		"a full snapshot":    {},
		"with raw registers": {IncludeRaw: true},
		"a compact snapshot": {Metadata: model.MetadataCompact, ModelIndexes: []int{idxVoltVar, idxVendor}},
	} {
		snap, err := e.Snapshot(ctx, info.ID, req)
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		sch.check("Snapshot", name, snap)
		for _, m := range snap.Models {
			sch.check("ModelResult", name+": model "+m.Name, m)
		}
	}
	snap, _ := e.Snapshot(ctx, info.ID, model.SnapshotRequest{ModelIndexes: []int{idxMPPT1}})
	group := findModel(t, snap.Models, idxMPPT1).Group
	sch.check("GroupView", "a group with nested groups", group)
	sch.check("PointView", "a scale factor point", findPoint(t, group, "DCA_SF"))
	point := sch.check("PointView", "a scaled point", findPoint(t, &group.Groups[0], "DCA"))
	for _, format := range []string{model.ExportCSV, model.ExportDeviceFile} {
		file, err := e.ExportSession(ctx, info.ID, format)
		if err != nil {
			t.Fatalf("ExportSession %s: %v", format, err)
		}
		sch.check("ExportFile", "the "+format+" export", file)
	}

	// Serial ports: none, and some.
	ports, _ := e.SerialPorts(ctx)
	sch.check("SerialPortList", "no serial ports", ports)
	withPorts := New(Options{SerialPorts: func() []string { return []string{"/dev/ttyUSB0", "/dev/ttyS1"} }})
	ports, _ = withPorts.SerialPorts(ctx)
	if doc := sch.check("SerialPortList", "serial ports", ports); !bytes.Contains(doc, []byte("ttyS1")) {
		t.Errorf("serial ports = %s", doc)
	}
	sch.check("SerialPort", "a serial port", ports[0])

	// Recording and polling.
	rec, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "run"})
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	running := sch.check("RecordingInfo", "a running recording", rec)
	wait("recording_status", kindIs(model.EventRecordingStatus))
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	wait("2 recorded samples", recordingSamples(2))
	st, _ = e.State(ctx, info.ID)
	if st.LastSample == nil || st.Recording == nil || !st.Polling.Active {
		t.Fatalf("state while polling = %+v", st)
	}
	busy := sch.check("SessionState", "the state of a polling, recording session", st)
	sampleDoc := sch.check("Sample", "the last sample", st.LastSample)
	sch.check("PollState", "polling", st.Polling)

	// A failing model, in a sample.
	inv := info.Models[idxInverter]
	d.setFilter(failRange(inv.StartAddress, inv.StartAddress+1, modbus.ErrServerDeviceFailure))
	failed := wait("a sample with a failed model", func(ev model.Event) bool {
		return isSample(ev) && slices.ContainsFunc(ev.Sample.Models, func(m model.ModelResult) bool { return m.Status == model.ModelError })
	})
	sch.check("Sample", "a sample with a failed model", failed.Sample)

	// A chain that changes while discovery fails (an error event), then
	// while it works (discovery_changed).
	d.setFilter(markerFails())
	d.setModels(stdModels()[:3]...)
	changed := wait("a sample with a changed model", func(ev model.Event) bool {
		return isSample(ev) && slices.ContainsFunc(ev.Sample.Models, func(m model.ModelResult) bool { return m.Status == model.ModelChanged })
	})
	sch.check("Sample", "a sample with a changed model", changed.Sample)
	wait("an error event", kindIs(model.EventError))
	d.setFilter(nil)
	rediscovered := wait("discovery_changed", kindIs(model.EventDiscoveryChanged))
	sch.check("SessionInfo", "a session discovered again", rediscovered.Session)

	// A device that goes away and comes back.
	d.stop()
	lost := wait("reconnecting", statusIs(model.SessionReconnecting))
	if doc := sch.check("SessionInfo", "a reconnecting session", lost.Session); !bytes.Contains(doc, []byte(`"status_detail"`)) {
		t.Errorf("reconnecting session = %.300s", doc)
	}
	d.start()
	wait("connected again", statusIs(model.SessionConnected))

	// A subscriber that asks for events of another run gets a resync.
	sub, cancel := context.WithCancel(ctx)
	resync := <-e.hub.subscribe(sub, 1<<40)
	cancel()
	if resync.Kind != model.EventResync {
		t.Fatalf("first event of a subscriber from another run = %+v", resync)
	}
	kinds[resync.Kind] = sch.check("Event", "a resync event", resync)

	// Stopped polling keeps the last sample; a stopped recording is listed.
	if err := e.StopPolling(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	st, _ = e.State(ctx, info.ID)
	if st.LastSample == nil || st.Polling.Active {
		t.Fatalf("state after polling = %+v", st)
	}
	sch.check("SessionState", "the state after polling", st)
	stopped, err := e.StopRecording(ctx, "run")
	if err != nil || stopped.State != model.RecordingStopped {
		t.Fatalf("StopRecording = %+v, %v", stopped, err)
	}
	sch.check("RecordingInfo", "a stopped recording", stopped)
	wait("recording stopped", func(ev model.Event) bool {
		return ev.Kind == model.EventRecordingStatus && ev.Recording.State == model.RecordingStopped
	})

	// A recording cut off by a kill is listed as interrupted after a restart.
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "cut"}); err != nil {
		t.Fatal(err)
	}
	cut, _ := e.recs.get("cut")
	cut.mu.Lock()
	_, _ = cut.file.WriteString(`{"type":"sample","seq":99,"models":[{"index":1,"sta`)
	_ = cut.file.Close()
	cut.file = nil
	cut.mu.Unlock()
	restarted := recEngine(t, dir, RecordingLimits{})
	recs, _ := restarted.Recordings(ctx)
	sch.check("RecordingList", "Recordings after a restart", recs)
	states := make(map[string]bool)
	for _, r := range recs {
		states[r.State] = true
		sch.check("RecordingInfo", "a "+r.State+" recording", r)
	}
	if !states[model.RecordingInterrupted] || !states[model.RecordingStopped] {
		t.Errorf("Recordings after a restart = %+v", recs)
	}
	none, _ := newEngine(t, tick).Recordings(ctx)
	sch.check("RecordingList", "no recordings", none)

	// The end of the session.
	if err := e.CloseSession(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	closed := wait("the session closed", statusIs(model.SessionClosed))
	sch.check("SessionInfo", "a closed session", closed.Session)

	for _, kind := range []string{
		model.EventSample, model.EventSessionStatus, model.EventDiscoveryChanged,
		model.EventRecordingStatus, model.EventError, model.EventResync,
	} {
		if _, ok := kinds[kind]; !ok {
			t.Errorf("no %s event was validated", kind)
		}
	}
	if t.Failed() {
		return
	}

	// The schema is not vacuous: every definition refuses a broken document.
	for _, c := range []struct {
		def, what string
		doc       []byte
		old, repl string
	}{
		{"Health", "a missing field", healthDoc, `"api_version"`, `"api"`},
		{"Health", "a time that is none", healthDoc, `"time":"`, `"time":"at `},
		{"Health", "another major version", healthDoc, `"api_version":"1.`, `"api_version":"2.`},
		{"TargetConfig", "a unit ID out of range", target, `"unit_id":1`, `"unit_id":300`},
		{"TargetConfig", "a reserved unit ID", target, `"unit_id":1`, `"unit_id":250`},
		{"TargetConfig", "a timeout below the minimum", target, `"timeout_ms":500`, `"timeout_ms":5`},
		{"TargetConfig", "no URL", target, `"url"`, `"uri"`},
		{"ExportRequest", "an unknown format", export, `"devicefile"`, `"xml"`},
		{"Error", "an unknown code", errDoc, `"code":"connect_failed"`, `"code":"oops"`},
		{"Error", "no message", errDoc, `"message"`, `"msg"`},
		{"Error", "an attempt without address", errDoc, `"base_address"`, `"base"`},
		{"ErrorResponse", "an error that is not wrapped", errDoc, `"code"`, `"code"`},
		{"SessionInfo", "an unknown status", infoDoc, `"status":"connected"`, `"status":"open"`},
		{"SessionInfo", "attempts that are null", infoDoc, `"attempts":[`, `"attempts":null,"x":[`},
		{"SessionInfo", "a model without index", infoDoc, `"index":0,`, ``},
		{"SessionInfo", "an unknown register type", infoDoc, `"reg_type":"holding"`, `"reg_type":"coil"`},
		{"SessionState", "no last_sample", idle, `"last_sample":null,`, ``},
		{"SessionState", "an interval beyond the maximum", busy, `"interval_s":1,`, `"interval_s":3601,`},
		{"SessionState", "a recording in an unknown state", busy, `"state":"running"`, `"state":"paused"`},
		{"Sample", "an unknown metadata level", sampleDoc, `"metadata":"compact"`, `"metadata":"terse"`},
		{"Sample", "a model without address", sampleDoc, `"start_address":`, `"start":`},
		{"Sample", "a negative duration", sampleDoc, `"read_duration_ms":`, `"read_duration_ms":-1`},
		{"PointView", "a scale factor out of range", point, `"sf_raw":-1`, `"sf_raw":40000`},
		{"PointView", "no raw value", point, `"raw":`, `"value":`},
		{"RecordingInfo", "an unknown state", running, `"state":"running"`, `"state":"paused"`},
		{"RecordingInfo", "no location", running, `"location"`, `"dir"`},
		{"RecordingInfo", "an unknown stop reason", running, `"samples":`, `"stop_reason":"bored","samples":`},
		{"Event", "an unknown kind", kinds[model.EventError], `"kind":"error"`, `"kind":"warning"`},
		{"Event", "an error without message", kinds[model.EventError], `"message"`, `"session_id"`},
		{"Event", "a sample event without sample", kinds[model.EventSample], `"sample":{`, `"session":{`},
		{"Event", "a session event without session", kinds[model.EventSessionStatus], `"session":{`, `"sample":{`},
		{"Event", "a recording event without recording", kinds[model.EventRecordingStatus], `"recording":{`, `"session":{`},
		{"Event", "an unknown field", kinds[model.EventResync], `"kind"`, `"token":"x","kind"`},
		{"Event", "no sequence number", kinds[model.EventResync], `"seq"`, `"number"`},
	} {
		sch.reject(c.def, c.what, c.doc, c.old, c.repl)
	}
}

// The definitions that the published schemas have in common are copies of
// each other: a change to one file that is not made to the others is caught
// here.
func TestSchemasShareDefinitions(t *testing.T) {
	type doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
		root map[string]any
	}
	load := func(file string) doc {
		t.Helper()
		b, err := os.ReadFile(schemaPath(t, file))
		if err != nil {
			t.Fatal(err)
		}
		var d doc
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if err := json.Unmarshal(b, &d.root); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		return d
	}
	files := []string{snapshotSchemaFile, recordingSchemaFile, apiSchemaFile}
	docs := make(map[string]doc)
	for _, f := range files {
		docs[f] = load(f)
		if id, _ := docs[f].root["$id"].(string); !strings.HasSuffix(id, "/"+f) {
			t.Errorf("%s: $id is %q", f, id)
		}
	}
	compact := func(raw json.RawMessage) string {
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	// What all three files describe.
	for _, name := range []string{"Address", "PointView", "GroupView", "ModelResult", "Target", "DeviceIdentity", "AppInfo", "LibInfo"} {
		for _, f := range files {
			if _, ok := docs[f].Defs[name]; !ok {
				t.Errorf("%s has no definition %s", f, name)
			}
		}
	}
	// A name means the same in every file that has it, to the letter.
	shared := 0
	for i, a := range files {
		for _, b := range files[i+1:] {
			for name, def := range docs[a].Defs {
				other, ok := docs[b].Defs[name]
				if !ok {
					continue
				}
				shared++
				if compact(def) != compact(other) {
					t.Errorf("definition %s differs between %s and %s", name, a, b)
				}
			}
		}
	}
	if shared < 3*8 {
		t.Errorf("only %d shared definitions were compared", shared)
	}

	object := func(raw json.RawMessage) map[string]any {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	// The Snapshot of the API is the snapshot export.
	snapshot, apiSnapshot := docs[snapshotSchemaFile].root, object(docs[apiSchemaFile].Defs["Snapshot"])
	for _, key := range []string{"type", "additionalProperties", "required", "properties"} {
		if snapshot[key] == nil || !reflect.DeepEqual(snapshot[key], apiSnapshot[key]) {
			t.Errorf("%q of the Snapshot differs between %s and %s", key, snapshotSchemaFile, apiSchemaFile)
		}
	}
	// A sample line of a recording is the Sample of the API with a type, at
	// one metadata level.
	line, sample := object(docs[recordingSchemaFile].Defs["RecordingSample"]), object(docs[apiSchemaFile].Defs["Sample"])
	lineProps, sampleProps := line["properties"].(map[string]any), sample["properties"].(map[string]any)
	if len(lineProps) != len(sampleProps)+1 {
		t.Errorf("a sample line has %d properties, a Sample %d", len(lineProps), len(sampleProps))
	}
	for name, prop := range sampleProps {
		if name != "metadata" && !reflect.DeepEqual(prop, lineProps[name]) {
			t.Errorf("property %q differs between RecordingSample and Sample", name)
		}
	}
	if want := append([]any{"type"}, sample["required"].([]any)...); !reflect.DeepEqual(line["required"], want) {
		t.Errorf("required of RecordingSample = %v, want %v", line["required"], want)
	}
}
