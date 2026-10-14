// SPDX-License-Identifier: MIT

package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/otfabric/sunspec-explorer/internal/api"
	"github.com/otfabric/sunspec-explorer/internal/engine"
	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// schemas compiles definitions of one published schema file (docs/schema)
// on demand.
type schemas struct {
	t        testing.TB
	path     string
	compiler *jsonschema.Compiler
	compiled map[string]*jsonschema.Schema
}

func newSchemas(t testing.TB, file string) *schemas {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "docs", "schema", file))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	return &schemas{t: t, path: path, compiler: c, compiled: make(map[string]*jsonschema.Schema)}
}

// validate checks a JSON document against a named definition, or against
// the whole file for "".
func (s *schemas) validate(name string, doc string) error {
	s.t.Helper()
	sch, ok := s.compiled[name]
	if !ok {
		loc := s.path
		if name != "" {
			loc += "#/$defs/" + name
		}
		var err error
		if sch, err = s.compiler.Compile(loc); err != nil {
			s.t.Fatalf("definition %q does not compile: %v", name, err)
		}
		s.compiled[name] = sch
	}
	v, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err != nil {
		s.t.Fatalf("not JSON: %v\n%.200s", err, doc)
	}
	return sch.Validate(v)
}

// sseFrame is one server-sent event as it came over the wire.
type sseFrame struct{ id, event, data string }

// stream opens the event stream and returns its frames, undecoded.
func (d *daemon) stream(t testing.TB, since string) <-chan sseFrame {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", d.srv.URL+"/api/v1/events?since="+since, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := d.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		t.Fatalf("events = %d", res.StatusCode)
	}
	out := make(chan sseFrame, 256)
	go func() {
		defer close(out)
		defer func() { _ = res.Body.Close() }()
		readSSE(res.Body, func(id, event, data string) {
			select {
			case out <- sseFrame{id, event, data}:
			case <-ctx.Done():
			}
		})
	}()
	return out
}

// A8, A10: what the daemon sends over HTTP validates against the published
// schema of the API entities: the answer of every endpoint, the error
// answers, and the events of the stream. The requests the test sends are
// valid against the definitions of the request bodies.
func TestAPIMatchesPublishedSchema(t *testing.T) {
	sch := newSchemas(t, "api-1.schema.json")
	blocked := errors.New("not on the allow-list")
	d := newDaemon(t, engine.Options{
		DataDir:     t.TempDir(),
		MaxSessions: 1,
		SerialPorts: func() []string { return []string{"/dev/ttyUSB0"} },
		AllowTarget: func(u *url.URL) error {
			if u.Hostname() == "192.0.2.1" {
				return blocked
			}
			return nil
		},
	}, api.Options{})
	dev := newSim(t)

	// call sends a request and validates the answer: against def for the
	// expected status, as an ErrorResponse for an error status. A request
	// body is first validated against reqDef.
	call := func(method, path, reqDef string, in any, status int, def string) string {
		t.Helper()
		body := ""
		switch v := in.(type) {
		case nil:
		case string:
			body = v
		default:
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			body = string(b)
		}
		if reqDef != "" {
			if err := sch.validate(reqDef, body); err != nil {
				t.Errorf("%s %s: the request is not a valid %s: %v\n%s", method, path, reqDef, err, body)
			}
		}
		got, header, answer := d.request(t, method, path, testToken, body, nil)
		if got != status {
			t.Fatalf("%s %s = %d %.300s, want %d", method, path, got, answer, status)
		}
		switch {
		case status == http.StatusNoContent:
			if answer != "" {
				t.Errorf("%s %s: 204 with a body: %.200s", method, path, answer)
			}
			return ""
		case status >= 400:
			def = "ErrorResponse"
		}
		if ct := header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s %s: Content-Type %q", method, path, ct)
		}
		if err := sch.validate(def, answer); err != nil {
			t.Errorf("%s %s = %d: not a valid %s: %v\n%.400s", method, path, status, def, err, answer)
		}
		return answer
	}

	// The event stream: every frame is validated as it is read.
	frames := d.stream(t, "0")
	kinds := make(map[string]string)
	wait := func(ch <-chan sseFrame, what string, match func(model.Event) bool) model.Event {
		t.Helper()
		deadline := time.After(20 * time.Second)
		for {
			select {
			case f, ok := <-ch:
				if !ok {
					t.Fatalf("event stream closed while waiting for %s", what)
				}
				if err := sch.validate("Event", f.data); err != nil {
					t.Errorf("a %s event is not a valid Event: %v\n%.400s", f.event, err, f.data)
				}
				var ev model.Event
				if err := json.Unmarshal([]byte(f.data), &ev); err != nil {
					t.Fatalf("event: %v", err)
				}
				if f.event != ev.Kind || f.id != strconv.FormatUint(ev.Seq, 10) {
					t.Errorf("frame id %q event %q carries seq %d kind %q", f.id, f.event, ev.Seq, ev.Kind)
				}
				if _, seen := kinds[ev.Kind]; !seen {
					kinds[ev.Kind] = f.data
				}
				if match(ev) {
					return ev
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	kind := func(k string) func(model.Event) bool {
		return func(ev model.Event) bool { return ev.Kind == k }
	}

	// Before any session.
	health := call("GET", "/api/v1/health", "", nil, 200, "Health")
	if empty := call("GET", "/api/v1/sessions", "", nil, 200, "SessionList"); strings.TrimSpace(empty) != "[]" {
		t.Errorf("no sessions = %s", empty)
	}
	if empty := call("GET", "/api/v1/recordings", "", nil, 200, "RecordingList"); strings.TrimSpace(empty) != "[]" {
		t.Errorf("no recordings = %s", empty)
	}
	if ports := call("GET", "/api/v1/serial-ports", "", nil, 200, "SerialPortList"); !strings.Contains(ports, "ttyUSB0") {
		t.Errorf("serial ports = %s", ports)
	}

	// Error answers. 400: a request the engine refuses, and one the handler
	// cannot read.
	code := func(answer, want string) {
		t.Helper()
		if got := errorCode(t, answer); got != want {
			t.Errorf("error code %q, want %q: %s", got, want, answer)
		}
	}
	code(call("POST", "/api/v1/sessions", "", `{"url":"http://x"}`, 400, ""), service.CodeInvalidArgument)
	code(call("POST", "/api/v1/sessions", "", `{"url":`, 400, ""), service.CodeInvalidArgument)
	code(call("GET", "/api/v1/events?since=abc", "", nil, 400, ""), service.CodeInvalidArgument)
	// 401: no token. 403: a host the daemon does not answer to.
	status, _, answer := d.request(t, "GET", "/api/v1/health", "", "", nil)
	if err := sch.validate("ErrorResponse", answer); err != nil || status != http.StatusUnauthorized {
		t.Errorf("without token = %d %s: %v", status, answer, err)
	}
	status, _, answer = d.request(t, "GET", "/api/v1/health", testToken, "", func(r *http.Request) { r.Host = "evil.example" })
	if err := sch.validate("ErrorResponse", answer); err != nil || status != http.StatusForbidden {
		t.Errorf("with a foreign host = %d %s: %v", status, answer, err)
	}
	// 403: a target outside the allow-list.
	code(call("POST", "/api/v1/sessions", "TargetConfig", model.TargetConfig{URL: "tcp://192.0.2.1:502"}, 403, ""), service.CodeNotAllowed)
	// 404: an unknown session, recording and endpoint.
	code(call("GET", "/api/v1/sessions/nope/state", "", nil, 404, ""), service.CodeNotFound)
	code(call("POST", "/api/v1/recordings/nope/stop", "", nil, 404, ""), service.CodeNotFound)
	code(call("GET", "/api/v1/unknown", "", nil, 404, ""), service.CodeNotFound)
	// 502: a device that does not answer, and one that is not SunSpec; both
	// with the probe attempts.
	silent := dev.cfg()
	silent.UnitID, silent.TimeoutMS = 9, 100
	failed := call("POST", "/api/v1/sessions", "TargetConfig", silent, 502, "")
	code(failed, service.CodeConnectFailed)
	if !strings.Contains(failed, `"attempts":[{"base_address":`) || !strings.Contains(failed, `"matched":false`) {
		t.Errorf("a failed open carries no attempts: %s", failed)
	}
	noMarker := dev.cfg()
	noMarker.BaseAddresses = []uint16{40002}
	notSunSpec := call("POST", "/api/v1/sessions", "TargetConfig", noMarker, 502, "")
	code(notSunSpec, service.CodeNotSunSpec)
	if !strings.Contains(notSunSpec, `"attempts":[{"base_address":40002`) {
		t.Errorf("not_sunspec carries no attempts: %s", notSunSpec)
	}

	// A session.
	opened := call("POST", "/api/v1/sessions", "TargetConfig", dev.cfg(), 201, "SessionInfo")
	var info model.SessionInfo
	if err := json.Unmarshal([]byte(opened), &info); err != nil {
		t.Fatal(err)
	}
	session := "/api/v1/sessions/" + info.ID
	wait(frames, "session_status", kind(model.EventSessionStatus))
	// 429: the session limit.
	code(call("POST", "/api/v1/sessions", "TargetConfig", dev.cfg(), 429, ""), service.CodeLimitExceeded)

	call("GET", session, "", nil, 200, "SessionInfo")
	call("GET", "/api/v1/sessions", "", nil, 200, "SessionList")
	idle := call("GET", session+"/state", "", nil, 200, "SessionState")
	if !strings.Contains(idle, `"last_sample":null,"recording":null`) {
		t.Errorf("idle state = %.300s", idle)
	}
	call("POST", session+"/rediscover", "", nil, 200, "SessionInfo")
	wait(frames, "discovery_changed", kind(model.EventDiscoveryChanged))

	// Single reads; the answer is the snapshot export too.
	export := newSchemas(t, "snapshot-1.schema.json")
	for _, req := range []any{
		nil,
		model.SnapshotRequest{},
		model.SnapshotRequest{IncludeRaw: true},
		model.SnapshotRequest{Metadata: model.MetadataCompact, ModelIndexes: []int{4, 5}},
	} {
		reqDef := "SnapshotRequest"
		if req == nil {
			reqDef = "" // an empty body selects the defaults
		}
		snap := call("POST", session+"/snapshot", reqDef, req, 200, "Snapshot")
		if err := export.validate("", snap); err != nil {
			t.Errorf("the snapshot is not a valid export: %v", err)
		}
	}
	code(call("POST", session+"/snapshot", "", `{"metadata":"terse"}`, 400, ""), service.CodeInvalidArgument)
	for _, format := range []string{model.ExportCSV, model.ExportDeviceFile} {
		file := call("POST", session+"/export", "ExportRequest", map[string]string{"format": format}, 200, "ExportFile")
		if !strings.Contains(file, `"mime":"`) {
			t.Errorf("export %s = %.200s", format, file)
		}
	}
	code(call("POST", session+"/export", "", `{"format":"xml"}`, 400, ""), service.CodeInvalidArgument)

	// Recording and polling.
	started := call("POST", session+"/recordings", "RecordingConfig", model.RecordingConfig{Name: "run", RotateBytes: 1 << 20}, 201, "RecordingInfo")
	if !strings.Contains(started, `"state":"running"`) {
		t.Errorf("started recording = %s", started)
	}
	wait(frames, "recording_status", kind(model.EventRecordingStatus))
	code(call("POST", session+"/recordings", "RecordingConfig", model.RecordingConfig{}, 400, ""), service.CodeInvalidArgument)
	code(call("PUT", session+"/polling", "", `{"interval_s":4000}`, 400, ""), service.CodeInvalidArgument)
	call("PUT", session+"/polling", "PollConfig", model.PollConfig{IntervalS: 1}, 204, "")
	wait(frames, "a second recorded sample", func(ev model.Event) bool {
		return ev.Kind == model.EventSample && ev.Recording != nil && ev.Recording.Samples >= 2
	})
	busy := call("GET", session+"/state", "", nil, 200, "SessionState")
	if !strings.Contains(busy, `"last_sample":{`) || !strings.Contains(busy, `"recording":{`) || !strings.Contains(busy, `"active":true`) {
		t.Errorf("state while polling = %.300s", busy)
	}
	call("GET", "/api/v1/recordings", "", nil, 200, "RecordingList")

	// The chain changes under the poller.
	dev.setModels(t, stdModels()[:3]...)
	wait(frames, "a sample with a changed model", func(ev model.Event) bool {
		return ev.Kind == model.EventSample && ev.Sample.Models[len(ev.Sample.Models)-1].Status == model.ModelChanged
	})
	wait(frames, "discovery_changed", func(ev model.Event) bool {
		return ev.Kind == model.EventDiscoveryChanged && len(ev.Session.Models) == 3
	})

	call("DELETE", session+"/polling", "", nil, 204, "")
	stopped := call("POST", "/api/v1/recordings/run/stop", "", nil, 200, "RecordingInfo")
	if !strings.Contains(stopped, `"state":"stopped"`) || !strings.Contains(stopped, `"stop_reason":"user"`) {
		t.Errorf("stopped recording = %s", stopped)
	}
	wait(frames, "recording stopped", func(ev model.Event) bool {
		return ev.Kind == model.EventRecordingStatus && ev.Recording.State == model.RecordingStopped
	})
	call("GET", "/api/v1/recordings", "", nil, 200, "RecordingList")

	// The download is a recording: every line validates against the schema
	// of the recording format.
	status, header, download := d.request(t, "GET", "/api/v1/recordings/run/download", testToken, "", nil)
	if status != http.StatusOK || header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("download = %d %s", status, header.Get("Content-Type"))
	}
	recording := newSchemas(t, "recording-1.schema.json")
	lines := strings.Split(strings.TrimSuffix(download, "\n"), "\n")
	types := make(map[string]bool)
	for i, line := range lines {
		if err := recording.validate("", line); err != nil {
			t.Errorf("line %d of the download: %v\n%.300s", i+1, err, line)
		}
		var l struct {
			Type string `json:"type"`
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal([]byte(line), &l)
		types[strings.TrimSuffix(l.Type+":"+l.Kind, ":")] = true
	}
	for _, want := range []string{"header", "sample", "event:gap", "event:polling_started", "event:discovery_changed", "footer"} {
		if !types[want] {
			t.Errorf("no %s line in the download (%d lines)", want, len(lines))
		}
	}
	// 416: a range beyond the recording.
	status, _, answer = d.request(t, "GET", "/api/v1/recordings/run/download", testToken, "", func(r *http.Request) {
		r.Header.Set("Range", "bytes=99999999-")
	})
	if err := sch.validate("ErrorResponse", answer); err != nil || status != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("a range beyond the recording = %d %s: %v", status, answer, err)
	}

	// The end.
	call("DELETE", "/api/v1/recordings/run", "", nil, 204, "")
	call("DELETE", session, "", nil, 204, "")
	wait(frames, "the session closed", func(ev model.Event) bool {
		return ev.Kind == model.EventSessionStatus && ev.Session.Status == model.SessionClosed
	})
	code(call("DELETE", session, "", nil, 404, ""), service.CodeNotFound)

	// A client that resumes after events this daemon never sent is told to
	// fetch the state again.
	wait(d.stream(t, "999999"), "resync", kind(model.EventResync))

	for _, k := range []string{
		model.EventSample, model.EventSessionStatus, model.EventDiscoveryChanged, model.EventRecordingStatus, model.EventResync,
	} {
		if kinds[k] == "" {
			t.Errorf("no %s event was validated", k)
		}
	}
	if t.Failed() {
		return
	}

	// The schema is not vacuous: an answer is not valid as another entity,
	// and a broken answer is not valid at all.
	for _, c := range []struct{ def, what, doc string }{
		{"ErrorResponse", "a health answer", health},
		{"Health", "an error answer", failed},
		{"SessionInfo", "a session state", idle},
		{"SessionState", "a session", opened},
		{"RecordingInfo", "a list of recordings", "[" + stopped + "]"},
		{"Event", "a sample without its event", busy},
		{"Health", "a health answer with an unknown field", strings.Replace(health, `"status"`, `"token":"x","status"`, 1)},
		{"ErrorResponse", "an error with an unknown code", strings.Replace(failed, `"connect_failed"`, `"oops"`, 1)},
		{"ErrorResponse", "an error whose attempts are null", strings.Replace(failed, `"attempts":[`, `"attempts":null,"x":[`, 1)},
		{"SessionInfo", "a session without models", strings.Replace(opened, `"models":`, `"model_list":`, 1)},
		{"SessionInfo", "a session with unit 0", strings.Replace(opened, `"unit_id":1`, `"unit_id":0`, 1)},
		{"RecordingInfo", "a recording with a text size", strings.Replace(stopped, `"size_bytes":`, `"size_bytes":"big","files_size":`, 1)},
		{"Event", "a sample event without sample", strings.Replace(kinds[model.EventSample], `"sample":{`, `"session":{`, 1)},
		{"Event", "an event without time", strings.Replace(kinds[model.EventResync], `"at"`, `"message"`, 1)},
		{"TargetConfig", "a target with a text port list", `{"url":"tcp://h","base_addresses":"40000"}`},
		{"TargetConfig", "a target with an unknown field", `{"url":"tcp://h","password":"x"}`},
		{"PollConfig", "an interval beyond the maximum", `{"interval_s":3601}`},
		{"SnapshotRequest", "a negative model index", `{"model_indexes":[-1]}`},
		{"RecordingConfig", "a path as name", `{"name":"../x"}`},
		{"ExportRequest", "no format", `{}`},
	} {
		if err := sch.validate(c.def, c.doc); err == nil {
			t.Errorf("%s was accepted as a %s", c.what, c.def)
		}
	}
}
