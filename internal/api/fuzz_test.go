// SPDX-License-Identifier: MIT

package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/otfabric/sunspec-explorer/internal/api"
	"github.com/otfabric/sunspec-explorer/internal/engine"
)

// A request of any shape, from whoever reached the port: the wrong Host or a
// wrong token gets nothing but a refusal; with the right ones, a body is
// decoded or refused, and an error is always the API's JSON error, never a
// 500. The engine may connect nowhere, so that a request cannot leave the
// process.
func FuzzRequest(f *testing.F) {
	const good = "Bearer " + testToken
	for _, c := range []struct{ method, path, host, auth, rng, body string }{
		{"GET", "/api/v1/health", "127.0.0.1", good, "", ""},
		{"GET", "/api/v1/health", "evil.example", good, "", ""},
		{"GET", "/api/v1/health", "127.0.0.1", "Bearer nope", "", ""},
		{"GET", "/api/v1/health", "[::1]:80", "bearer  " + testToken, "", ""},
		{"POST", "/api/v1/sessions", "localhost:8765", good, "", `{"url":"tcp://10.0.0.1:502","unit_id":1}`},
		{"POST", "/api/v1/sessions", "localhost", good, "", `{"url":"rtu:///dev/ttyUSB0","serial":{"baud":1}}`},
		{"POST", "/api/v1/sessions", "localhost", good, "", `{"url":5}`},
		{"POST", "/api/v1/sessions/x/snapshot", "localhost", good, "", `{"model_indexes":[-1],"metadata":"x"}`},
		{"PUT", "/api/v1/sessions/x/polling", "localhost", good, "", `{"interval_s":0}`},
		{"POST", "/api/v1/sessions/x/recordings", "localhost", good, "", `{"name":"../x","rotate_bytes":-1}`},
		{"POST", "/api/v1/sessions/x/export", "localhost", good, "", `{"format":"exe"}`},
		{"GET", "/api/v1/recordings/x/download?format=csv", "localhost", good, "bytes=0-", ""},
		{"GET", "/api/v1/recordings/x/download", "localhost", good, "bytes=1-2,3-4", ""},
		{"DELETE", "/api/v1/recordings/..%2f..%2fx", "localhost", good, "", ""},
		{"GET", "/api/v1", "localhost", good, "", ""},
		{"TRACE", "/x", "localhost", "", "", ""},
	} {
		f.Add(c.method, c.path, c.host, c.auth, c.rng, []byte(c.body))
	}

	eng := engine.New(engine.Options{
		NoSerial:    true,
		AllowTarget: func(*url.URL) error { return errors.New("no target in this test") },
	})
	f.Cleanup(eng.Close)
	h := api.New(eng, api.Options{Token: func() string { return testToken }, Hosts: []string{"127.0.0.1", "localhost", "::1"}})

	f.Fuzz(func(t *testing.T, method, path, host, auth, rng string, body []byte) {
		// The events endpoint streams until the client leaves.
		if !strings.HasPrefix(path, "/") || strings.Contains(path, "events") {
			return
		}
		req, err := http.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(string(body)))
		if err != nil {
			return
		}
		req.Host = host
		req.Header.Set("Authorization", auth)
		req.Header.Set("Range", rng)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		status := rec.Code
		switch hostOK, tokenOK := hostAllowed(host), goodToken(auth); {
		case !hostOK && status != http.StatusForbidden:
			t.Fatalf("host %q got %d", host, status)
		case hostOK && !tokenOK && isAPI(req) && status != http.StatusUnauthorized:
			t.Fatalf("Authorization %q on %s got %d", auth, path, status)
		}
		if status >= 500 && status != http.StatusNotImplemented {
			t.Fatalf("%s %s: %d: %s", method, path, status, rec.Body)
		}
		if status >= 400 && isAPI(req) && hostAllowed(host) {
			var e struct {
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error == nil || e.Error.Code == "" {
				t.Fatalf("%s %s: %d is not an API error: %q", method, path, status, rec.Body)
			}
		}
	})
}

func isAPI(r *http.Request) bool {
	return r.URL.Path == "/api/v1" || strings.HasPrefix(r.URL.Path, "/api/v1/")
}

func goodToken(auth string) bool {
	scheme, token, ok := strings.Cut(auth, " ")
	return ok && strings.EqualFold(scheme, "Bearer") && strings.TrimSpace(token) == testToken
}

func hostAllowed(hostport string) bool {
	host := strings.ToLower(hostport)
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.HasSuffix(host, "]") && strings.Count(host, ":") == 1 {
		host = host[:i]
	}
	if strings.HasPrefix(host, "[") {
		if i := strings.Index(host, "]"); i > 0 {
			host = host[1:i]
		}
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}
