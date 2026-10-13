// SPDX-License-Identifier: MIT

// Package web serves the web build of the user interface from the daemon.
// It must not import Wails.
//
// The assets hold no secret, so they need no token: the page gets the token
// from the URL fragment, which a browser never sends to a server.
package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// ContentSecurityPolicy lets the page load and call its own origin and
// nothing else: no inline script or style, no other host, no framing.
const ContentSecurityPolicy = "default-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// Handler serves assets, a file system with index.html at its root.
//
// A path that names no file gets index.html, so that the page can be
// reloaded anywhere. index.html is never cached; files under assets/ carry a
// content hash in their name and are cached for good.
func Handler(assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Security-Policy", ContentSecurityPolicy)

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "index.html" || !isFile(assets, name) {
			// A missing asset is an error, not a page.
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, assets, "index.html")
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func isFile(assets fs.FS, name string) bool {
	info, err := fs.Stat(assets, name)
	return err == nil && !info.IsDir() && !strings.HasPrefix(path.Base(name), ".")
}
