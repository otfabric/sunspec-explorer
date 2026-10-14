// SPDX-License-Identifier: MIT

package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/api"
	"github.com/otfabric/sunspec-explorer/internal/engine"
	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/readonly"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

func errorCode(t testing.TB, body string) string {
	t.Helper()
	var v struct {
		Error service.Error `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("error body %q: %v", body, err)
	}
	return v.Error.Code
}

// A13: every API request needs the token, also on loopback.
func TestAuth(t *testing.T) {
	d := newDaemon(t, engine.Options{}, api.Options{UI: http.NotFoundHandler()})

	paths := []string{"/api/v1/health", "/api/v1/sessions", "/api/v1/events", "/api/v1/recordings", "/api/v1/nope", "/api/v1"}
	for _, path := range paths {
		for name, token := range map[string]string{"missing": "", "wrong": "wrong-token", "prefix": testToken[:len(testToken)-1]} {
			status, header, body := d.request(t, "GET", path, token, "", nil)
			if status != http.StatusUnauthorized || errorCode(t, body) != service.CodeUnauthorized ||
				!strings.HasPrefix(header.Get("WWW-Authenticate"), "Bearer") {
				t.Errorf("%s with a %s token = %d %s", path, name, status, body)
			}
			if strings.Contains(body, testToken) {
				t.Errorf("the answer contains the token")
			}
		}
	}
	// The token counts in the Authorization header only.
	for _, edit := range []func(*http.Request){
		func(r *http.Request) { r.URL.RawQuery = "token=" + testToken },
		func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "token", Value: testToken}) },
		func(r *http.Request) { r.Header.Set("Authorization", "Basic "+testToken) },
		func(r *http.Request) { r.Header.Set("Authorization", testToken) },
		func(r *http.Request) { r.Header.Set("X-Token", testToken) },
	} {
		if status, _, _ := d.request(t, "GET", "/api/v1/health", "", "", edit); status != http.StatusUnauthorized {
			t.Errorf("a token outside the bearer header was accepted: %d", status)
		}
	}

	status, header, body := d.request(t, "GET", "/api/v1/health", testToken, "", nil)
	if status != http.StatusOK || !strings.Contains(body, `"api_version":"1.0"`) {
		t.Fatalf("with the token = %d %s", status, body)
	}
	if status, _, _ := d.request(t, "GET", "/api/v1/health", "", "", func(r *http.Request) {
		r.Header.Set("Authorization", "bearer "+testToken)
	}); status != http.StatusOK {
		t.Errorf("lower-case scheme = %d", status)
	}
	// Same origin only: no CORS headers, nothing cached, no cookie.
	for _, name := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Headers", "Set-Cookie"} {
		if header.Get(name) != "" {
			t.Errorf("header %s = %q", name, header.Get(name))
		}
	}
	if header.Get("Cache-Control") != "no-store" || header.Get("X-Content-Type-Options") != "nosniff" ||
		header.Get("Referrer-Policy") != "no-referrer" || header.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v", header)
	}
	if status, header, _ := d.request(t, "OPTIONS", "/api/v1/sessions", "", "", func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("Access-Control-Request-Method", "POST")
	}); status == http.StatusOK || status == http.StatusNoContent || header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("a CORS preflight was answered: %d", status)
	}

	// A token that is empty on the server side opens nothing.
	open := newDaemon(t, engine.Options{}, api.Options{Token: func() string { return "" }})
	if status, _, _ := open.request(t, "GET", "/api/v1/health", "", "", nil); status != http.StatusUnauthorized {
		t.Errorf("empty server token, empty request token = %d", status)
	}
}

// A13: a request for another host name is refused, whatever its token.
func TestHostCheck(t *testing.T) {
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ui") })
	d := newDaemon(t, engine.Options{}, api.Options{UI: ui})

	for _, host := range []string{"evil.example", "evil.example:80", "127.0.0.1.evil.example", "192.168.1.10:8765", "localhost.evil.example"} {
		for _, path := range []string{"/api/v1/health", "/", "/index.html"} {
			status, _, body := d.request(t, "GET", path, testToken, "", func(r *http.Request) { r.Host = host })
			if status != http.StatusForbidden || errorCode(t, body) != service.CodeUnauthorized {
				t.Errorf("Host %s, %s = %d %s", host, path, status, body)
			}
		}
	}
	// The port is free: an SSH forward may use another local port.
	for _, host := range []string{"127.0.0.1", "127.0.0.1:9999", "localhost:8765", "LOCALHOST", "[::1]:8765"} {
		if status, _, _ := d.request(t, "GET", "/api/v1/health", testToken, "", func(r *http.Request) { r.Host = host }); status != http.StatusOK {
			t.Errorf("Host %s = %d", host, status)
		}
		if status, _, body := d.request(t, "GET", "/", "", "", func(r *http.Request) { r.Host = host }); status != http.StatusOK || body != "ui" {
			t.Errorf("UI with Host %s = %d", host, status)
		}
	}

	// --no-ui: nothing outside the API.
	bare := newDaemon(t, engine.Options{}, api.Options{})
	if status, _, _ := bare.request(t, "GET", "/", "", "", nil); status != http.StatusNotFound {
		t.Errorf("/ without UI = %d", status)
	}
}

func TestRequestErrors(t *testing.T) {
	d := newDaemon(t, engine.Options{MaxSessions: 1, AllowTarget: nil}, api.Options{})
	dev := newSim(t)

	cases := []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"POST", "/api/v1/sessions", `{"url":`, http.StatusBadRequest, service.CodeInvalidArgument},
		{"POST", "/api/v1/sessions", `[1]`, http.StatusBadRequest, service.CodeInvalidArgument},
		{"POST", "/api/v1/sessions", ``, http.StatusBadRequest, service.CodeInvalidArgument},
		{"POST", "/api/v1/sessions", `{"url":"tcp://h","pad":"` + strings.Repeat("x", 70<<10) + `"}`, http.StatusBadRequest, service.CodeInvalidArgument},
		{"GET", "/api/v1/sessions/nope", ``, http.StatusNotFound, service.CodeNotFound},
		{"GET", "/api/v1/sessions/nope/state", ``, http.StatusNotFound, service.CodeNotFound},
		{"PUT", "/api/v1/sessions/nope/polling", `{"interval_s":1}`, http.StatusNotFound, service.CodeNotFound},
		{"GET", "/api/v1/events?since=abc", ``, http.StatusBadRequest, service.CodeInvalidArgument},
		// This daemon has no data directory.
		{"GET", "/api/v1/recordings/r/download", ``, http.StatusForbidden, service.CodeNotAllowed},
		{"DELETE", "/api/v1/recordings/r", ``, http.StatusForbidden, service.CodeNotAllowed},
		{"GET", "/api/v1/unknown", ``, http.StatusNotFound, service.CodeNotFound},
		{"PATCH", "/api/v1/sessions", ``, http.StatusNotFound, service.CodeNotFound},
	}
	for _, c := range cases {
		status, _, body := d.request(t, c.method, c.path, testToken, c.body, nil)
		if status != c.status || errorCode(t, body) != c.code {
			t.Errorf("%s %s = %d %.120s, want %d %s", c.method, c.path, status, body, c.status, c.code)
		}
	}

	// The limit of the engine arrives as 429; the session as 201.
	b, _ := json.Marshal(dev.cfg())
	status, _, body := d.request(t, "POST", "/api/v1/sessions", testToken, string(b), nil)
	if status != http.StatusCreated {
		t.Fatalf("open = %d %s", status, body)
	}
	var info model.SessionInfo
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatal(err)
	}
	if status, _, body := d.request(t, "POST", "/api/v1/sessions", testToken, string(b), nil); status != http.StatusTooManyRequests ||
		errorCode(t, body) != service.CodeLimitExceeded {
		t.Errorf("second open = %d %s", status, body)
	}
	if status, _, body := d.request(t, "GET", "/api/v1/sessions/"+info.ID, testToken, "", nil); status != http.StatusOK ||
		!strings.Contains(body, `"id":"`+info.ID+`"`) {
		t.Errorf("get session = %d %s", status, body)
	}
	// An empty body selects the defaults.
	if status, _, body := d.request(t, "POST", "/api/v1/sessions/"+info.ID+"/snapshot", testToken, "", nil); status != http.StatusOK ||
		!strings.Contains(body, model.SnapshotSchema) {
		t.Errorf("snapshot = %d %.200s", status, body)
	}
	if status, _, body := d.request(t, "DELETE", "/api/v1/sessions/"+info.ID, testToken, "", nil); status != http.StatusNoContent || body != "" {
		t.Errorf("close = %d %q", status, body)
	}
}

// fake is a service whose failures are not service errors.
type fake struct {
	service.Service
	err error
}

func (f fake) Health(context.Context) (model.Health, error) { return model.Health{}, f.err }

func (f fake) OpenRecording(context.Context, string, int64) (io.ReadCloser, model.RecordingInfo, error) {
	const body = "{\"type\":\"header\"}\n"
	return io.NopCloser(strings.NewReader(body)), model.RecordingInfo{Name: "run-1", SizeBytes: int64(len(body))}, nil
}

func TestInternalErrorsAndDownload(t *testing.T) {
	handler := func(err error) http.Handler {
		return api.New(fake{err: err}, api.Options{Token: func() string { return testToken }, Hosts: []string{"example.com"}})
	}
	get := func(h http.Handler, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// What is not written for the user stays inside.
	rec := get(handler(errors.New("open /secret/path: permission denied")), "/api/v1/health")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") ||
		errorCode(t, rec.Body.String()) != service.CodeInternal {
		t.Errorf("internal error = %d %s", rec.Code, rec.Body)
	}
	rec = get(handler(context.DeadlineExceeded), "/api/v1/health")
	if rec.Code != http.StatusRequestTimeout || errorCode(t, rec.Body.String()) != service.CodeCanceled {
		t.Errorf("canceled = %d %s", rec.Code, rec.Body)
	}
	rec = get(handler(service.Errorf(service.CodeNotAllowed, "outside the allow-list")), "/api/v1/health")
	if rec.Code != http.StatusForbidden || errorCode(t, rec.Body.String()) != service.CodeNotAllowed {
		t.Errorf("not allowed = %d %s", rec.Code, rec.Body)
	}

	rec = get(handler(nil), "/api/v1/recordings/abc/download")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Disposition") != `attachment; filename="run-1.jsonl"` ||
		rec.Header().Get("Content-Type") != "application/x-ndjson" || !strings.Contains(rec.Body.String(), "header") {
		t.Errorf("download = %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
}

// R7: a recording downloads while it runs and afterwards, whole or from an
// offset, so that a download that broke off can be resumed.
func TestDownloadRanges(t *testing.T) {
	d := newDaemon(t, engine.Options{DataDir: t.TempDir()}, api.Options{})
	dev := newSim(t)
	info, err := d.svc.OpenSession(ctx, dev.cfg())
	if err != nil {
		t.Fatal(err)
	}
	events, _ := d.svc.Subscribe(ctx, 0)
	if _, err := d.svc.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "dl", RotateBytes: 5000}); err != nil {
		t.Fatal(err)
	}
	if err := d.svc.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	next(t, events, "a recorded sample", func(ev model.Event) bool { return ev.Recording != nil && ev.Recording.Samples >= 1 })

	get := func(rng string) (int, http.Header, string) {
		return d.request(t, "GET", "/api/v1/recordings/dl/download", testToken, "", func(r *http.Request) {
			if rng != "" {
				r.Header.Set("Range", rng)
			}
		})
	}

	// While it runs.
	status, header, running := get("")
	if status != http.StatusOK || header.Get("Content-Type") != "application/x-ndjson" || header.Get("Accept-Ranges") != "bytes" ||
		header.Get("Content-Disposition") != `attachment; filename="dl.jsonl"` || !strings.HasSuffix(running, "\n") {
		t.Fatalf("download while running = %d %v", status, header)
	}
	if _, err := d.svc.StopRecording(ctx, "dl"); err != nil {
		t.Fatal(err)
	}
	_, _, whole := get("")
	if !strings.HasPrefix(whole, running) || len(whole) <= len(running) {
		t.Fatalf("finished recording: %d bytes, running download: %d", len(whole), len(running))
	}
	total := strconv.Itoa(len(whole))

	// What was added since the first download.
	status, header, rest := get("bytes=" + strconv.Itoa(len(running)) + "-")
	if status != http.StatusPartialContent || rest != whole[len(running):] ||
		header.Get("Content-Range") != "bytes "+strconv.Itoa(len(running))+"-"+strconv.Itoa(len(whole)-1)+"/"+total {
		t.Errorf("open range = %d %q, %d bytes", status, header.Get("Content-Range"), len(rest))
	}
	// A closed range, across a file boundary.
	status, header, part := get("bytes=4990-5019")
	if status != http.StatusPartialContent || part != whole[4990:5020] || header.Get("Content-Range") != "bytes 4990-5019/"+total ||
		header.Get("Content-Length") != "30" {
		t.Errorf("closed range = %d %q %q", status, header.Get("Content-Range"), part)
	}
	// A range that ends beyond the recording is cut.
	if status, _, part := get("bytes=10-99999999"); status != http.StatusPartialContent || part != whole[10:] {
		t.Errorf("long range = %d, %d bytes", status, len(part))
	}
	if status, header, _ := get("bytes=" + strconv.Itoa(len(whole)+1) + "-"); status != http.StatusRequestedRangeNotSatisfiable ||
		header.Get("Content-Range") != "bytes */"+total {
		t.Errorf("range beyond the end = %d %q", status, header.Get("Content-Range"))
	}
	for _, bad := range []string{"bytes=-500", "bytes=5-2", "bytes=0-1,5-6", "lines=1-2", "bytes=x-", "bytes=5"} {
		if status, _, body := get(bad); status != http.StatusBadRequest || errorCode(t, body) != service.CodeInvalidArgument {
			t.Errorf("Range %q = %d", bad, status)
		}
	}
	// The same recording converted to CSV; that form has no ranges.
	status, header, table := d.request(t, "GET", "/api/v1/recordings/dl/download?format=csv", testToken, "", nil)
	if status != http.StatusOK || !strings.HasPrefix(header.Get("Content-Type"), "text/csv") ||
		header.Get("Content-Disposition") != `attachment; filename="dl.csv"` || !strings.HasPrefix(table, csvColumns+"\n") {
		t.Errorf("CSV download = %d %v %.80s", status, header, table)
	}
	if status, _, same := d.request(t, "GET", "/api/v1/recordings/dl/download?format=jsonl", testToken, "", nil); status != http.StatusOK || same != whole {
		t.Errorf("format=jsonl = %d, %d bytes", status, len(same))
	}
	if status, _, _ := d.request(t, "GET", "/api/v1/recordings/dl/download?format=csv", testToken, "", func(r *http.Request) {
		r.Header.Set("Range", "bytes=0-10")
	}); status != http.StatusBadRequest {
		t.Errorf("Range on CSV = %d", status)
	}
	if status, _, _ := d.request(t, "GET", "/api/v1/recordings/dl/download?format=xlsx", testToken, "", nil); status != http.StatusBadRequest {
		t.Errorf("unknown format = %d", status)
	}
	// No token, no recording.
	if status, _, _ := d.request(t, "GET", "/api/v1/recordings/dl/download", "", "", nil); status != http.StatusUnauthorized {
		t.Errorf("download without token = %d", status)
	}
	// A name with a path in it reaches nothing.
	for _, id := range []string{"..%2F..%2Ftoken", "%2e%2e", "dl%2F..%2Fdl"} {
		if status, _, _ := d.request(t, "GET", "/api/v1/recordings/"+id+"/download", testToken, "", nil); status != http.StatusNotFound {
			t.Errorf("download of %s = %d, want 404", id, status)
		}
	}
	if status, _, _ := d.request(t, "DELETE", "/api/v1/recordings/dl", testToken, "", nil); status != http.StatusNoContent {
		t.Errorf("delete = %d", status)
	}
}

// collect reads events until n arrived or the stream ends.
func collect(t testing.TB, ch <-chan model.Event, n int) []model.Event {
	t.Helper()
	var out []model.Event
	deadline := time.After(10 * time.Second)
	for len(out) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("got %d of %d events", len(out), n)
		}
	}
	return out
}

func seqs(events []model.Event) []uint64 {
	out := make([]uint64, len(events))
	for i, ev := range events {
		out[i] = ev.Seq
	}
	return out
}

// The tunnel drops and comes back: the stream resumes after the last event
// seen, without a loss and without a duplicate; when the buffer wrapped in
// between, the gap is reported.
func TestEventsResume(t *testing.T) {
	d := newDaemon(t, engine.Options{EventBuffer: 6, MaxSessions: 1}, api.Options{})
	dev := newSim(t)
	// Opening and closing a session makes two events.
	cycle := func() {
		info, err := d.svc.OpenSession(ctx, dev.cfg())
		if err != nil {
			t.Fatalf("OpenSession: %v", err)
		}
		if err := d.svc.CloseSession(ctx, info.ID); err != nil {
			t.Fatal(err)
		}
	}

	first, drop := context.WithCancel(ctx)
	live, err := d.svc.Subscribe(first, 0)
	if err != nil {
		t.Fatal(err)
	}
	cycle()
	seen := collect(t, live, 2)
	if !slices.Equal(seqs(seen), []uint64{1, 2}) {
		t.Fatalf("live events = %v", seqs(seen))
	}
	drop() // the tunnel drops

	cycle() // events 3 and 4 happen meanwhile
	again, stop := context.WithCancel(ctx)
	resumed, err := d.svc.Subscribe(again, 2)
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, resumed, 2)
	if !slices.Equal(seqs(got), []uint64{3, 4}) || got[0].Kind != model.EventSessionStatus {
		t.Fatalf("resumed = %v, want exactly the missed events 3 and 4", seqs(got))
	}
	cycle()
	if got := collect(t, resumed, 2); !slices.Equal(seqs(got), []uint64{5, 6}) {
		t.Errorf("after the replay = %v, want 5 and 6", seqs(got))
	}
	stop()

	// Far more happens than the buffer holds: the client is told.
	for i := 0; i < 4; i++ {
		cycle()
	}
	late, stopLate := context.WithCancel(ctx)
	defer stopLate()
	wrapped, err := d.svc.Subscribe(late, 6)
	if err != nil {
		t.Fatal(err)
	}
	got = collect(t, wrapped, 7)
	if got[0].Kind != model.EventResync || !slices.Equal(seqs(got[1:]), []uint64{9, 10, 11, 12, 13, 14}) {
		t.Errorf("after the buffer wrapped = %v (first kind %s), want a resync and the 6 newest", seqs(got), got[0].Kind)
	}

	// A sequence number of an earlier daemon run.
	stale, stopStale := context.WithCancel(ctx)
	defer stopStale()
	other, err := d.svc.Subscribe(stale, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, other, 1); got[0].Kind != model.EventResync {
		t.Errorf("since a future number = %+v", got)
	}

	// Last-Event-ID does what ?since= does; the frames carry id and event.
	req, _ := http.NewRequest("GET", d.srv.URL+"/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Last-Event-ID", "13")
	res, err := d.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.Header.Get("Content-Type") != "text/event-stream" || res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("stream headers = %v", res.Header)
	}
	frames := make(chan [3]string, 1)
	go readSSE(res.Body, func(id, event, data string) {
		select {
		case frames <- [3]string{id, event, data}:
		default:
		}
	})
	select {
	case f := <-frames:
		if f[0] != "14" || f[1] != model.EventSessionStatus || !strings.Contains(f[2], `"seq":14`) {
			t.Errorf("frame = %v", f)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no frame")
	}
}

// A rotated token ends the streams that were opened with the old one.
func TestEventsEndWhenTokenRotates(t *testing.T) {
	var current atomic.Value
	current.Store(testToken)
	d := newDaemon(t, engine.Options{}, api.Options{
		Token:     func() string { return current.Load().(string) },
		KeepAlive: 20 * time.Millisecond,
	})
	events, err := d.svc.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The stream stays open across keep-alives while the token is valid.
	select {
	case _, ok := <-events:
		if !ok {
			t.Fatal("stream closed with a valid token")
		}
	case <-time.After(100 * time.Millisecond):
	}
	current.Store("another-token-0123456789abcdefghijklmnop")
	select {
	case _, ok := <-events:
		if ok {
			t.Error("event after rotation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream still open after the token was rotated")
	}
	if _, err := d.svc.Health(ctx); service.CodeOf(err) != service.CodeUnauthorized {
		t.Errorf("old token after rotation = %v", err)
	}
}

// A11 through the API: whatever a client sends to the daemon, only FC03 and
// FC04 reach the device.
func TestWireOnlyReadsThroughAPI(t *testing.T) {
	dev := newSim(t)
	tap, err := readonly.NewWireTap(dev.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer tap.Close()
	d := newDaemon(t, engine.Options{}, api.Options{})

	cfg := model.TargetConfig{URL: "tcp://" + tap.Addr(), UnitID: 1, TimeoutMS: 500}
	events, err := d.svc.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := d.svc.OpenSession(ctx, cfg)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if _, err := d.svc.Snapshot(ctx, info.ID, model.SnapshotRequest{IncludeRaw: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.svc.Rediscover(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.svc.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	next(t, events, "sample", func(ev model.Event) bool { return ev.Kind == model.EventSample })

	// A client that tries to make the daemon write: fields and endpoints that
	// do not exist, and every method on every path.
	base := "/api/v1/sessions/" + info.ID
	hostile := `{"write":true,"function_code":6,"fc":16,"address":40068,"value":7,"values":[7],"registers":[7],"frame":"0106","raw":"AQYAAAAH"}`
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		for _, path := range []string{
			base + "/write", base + "/registers", base + "/points/DA", base + "/models/0", base + "/raw", base + "/modbus",
			"/api/v1/write", "/api/v1/modbus", "/api/v1/raw",
		} {
			if status, _, _ := d.request(t, method, path, testToken, hostile, nil); status != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404", method, path, status)
			}
		}
	}
	for _, path := range []string{base + "/snapshot", base + "/rediscover"} {
		if status, _, body := d.request(t, "POST", path, testToken, hostile, nil); status != http.StatusOK {
			t.Errorf("POST %s with extra fields = %d %.100s", path, status, body)
		}
	}
	if status, _, _ := d.request(t, "PUT", base+"/polling", testToken, hostile, nil); status != http.StatusNoContent {
		t.Errorf("PUT polling with extra fields = %d", status)
	}
	next(t, events, "sample", func(ev model.Event) bool { return ev.Kind == model.EventSample })

	if err := d.svc.CloseSession(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	cfg.RegType = model.RegTypeInput
	if _, err := d.svc.OpenSession(ctx, cfg); err == nil {
		t.Error("input registers on this device: no error")
	}

	if got := tap.FunctionCodes(); !slices.Equal(got, []uint8{3, 4}) {
		t.Errorf("function codes on the wire = %v, want [3 4]", got)
	}
	if tap.Requests() < 20 || d.eng.Guard().Violations() != 0 {
		t.Errorf("wire: %d requests; guard: %d violations", tap.Requests(), d.eng.Guard().Violations())
	}
}
