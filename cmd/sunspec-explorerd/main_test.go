// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/daemon"
)

// syncBuffer is a bytes.Buffer the daemon and the test may use at once.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// started is a daemon running in this process.
type started struct {
	dir    string
	addr   string
	stderr *syncBuffer
	stop   func() error
}

func start(t *testing.T, args ...string) *started {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan error, 1)
	stderr := &syncBuffer{}
	go func() {
		done <- runServe(ctx, append([]string{"--listen", "127.0.0.1:0", "--data-dir", dir}, args...), stderr, ready)
	}()
	s := &started{dir: dir, stderr: stderr}
	var once sync.Once
	var stopErr error
	s.stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case stopErr = <-done:
			case <-time.After(10 * time.Second):
				t.Error("daemon did not stop")
			}
		})
		return stopErr
	}
	t.Cleanup(func() { _ = s.stop() })
	select {
	case s.addr = <-ready:
	case err := <-done:
		t.Fatalf("daemon did not start: %v\n%s", err, stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not start in time")
	}
	return s
}

func status(t *testing.T, addr, path, token string) int {
	t.Helper()
	req, err := http.NewRequest("GET", "http://"+addr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

var urlLine = regexp.MustCompile(`^http://127\.0\.0\.1:(\d+)/#token=([A-Za-z0-9_-]{40,})\n$`)

// A17: the url command prints the paste-ready URL of the running daemon with
// the port it really bound; the token survives restarts, rotates without
// one, and shows up nowhere but in the startup line and the url command.
func TestURLAndTokenLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
	d := start(t)
	tokenFile := filepath.Join(d.dir, daemon.TokenFileName)

	// Data directory 0700, token 0600 (A16).
	for path, want := range map[string]os.FileMode{d.dir: 0o700, tokenFile: 0o600, filepath.Join(d.dir, daemon.StateFileName): 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: mode %v, %v; want %04o", path, info.Mode().Perm(), err, want)
		}
	}

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"url", "--data-dir", d.dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("url = %d: %s", code, stderr.String())
	}
	m := urlLine.FindStringSubmatch(stdout.String())
	if m == nil {
		t.Fatalf("url printed %q", stdout.String())
	}
	token := m[2]
	if "127.0.0.1:"+m[1] != d.addr {
		t.Errorf("url names port %s, the daemon bound %s", m[1], d.addr)
	}
	if !strings.Contains(stderr.String(), "ssh -N -L "+m[1]+":127.0.0.1:"+m[1]+" <user>@") {
		t.Errorf("no ssh suggestion: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), token) {
		t.Error("the token is repeated on standard error")
	}

	if got := status(t, d.addr, "/api/v1/health", token); got != http.StatusOK {
		t.Fatalf("health with the token = %d", got)
	}
	if got := status(t, d.addr, "/api/v1/health", ""); got != http.StatusUnauthorized {
		t.Errorf("health without the token = %d", got)
	}

	// This daemon does not write to a terminal, like one under systemd or in
	// a container: the token is in none of its output, which names the
	// command that shows it instead. Requests, also failed ones, do not
	// change that.
	logs := d.stderr.String()
	if strings.Contains(logs, token) || strings.Contains(logs, "#token=") || !strings.Contains(logs, `Run "sunspec-explorerd url"`) {
		t.Errorf("the daemon's output:\n%s", logs)
	}

	// Rotation: the old token stops working, the new one works, no restart.
	stdout.Reset()
	if code := run(context.Background(), []string{"token", "rotate", "--data-dir", d.dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("rotate = %d: %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), token) || !strings.Contains(stdout.String(), "rotated") {
		t.Errorf("rotate printed %q", stdout.String())
	}
	stdout.Reset()
	if code := run(context.Background(), []string{"url", "--data-dir", d.dir}, &stdout, &stderr); code != 0 {
		t.Fatal("url after rotation failed")
	}
	rotated := urlLine.FindStringSubmatch(stdout.String())
	if rotated == nil || rotated[2] == token {
		t.Fatalf("url after rotation printed %q", stdout.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for status(t, d.addr, "/api/v1/health", rotated[2]) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("the running daemon did not pick up the rotated token")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := status(t, d.addr, "/api/v1/health", token); got != http.StatusUnauthorized {
		t.Errorf("old token after rotation = %d", got)
	}
	if strings.Contains(d.stderr.String(), rotated[2]) {
		t.Error("the rotated token was logged")
	}

	// A clean stop removes the state file; url then says so.
	if err := d.stop(); err != nil {
		t.Errorf("stop: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"url", "--data-dir", d.dir}, &stdout, &stderr); code == 0 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "no daemon is running") {
		t.Errorf("url without a daemon = %d %q %q", code, stdout.String(), stderr.String())
	}

	// A restart with the same data directory keeps the token.
	again := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	log := &syncBuffer{}
	go func() { done <- runServe(ctx, []string{"--listen", "127.0.0.1:0", "--data-dir", d.dir}, log, again) }()
	addr := <-again
	if got := status(t, addr, "/api/v1/health", rotated[2]); got != http.StatusOK {
		t.Errorf("token after restart = %d", got)
	}
	if !strings.Contains(log.String(), "new_token=false") {
		t.Errorf("restart log: %s", log.String())
	}
	cancel()
	<-done
}

// The URL with the token is shown to a person at a terminal, and to nobody
// else.
func TestAnnounce(t *testing.T) {
	access := daemon.Access{URL: "http://127.0.0.1:8765/#token=secret-token", SSH: "ssh -N -L 8765:127.0.0.1:8765 <user>@gw"}

	var terminal bytes.Buffer
	announce(&terminal, true, access)
	if terminal.String() != "Open "+access.URL+"\nFrom a workstation, first run: "+access.SSH+"\n" {
		t.Errorf("at a terminal: %q", terminal.String())
	}
	terminal.Reset()
	announce(&terminal, true, daemon.Access{URL: access.URL})
	if strings.Contains(terminal.String(), "ssh") {
		t.Errorf("ssh hint without a command: %q", terminal.String())
	}

	var journal bytes.Buffer
	announce(&journal, false, access)
	if strings.Contains(journal.String(), "secret-token") || strings.Contains(journal.String(), "token=") ||
		!strings.Contains(journal.String(), "sunspec-explorerd url") {
		t.Errorf("not at a terminal: %q", journal.String())
	}

	// A buffer, a pipe and a regular file are not terminals.
	file, err := os.Create(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	if isTerminal(&journal) || isTerminal(file) || isTerminal(w) {
		t.Error("a buffer, a file or a pipe was taken for a terminal")
	}
}

// Serial devices, TLS identity and pacing come from the operator's flags.
func TestTargetFlags(t *testing.T) {
	d := start(t, "--allow-serial", "/dev/ttyUSB*", "--allow-serial", "/dev/ttyACM0", "--request-delay", "20ms")
	token, err := daemon.ReadToken(filepath.Join(d.dir, daemon.TokenFileName))
	if err != nil {
		t.Fatal(err)
	}
	get := func(addr, path string) string {
		req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	if health := get(d.addr, "/api/v1/health"); !strings.Contains(health, `"serial"`) || !strings.Contains(health, `"recording"`) ||
		strings.Contains(health, `"tls"`) {
		t.Errorf("health = %s", health)
	}

	// Without --allow-serial the daemon opens no serial device.
	var stdout, stderr bytes.Buffer
	plain := start(t)
	plainToken, _ := daemon.ReadToken(filepath.Join(plain.dir, daemon.TokenFileName))
	token = plainToken
	if health := get(plain.addr, "/api/v1/health"); strings.Contains(health, `"serial"`) {
		t.Errorf("health without --allow-serial = %s", health)
	}

	for _, args := range [][]string{
		{"--allow-serial", "[", "--data-dir", filepath.Join(t.TempDir(), "d")},
		{"--tls-cert", "only-one.pem", "--data-dir", filepath.Join(t.TempDir(), "d")},
		{"--tls-cert", "a", "--tls-key", "b", "--tls-ca", "c", "--data-dir", filepath.Join(t.TempDir(), "d")},
		{"--request-delay", "1m", "--data-dir", filepath.Join(t.TempDir(), "d")},
		{"--max-recording-mb", "0", "--data-dir", filepath.Join(t.TempDir(), "d")},
		{"--min-free-mb", "-1", "--data-dir", filepath.Join(t.TempDir(), "d")},
	} {
		stderr.Reset()
		if code := run(context.Background(), append([]string{"--listen", "127.0.0.1:0"}, args...), &stdout, &stderr); code != 1 || stderr.Len() == 0 {
			t.Errorf("%v = %d %q", args, code, stderr.String())
		}
	}
}

func TestStaleStateFile(t *testing.T) {
	dir := t.TempDir()
	if err := daemon.WriteState(dir, daemon.State{Listen: "127.0.0.1:1", PID: 1}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"url", "--data-dir", dir}, &stdout, &stderr); code == 0 ||
		!strings.Contains(stderr.String(), "no daemon answers") {
		t.Errorf("url with a stale state file = %d %q", code, stderr.String())
	}
}

// The UI is served without a token, the API is not; --no-ui serves no page.
func TestServesUI(t *testing.T) {
	d := start(t)
	res, err := http.Get("http://" + d.addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if strings.Contains(d.stderr.String(), "built without the web UI") {
		// A checkout without "make frontend": the API alone.
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("/ without a UI build = %d", res.StatusCode)
		}
	} else if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Security-Policy"), "default-src 'self'") ||
		res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("/ = %d %v", res.StatusCode, res.Header)
	}

	bare := start(t, "--no-ui")
	if got := status(t, bare.addr, "/", ""); got != http.StatusNotFound {
		t.Errorf("/ with --no-ui = %d", got)
	}
	if got := status(t, bare.addr, "/api/v1/health", ""); got != http.StatusUnauthorized {
		t.Errorf("API with --no-ui = %d", got)
	}
}

// Loopback by default; anything else needs the flag and gets a warning, and
// the token and the Host check stay.
func TestBindRules(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dir := filepath.Join(t.TempDir(), "data")
	for _, listen := range []string{"0.0.0.0:0", ":0", "192.0.2.1:8765"} {
		stderr.Reset()
		code := run(context.Background(), []string{"--listen", listen, "--data-dir", dir}, &stdout, &stderr)
		if code == 0 || !strings.Contains(stderr.String(), "--allow-remote-bind") {
			t.Errorf("--listen %s without the flag = %d %q", listen, code, stderr.String())
		}
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a refused start created the data directory")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	done := make(chan error, 1)
	log := &syncBuffer{}
	go func() {
		done <- runServe(ctx, []string{"--listen", "0.0.0.0:0", "--allow-remote-bind", "--data-dir", dir}, log, ready)
	}()
	var addr string
	select {
	case addr = <-ready:
	case err := <-done:
		t.Fatalf("remote bind: %v", err)
	}
	if !strings.Contains(log.String(), "level=WARN") || !strings.Contains(log.String(), "not encrypted") {
		t.Errorf("no warning for a remote bind:\n%s", log.String())
	}
	_, port, _ := strings.Cut(addr, "]:")
	local := "127.0.0.1:" + port
	if got := status(t, local, "/api/v1/health", ""); got != http.StatusUnauthorized {
		t.Errorf("remote bind without token = %d", got)
	}
	req, _ := http.NewRequest("GET", "http://"+local+"/api/v1/health", nil)
	req.Host = "gateway.example"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("remote bind with a foreign Host = %d", res.StatusCode)
	}
	cancel()
	<-done
}

func TestCommandLine(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"version"}, &stdout, &stderr); code != 0 || !strings.HasPrefix(stdout.String(), "sunspec-explorerd ") {
		t.Errorf("version = %d %q", code, stdout.String())
	}
	if code := run(context.Background(), []string{"--help"}, &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "ssh -N -L") {
		t.Errorf("--help = %d %q", code, stderr.String())
	}
	for _, args := range [][]string{
		{"token"}, {"token", "show"}, {"serve"}, {"--no-such-flag"}, {"url", "--no-such-flag"}, {"token", "rotate", "--no-such-flag"},
		{"--allow-target", "nonsense", "--data-dir", filepath.Join(t.TempDir(), "d")},
		{"--listen", "nonsense"},
		{"--data-dir", filepath.Join(t.TempDir(), "missing", "deep")},
		{"token", "rotate", "--data-dir", filepath.Join(t.TempDir(), "missing", "deep")},
	} {
		stderr.Reset()
		if code := run(context.Background(), args, &stdout, &stderr); code != 1 || stderr.Len() == 0 {
			t.Errorf("%v = %d %q", args, code, stderr.String())
		}
	}
}

// A16: the shipped unit must not give the service a private /tmp: systemd
// deletes it when the service stops, and the recordings with it.
func TestSystemdUnit(t *testing.T) {
	unit, err := os.ReadFile(filepath.Join("..", "..", "packaging", "sunspec-explorerd.service"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(unit), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "PrivateTmp") {
			t.Errorf("the unit sets %s", line)
		}
		if strings.HasPrefix(line, "ExecStart=") && (!strings.Contains(line, "--listen 127.0.0.1:") || strings.Contains(line, "allow-remote-bind")) {
			t.Errorf("the unit does not bind loopback: %s", line)
		}
	}
	if !strings.Contains(string(unit), "ExecStart=/usr/local/bin/sunspec-explorerd ") {
		t.Error("no ExecStart")
	}
}

// A12: the daemon links no Wails package and nothing that needs cgo.
func TestNoWailsNoCgo(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}} {{if .CgoFiles}}cgo{{end}}", ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=arm", "GOARM=7", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.Contains(line, "wailsapp") || strings.Contains(line, "internal/gui") {
			t.Errorf("the daemon depends on %s", line)
		}
	}
	if !strings.Contains(string(out), "sunspec-explorer/internal/engine") {
		t.Errorf("unexpected dependency list:\n%.300s", out)
	}
}
