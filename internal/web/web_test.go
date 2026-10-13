// SPDX-License-Identifier: MIT

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>Explorer</title>")},
		"favicon.svg":          {Data: []byte("<svg/>")},
		"assets/app-abc123.js": {Data: []byte("console.log(1)")},
		".gitkeep":             {Data: nil},
	}
	h := Handler(assets)
	get := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	for _, path := range []string{"/", "/index.html", "/some/deep/link", "/.gitkeep"} {
		rec := get("GET", path)
		if path == "/index.html" && rec.Code == http.StatusMovedPermanently {
			continue // the file server sends /index.html to /
		}
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Explorer") || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("GET %s = %d %q cache %q", path, rec.Code, rec.Body, rec.Header().Get("Cache-Control"))
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("GET %s content type %q", path, rec.Header().Get("Content-Type"))
		}
	}

	rec := get("GET", "/assets/app-abc123.js")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") ||
		!strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Errorf("asset = %d %v", rec.Code, rec.Header())
	}
	if rec := get("GET", "/favicon.svg"); rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("favicon = %d %v", rec.Code, rec.Header())
	}
	// A missing asset is not answered with the page.
	if rec := get("GET", "/assets/old-000.js"); rec.Code != http.StatusNotFound {
		t.Errorf("missing asset = %d", rec.Code)
	}
	// Path traversal gets nothing.
	if rec := get("GET", "/../index.html"); rec.Code != http.StatusBadRequest {
		t.Errorf("traversal = %d", rec.Code)
	}
	if rec := get("POST", "/"); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") == "" {
		t.Errorf("POST = %d", rec.Code)
	}
	if rec := get("HEAD", "/"); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD = %d with %d bytes", rec.Code, rec.Body.Len())
	}

	// The policy allows the page's own origin and nothing else.
	for _, path := range []string{"/", "/assets/app-abc123.js", "/assets/old-000.js"} {
		csp := get("GET", path).Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") ||
			strings.Contains(csp, "unsafe") || strings.Contains(csp, "*") || strings.Contains(csp, "http") {
			t.Errorf("CSP of %s = %q", path, csp)
		}
	}
}
