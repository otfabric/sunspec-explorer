// SPDX-License-Identifier: MIT

package api_test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/sunspec-explorer/frontend"
	"github.com/otfabric/sunspec-explorer/internal/api"
	"github.com/otfabric/sunspec-explorer/internal/engine"
	"github.com/otfabric/sunspec-explorer/internal/web"
)

// A15: the web build's own code (token handling, fetch client, event stream,
// the React application) runs against a real daemon with a simulated device:
// this test is the daemon "in test mode", and it runs the frontend's smoke
// suite against itself. It needs Node and the frontend's dependencies and is
// skipped without them.
func TestWebUISmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("runs npm")
	}
	dir, err := filepath.Abs(filepath.Join("..", "..", "frontend"))
	if err != nil {
		t.Fatal(err)
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Skip("npm is not installed")
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err != nil {
		t.Skip(`the frontend's dependencies are not installed: run "make frontend-install"`)
	}

	dev := newSim(t)
	opts := api.Options{}
	built := frontend.Built(frontend.Web())
	if built {
		opts.UI = web.Handler(frontend.Web())
	}
	d := newDaemon(t, engine.Options{DataDir: t.TempDir(), DataDirTemporary: true}, opts)

	// The daemon serves the built page under its policy, without a token.
	if built {
		status, header, body := d.request(t, "GET", "/", "", "", nil)
		if status != http.StatusOK || !strings.Contains(body, "<title>SunSpec Explorer</title>") ||
			header.Get("Content-Security-Policy") != web.ContentSecurityPolicy || strings.Contains(strings.ToLower(body), "wails") {
			t.Errorf("/ = %d %v", status, header)
		}
		if strings.Contains(body, "<script>") || strings.Contains(body, "<style") {
			t.Error("the page has inline script or style, which the policy forbids")
		}
	} else {
		t.Log(`no web build embedded (run "make frontend"): checking the page's code only`)
	}

	cmd := exec.Command(npm, "run", "test:smoke")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"EXPLORER_URL="+d.srv.URL,
		"EXPLORER_TOKEN="+testToken,
		"EXPLORER_TARGET=tcp://"+dev.addr,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("frontend smoke suite failed: %v\n%s", err, out)
	}
	if list, _ := d.svc.Sessions(ctx); len(list) != 0 {
		t.Errorf("the smoke suite left %d sessions open", len(list))
	}
}
