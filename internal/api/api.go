// SPDX-License-Identifier: MIT

// Package api serves a service.Service over HTTP: JSON requests under
// /api/v1, the event stream as server-sent events, and, next to them, the
// web UI. It is what a browser talks to; there is no Go client.
//
// Every request must name a host the daemon answers to, and every /api/v1
// request must carry the bearer token, also on loopback: other local users
// and other pages in the user's browser can reach a loopback port too.
//
// No endpoint accepts a file path, a Modbus frame, a function code or a
// value to write.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

const (
	prefix = "/api/v1"
	// maxBody bounds a request body; the largest real one is a target
	// configuration.
	maxBody = 64 << 10
	// DefaultKeepAlive is the interval of the comment lines that keep an idle
	// event stream open through proxies and tunnels.
	DefaultKeepAlive = 15 * time.Second
)

// Options configures the handler.
type Options struct {
	// Token returns the bearer token that is valid now. It is called for
	// every request, so that a rotated token takes effect at once.
	Token func() string
	// Hosts lists the host names (without port) the daemon answers to, such
	// as "127.0.0.1" and "localhost". A request with another Host header is
	// refused: that is what stops a hostile web page from reaching the daemon
	// through a DNS name it points at 127.0.0.1. The port is not compared: an
	// SSH forward may use another local port.
	Hosts []string
	// UI serves everything outside /api/v1. Nil answers 404 there.
	UI http.Handler
	// KeepAlive overrides DefaultKeepAlive.
	KeepAlive time.Duration
	// Logger receives request errors. Nil discards them.
	Logger *slog.Logger
}

type server struct {
	svc   service.Service
	opts  Options
	hosts map[string]bool
	log   *slog.Logger
}

// New returns the handler of the daemon.
func New(svc service.Service, opts Options) http.Handler {
	s := &server{svc: svc, opts: opts, hosts: make(map[string]bool), log: opts.Logger}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.opts.KeepAlive <= 0 {
		s.opts.KeepAlive = DefaultKeepAlive
	}
	for _, h := range opts.Hosts {
		s.hosts[strings.ToLower(h)] = true
	}

	api := http.NewServeMux()
	api.HandleFunc("GET "+prefix+"/health", s.health)
	api.HandleFunc("GET "+prefix+"/sessions", s.sessions)
	api.HandleFunc("POST "+prefix+"/sessions", s.openSession)
	api.HandleFunc("GET "+prefix+"/sessions/{id}", s.session)
	api.HandleFunc("DELETE "+prefix+"/sessions/{id}", s.closeSession)
	api.HandleFunc("POST "+prefix+"/sessions/{id}/rediscover", s.rediscover)
	api.HandleFunc("POST "+prefix+"/sessions/{id}/snapshot", s.snapshot)
	api.HandleFunc("PUT "+prefix+"/sessions/{id}/polling", s.startPolling)
	api.HandleFunc("DELETE "+prefix+"/sessions/{id}/polling", s.stopPolling)
	api.HandleFunc("GET "+prefix+"/sessions/{id}/state", s.state)
	api.HandleFunc("POST "+prefix+"/sessions/{id}/recordings", s.startRecording)
	api.HandleFunc("POST "+prefix+"/sessions/{id}/export", s.exportSession)
	api.HandleFunc("GET "+prefix+"/events", s.events)
	api.HandleFunc("GET "+prefix+"/recordings", s.recordings)
	api.HandleFunc("POST "+prefix+"/recordings/{rid}/stop", s.stopRecording)
	api.HandleFunc("GET "+prefix+"/recordings/{rid}/download", s.download)
	api.HandleFunc("DELETE "+prefix+"/recordings/{rid}", s.deleteRecording)
	api.HandleFunc("GET "+prefix+"/serial-ports", s.serialPorts)
	api.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, service.Errorf(service.CodeNotFound, "no such endpoint: %s %s", r.Method, r.URL.Path))
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")

		if !s.hostAllowed(r.Host) {
			writeError(w, http.StatusForbidden, service.Errorf(service.CodeUnauthorized,
				"this daemon does not answer to the host name in the request"))
			return
		}
		if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
			if s.opts.UI == nil {
				http.NotFound(w, r)
				return
			}
			s.opts.UI.ServeHTTP(w, r)
			return
		}
		h.Set("Cache-Control", "no-store")
		if !s.authorized(r) {
			h.Set("WWW-Authenticate", `Bearer realm="sunspec-explorerd"`)
			writeError(w, http.StatusUnauthorized, service.Errorf(service.CodeUnauthorized, "missing or wrong bearer token"))
			return
		}
		api.ServeHTTP(w, r)
	})
}

// hostAllowed reports whether a Host header names a host the daemon answers
// to. Brackets belong around an IPv6 literal and nowhere else, as a pair: a
// header that is not well formed names no host.
func (s *server) hostAllowed(hostport string) bool {
	host, bracketed := hostport, strings.HasPrefix(hostport, "[")
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	} else if bracketed && strings.HasSuffix(hostport, "]") {
		host = hostport[1 : len(hostport)-1]
	}
	if bracketed && !strings.Contains(host, ":") {
		return false
	}
	return s.hosts[strings.ToLower(host)]
}

// authorized compares the bearer token in constant time. The token is only
// taken from the Authorization header: never from the URL, where it would
// end up in logs and histories, and never from a cookie.
func (s *server) authorized(r *http.Request) bool {
	return s.tokenValid(bearer(r))
}

func (s *server) tokenValid(got string) bool {
	want := s.opts.Token()
	return want != "" && got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func bearer(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.Health(r.Context())
	s.reply(w, r, v, err)
}

func (s *server) sessions(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.Sessions(r.Context())
	s.reply(w, r, v, err)
}

func (s *server) openSession(w http.ResponseWriter, r *http.Request) {
	var cfg model.TargetConfig
	if !s.decode(w, r, &cfg) {
		return
	}
	v, err := s.svc.OpenSession(r.Context(), cfg)
	s.replyStatus(w, r, http.StatusCreated, v, err)
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.State(r.Context(), r.PathValue("id"))
	s.reply(w, r, st.Session, err)
}

func (s *server) closeSession(w http.ResponseWriter, r *http.Request) {
	s.replyEmpty(w, r, s.svc.CloseSession(r.Context(), r.PathValue("id")))
}

func (s *server) rediscover(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.Rediscover(r.Context(), r.PathValue("id"))
	s.reply(w, r, v, err)
}

func (s *server) snapshot(w http.ResponseWriter, r *http.Request) {
	var req model.SnapshotRequest
	if !s.decode(w, r, &req) {
		return
	}
	v, err := s.svc.Snapshot(r.Context(), r.PathValue("id"), req)
	s.reply(w, r, v, err)
}

func (s *server) startPolling(w http.ResponseWriter, r *http.Request) {
	var cfg model.PollConfig
	if !s.decode(w, r, &cfg) {
		return
	}
	s.replyEmpty(w, r, s.svc.StartPolling(r.Context(), r.PathValue("id"), cfg))
}

func (s *server) stopPolling(w http.ResponseWriter, r *http.Request) {
	s.replyEmpty(w, r, s.svc.StopPolling(r.Context(), r.PathValue("id")))
}

func (s *server) state(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.State(r.Context(), r.PathValue("id"))
	s.reply(w, r, v, err)
}

func (s *server) startRecording(w http.ResponseWriter, r *http.Request) {
	var cfg model.RecordingConfig
	if !s.decode(w, r, &cfg) {
		return
	}
	v, err := s.svc.StartRecording(r.Context(), r.PathValue("id"), cfg)
	s.replyStatus(w, r, http.StatusCreated, v, err)
}

func (s *server) stopRecording(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.StopRecording(r.Context(), r.PathValue("rid"))
	s.reply(w, r, v, err)
}

func (s *server) recordings(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.Recordings(r.Context())
	s.reply(w, r, v, err)
}

func (s *server) serialPorts(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.SerialPorts(r.Context())
	s.reply(w, r, v, err)
}

// exportSession renders a session in another format. The client names a
// format, never a file: it gets the text back and saves it itself.
func (s *server) exportSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Format string `json:"format"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	v, err := s.svc.ExportSession(r.Context(), r.PathValue("id"), req.Format)
	s.reply(w, r, v, err)
}

// downloadCSV streams a recording converted to CSV. The size is not known
// in advance, so there is no Content-Length and no Range.
func (s *server) downloadCSV(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Range") != "" {
		s.fail(w, r, service.Errorf(service.CodeInvalidArgument, "Range is not supported for the CSV form of a recording"))
		return
	}
	body, info, err := s.svc.OpenRecordingCSV(r.Context(), r.PathValue("rid"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = body.Close() }()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", info.Name+".csv"))
	_, _ = io.Copy(w, body)
}

func (s *server) deleteRecording(w http.ResponseWriter, r *http.Request) {
	s.replyEmpty(w, r, s.svc.DeleteRecording(r.Context(), r.PathValue("rid")))
}

// download streams a recording, all its files as one. Recordings are
// addressed by ID: the client never names a path. A running recording is
// delivered up to its size at this moment; "Range: bytes=N-" fetches what
// was added since, or resumes a download that broke off.
func (s *server) download(w http.ResponseWriter, r *http.Request) {
	switch format := r.URL.Query().Get("format"); format {
	case "", "jsonl":
	case "csv":
		s.downloadCSV(w, r)
		return
	default:
		s.fail(w, r, service.Errorf(service.CodeInvalidArgument, "recording format %q: use jsonl or csv", format))
		return
	}
	start, end, ranged, ok := parseRange(r.Header.Get("Range"))
	if !ok {
		s.fail(w, r, service.Errorf(service.CodeInvalidArgument, "unsupported Range header: use bytes=<first>- or bytes=<first>-<last>"))
		return
	}
	body, info, err := s.svc.OpenRecording(r.Context(), r.PathValue("rid"), start)
	if err != nil {
		if ranged && service.CodeOf(err) == service.CodeInvalidArgument {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", info.SizeBytes))
			writeError(w, http.StatusRequestedRangeNotSatisfiable, service.Errorf(service.CodeInvalidArgument, "the range starts beyond the recording"))
			return
		}
		s.fail(w, r, err)
		return
	}
	defer func() { _ = body.Close() }()

	length := info.SizeBytes - start
	if end >= 0 && end-start+1 < length {
		length = end - start + 1
	}
	h := w.Header()
	h.Set("Content-Type", "application/x-ndjson")
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", info.Name+".jsonl"))
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	if ranged {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, info.SizeBytes))
		w.WriteHeader(http.StatusPartialContent)
	}
	_, _ = io.CopyN(w, body, length)
}

// parseRange reads a Range header with a single byte range that names its
// first byte. end is -1 for an open range. ok is false for anything else
// (several ranges, suffix ranges, other units).
func parseRange(header string) (start, end int64, ranged, ok bool) {
	if header == "" {
		return 0, -1, false, true
	}
	spec, found := strings.CutPrefix(header, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, true, false
	}
	first, last, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, true, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(first), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, true, false
	}
	end = -1
	if last = strings.TrimSpace(last); last != "" {
		if end, err = strconv.ParseInt(last, 10, 64); err != nil || end < start {
			return 0, 0, true, false
		}
	}
	return start, end, true, true
}

// events streams the events after ?since= (or Last-Event-ID) as server-sent
// events until the client goes away or its token stops being valid.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.fail(w, r, service.Errorf(service.CodeInternal, "streaming is not supported"))
		return
	}
	raw := r.URL.Query().Get("since")
	if raw == "" {
		raw = r.Header.Get("Last-Event-ID")
	}
	var since uint64
	if raw != "" {
		var err error
		if since, err = strconv.ParseUint(raw, 10, 64); err != nil {
			s.fail(w, r, service.Errorf(service.CodeInvalidArgument, "since must be a sequence number"))
			return
		}
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events, err := s.svc.Subscribe(ctx, since)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	token := bearer(r)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	keepAlive := time.NewTicker(s.opts.KeepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				s.log.Error("event not encoded", "kind", ev.Kind, "error", err)
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Kind, data); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			// A stream opened with a token that was rotated away ends here.
			if !s.tokenValid(token) {
				return
			}
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// decode reads a JSON request body into v. An empty body leaves v as it is.
func (s *server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		s.fail(w, r, service.Errorf(service.CodeInvalidArgument, "request body is too large or unreadable"))
		return false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return true
	}
	if err := json.Unmarshal(body, v); err != nil {
		s.fail(w, r, service.Errorf(service.CodeInvalidArgument, "request body is not valid JSON for this endpoint"))
		return false
	}
	return true
}

func (s *server) reply(w http.ResponseWriter, r *http.Request, v any, err error) {
	s.replyStatus(w, r, http.StatusOK, v, err)
}

func (s *server) replyStatus(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

func (s *server) replyEmpty(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fail answers with the error as JSON. The message of a service.Error is
// written for the user; anything else is logged and not passed on.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var se *service.Error
	switch {
	case errors.As(err, &se):
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		se = service.Errorf(service.CodeCanceled, "the request was canceled")
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		se = service.Errorf(service.CodeInternal, "internal error")
	}
	writeError(w, statusOf(se.Code), se)
}

func statusOf(code string) int {
	switch code {
	case service.CodeInvalidArgument:
		return http.StatusBadRequest
	case service.CodeUnauthorized:
		return http.StatusUnauthorized
	case service.CodeNotAllowed:
		return http.StatusForbidden
	case service.CodeNotFound:
		return http.StatusNotFound
	case service.CodeLimitExceeded:
		return http.StatusTooManyRequests
	case service.CodeConnectFailed, service.CodeNotSunSpec, service.CodeDiscoveryFailed:
		return http.StatusBadGateway
	case service.CodeNotImplemented:
		return http.StatusNotImplemented
	case service.CodeCanceled:
		return http.StatusRequestTimeout
	}
	return http.StatusInternalServerError
}

func writeError(w http.ResponseWriter, status int, e *service.Error) {
	writeJSON(w, status, struct {
		Error *service.Error `json:"error"`
	}{e})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
