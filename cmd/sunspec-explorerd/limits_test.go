// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The size flags are refused above the largest number of MiB whose bytes
// fit an int64, before they are converted.
func TestSizeFlagCeiling(t *testing.T) {
	over := strconv.FormatInt(maxMiB+1, 10)
	// Ended already: a daemon that took the flag would stop at once instead
	// of serving.
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	for _, flag := range []string{"--max-recording-mb", "--max-recordings-mb", "--rotate-recording-mb", "--min-free-mb"} {
		for _, v := range []string{over, "9223372036854775807"} {
			var stdout, stderr bytes.Buffer
			args := []string{"--listen", "127.0.0.1:0", flag, v, "--data-dir", filepath.Join(t.TempDir(), "d")}
			if code := run(stopped, args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "too large") {
				t.Errorf("%s %s = %d %q", flag, v, code, stderr.String())
			}
		}
	}
	// The ceiling itself is a size; the daemon starts, and stops cleanly.
	d := start(t, "--max-recordings-mb", strconv.FormatInt(maxMiB, 10))
	if err := d.stop(); err != nil {
		t.Errorf("stop = %v", err)
	}
}

// serve runs srv on a loopback port and returns its address.
func serve(t *testing.T, srv *httpServer) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return l.Addr().String()
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// A request still running when the shutdown bound ends is cut off, and the
// daemon says so: runServe returns an error, and the daemon exits with 1.
func TestShutdownTimeout(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}), context.Background(), quiet(), daemonTimeouts)
	addr := serve(t, srv)
	go func() {
		if res, err := http.Get("http://" + addr + "/"); err == nil {
			_ = res.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the request did not arrive")
	}
	if err := shutdown(srv, 20*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("shutdown with a request running = %v, want the deadline", err)
	}

	// Nothing running: a clean stop.
	idle := newServer(http.NotFoundHandler(), context.Background(), quiet(), daemonTimeouts)
	serve(t, idle)
	if err := shutdown(idle, time.Second); err != nil {
		t.Errorf("shutdown of an idle server = %v", err)
	}

	// A connection that never sent a request is waited for, as net/http
	// does, but closing it cuts nothing off.
	early := newServer(http.NotFoundHandler(), context.Background(), quiet(), daemonTimeouts)
	accepted := make(chan struct{})
	var once sync.Once
	early.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			once.Do(func() { close(accepted) })
		}
	}
	conn, err := net.Dial("tcp", serve(t, early))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	select {
	case <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("the connection was not accepted")
	}
	if err := shutdown(early, 20*time.Millisecond); err != nil {
		t.Errorf("shutdown with a connection that sent no request = %v", err)
	}
}

// A connection that carries no request for the idle timeout is closed by
// the server.
func TestServerClosesIdleConnections(t *testing.T) {
	srv := newServer(http.NotFoundHandler(), context.Background(), quiet(),
		serverTimeouts{readHeader: daemonTimeouts.readHeader, idle: 100 * time.Millisecond})
	conn, err := net.Dial("tcp", serve(t, srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// The test's own bound: far beyond the idle timeout.
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(conn)
	res, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound || res.Close {
		t.Fatalf("response = %d, close %v; want a 404 on a connection kept open", res.StatusCode, res.Close)
	}
	if n, err := r.Read(make([]byte, 1)); n != 0 || !closedByPeer(err) {
		t.Errorf("idle connection read = %d, %v; want it closed by the server", n, err)
	}
}

// closedByPeer reports whether a read error says that the other end closed
// the connection (EOF, or a reset on some systems), not that the test's own
// deadline passed.
func closedByPeer(err error) bool {
	var ne net.Error
	return err != nil && (!errors.As(err, &ne) || !ne.Timeout())
}
