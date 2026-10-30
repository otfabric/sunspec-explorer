// SPDX-License-Identifier: MIT

package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
)

// A path of any shape gets the page, an asset or a refusal: never a hidden
// file, and the page's security policy on all of them.
func FuzzHandlerPath(f *testing.F) {
	for _, s := range []string{
		"/", "/index.html", "/assets/app.js", "/assets/none.js", "/.env", "/assets/.hidden", "/../../etc/passwd", "/assets/../.env",
		"//x", "/a%00b", "/%2e%2e/.env", "/assets", "/deep/link",
	} {
		f.Add(s)
	}
	h := Handler(fstest.MapFS{
		"index.html":     {Data: []byte("page")},
		"assets/app.js":  {Data: []byte("script")},
		".env":           {Data: []byte("hidden")},
		"assets/.hidden": {Data: []byte("hidden")},
		"favicon.svg":    {Data: []byte("icon")},
	})

	f.Fuzz(func(t *testing.T, path string) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.URL = &url.URL{Path: path}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "hidden") {
			t.Fatalf("%q served a hidden file", path)
		}
		if rec.Code >= 500 {
			t.Fatalf("%q: %d", path, rec.Code)
		}
		if rec.Header().Get("Content-Security-Policy") != ContentSecurityPolicy {
			t.Fatalf("%q has no security policy", path)
		}
	})
}
