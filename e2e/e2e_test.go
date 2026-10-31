// SPDX-License-Identifier: MIT

// Package e2e builds sunspec-explorerd and runs it as a process, the way it
// runs on a gateway: a simulated SunSpec device on loopback TCP, the HTTP API
// with the token from "sunspec-explorerd url", and shutdown by signal.
package e2e

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	sunspec "github.com/otfabric/go-sunspec"

	"github.com/otfabric/sunspec-explorer/internal/model"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sunspec-explorerd-e2e-")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "sunspec-explorerd")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/sunspec-explorerd").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// device serves a Common and an inverter model on a free loopback port.
func device(t *testing.T) string {
	t.Helper()
	common := (&sunspec.Values{}).Set("Mn", "OT Fabric").Set("Md", "E2E").Set("Vr", "1.0").Set("SN", "SN-9").Set("DA", 1)
	inverter := (&sunspec.Values{}).Set("A_SF", -1).Set("A", 12.3).Set("W_SF", 1).Set("W", 4200).Set("St", "MPPT")
	srv, err := sunspec.NewServer(&sunspec.ServerOptions{UnitID: 1, BaseAddress: 40000},
		sunspec.ServerModel{ID: 1, Values: common}, sunspec.ServerModel{ID: 103, Values: inverter})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	ms, err := modbus.NewServer(&modbus.ServerConfig{URL: "tcp://" + addr, Logger: modbus.NopLogger()}, srv)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Stop() })
	return addr
}

type daemon struct {
	cmd    *exec.Cmd
	base   string
	token  string
	dir    string
	stderr *bytes.Buffer
}

var urlLine = regexp.MustCompile(`http://(127\.0\.0\.1:\d+)/#token=([A-Za-z0-9_-]+)`)

// run starts the daemon and reads its URL as a user does, from the url command.
func run(t *testing.T) *daemon {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	d := &daemon{dir: dir, stderr: &bytes.Buffer{}}
	d.cmd = exec.Command(bin, "--listen", "127.0.0.1:0", "--data-dir", dir, "--allow-target", "127.0.0.1")
	d.cmd.Stderr = d.stderr
	if err := d.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.cmd.Process.Kill()
		_ = d.cmd.Wait()
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		out, err := exec.Command(bin, "url", "--data-dir", dir).Output()
		if m := urlLine.FindSubmatch(out); err == nil && m != nil {
			d.base, d.token = "http://"+string(m[1]), string(m[2])
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("no URL from the daemon: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (d *daemon) call(t *testing.T, method, path, body, token string, out any) int {
	t.Helper()
	req, err := http.NewRequest(method, d.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if out != nil && res.StatusCode < 300 {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return res.StatusCode
}

// TestDiscoverAndRead is the whole path: authenticated, a session on a real
// connection, discovery, a snapshot, close.
func TestDiscoverAndRead(t *testing.T) {
	dev := device(t)
	d := run(t)
	if got := d.call(t, "GET", "/api/v1/health", "", "", nil); got != http.StatusUnauthorized {
		t.Fatalf("health without the token: %d", got)
	}
	if got := d.call(t, "GET", "/api/v1/health", "", d.token, nil); got != http.StatusOK {
		t.Fatalf("health: %d", got)
	}
	var s model.SessionInfo
	if got := d.call(t, "POST", "/api/v1/sessions", `{"url":"tcp://`+dev+`","unit_id":1}`, d.token, &s); got != http.StatusCreated {
		t.Fatalf("open session: %d\n%s", got, d.stderr)
	}
	if s.Device == nil || s.Device.Model != "E2E" || s.Device.Manufacturer != "OT Fabric" {
		t.Fatalf("device %+v", s.Device)
	}
	var ids []uint16
	for _, m := range s.Models {
		ids = append(ids, m.ID)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 103 {
		t.Fatalf("models %v", ids)
	}
	var snap json.RawMessage
	if got := d.call(t, "POST", "/api/v1/sessions/"+s.ID+"/snapshot", `{}`, d.token, &snap); got != http.StatusOK {
		t.Fatalf("snapshot: %d", got)
	}
	if !bytes.Contains(snap, []byte("4200")) && !bytes.Contains(snap, []byte("4.2")) {
		t.Errorf("the snapshot lacks the inverter's power: %s", snap)
	}
	if got := d.call(t, "DELETE", "/api/v1/sessions/"+s.ID, "", d.token, nil); got >= 300 {
		t.Errorf("close session: %d", got)
	}
}

// TestRefusedTargets checks the daemon's target allow-list from outside: the
// process only connects where --allow-target says.
func TestRefusedTargets(t *testing.T) {
	d := run(t)
	if got := d.call(t, "POST", "/api/v1/sessions", `{"url":"tcp://8.8.8.8:502","unit_id":1}`, d.token, nil); got < 400 {
		t.Errorf("a public address was accepted: %d", got)
	}
	if got := d.call(t, "POST", "/api/v1/sessions", `{"url":`, d.token, nil); got != http.StatusBadRequest {
		t.Errorf("broken JSON: %d", got)
	}
}

// TestSignalStops checks a clean exit on SIGTERM, as from systemd.
func TestSignalStops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no SIGTERM on Windows")
	}
	d := run(t)
	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- d.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit after SIGTERM: %v\n%s", err, d.stderr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon did not stop")
	}
}
