// SPDX-License-Identifier: MIT

package readonly

import (
	"encoding/binary"
	"io"
	"net"
	"sort"
	"sync"
)

// WireTap is a TCP proxy that records the function code of every Modbus/TCP
// request passing through it. Tests put it between the engine and a device to
// prove what is on the wire, independently of the client library.
type WireTap struct {
	listener net.Listener
	upstream string

	mu    sync.Mutex
	codes map[uint8]int
	conns map[net.Conn]struct{}
	wg    sync.WaitGroup
}

// NewWireTap listens on a loopback port and forwards every connection to
// upstream (host:port). Close it when done.
func NewWireTap(upstream string) (*WireTap, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	w := &WireTap{listener: l, upstream: upstream, codes: make(map[uint8]int), conns: make(map[net.Conn]struct{})}
	w.wg.Add(1)
	go w.accept()
	return w, nil
}

// Addr returns the host:port clients connect to.
func (w *WireTap) Addr() string { return w.listener.Addr().String() }

// FunctionCodes returns the distinct function codes seen so far, ascending.
func (w *WireTap) FunctionCodes() []uint8 {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]uint8, 0, len(w.codes))
	for fc := range w.codes {
		out = append(out, fc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Requests returns the number of requests seen so far.
func (w *WireTap) Requests() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, c := range w.codes {
		n += c
	}
	return n
}

// Close stops the proxy and its connections.
func (w *WireTap) Close() {
	_ = w.listener.Close()
	w.mu.Lock()
	for c := range w.conns {
		_ = c.Close()
	}
	w.mu.Unlock()
	w.wg.Wait()
}

func (w *WireTap) track(c net.Conn) {
	w.mu.Lock()
	w.conns[c] = struct{}{}
	w.mu.Unlock()
}

func (w *WireTap) accept() {
	defer w.wg.Done()
	for {
		client, err := w.listener.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", w.upstream)
		if err != nil {
			_ = client.Close()
			continue
		}
		w.track(client)
		w.track(server)
		w.wg.Add(2)
		go func() {
			defer w.wg.Done()
			w.requests(client, server)
			_ = server.Close()
		}()
		go func() {
			defer w.wg.Done()
			_, _ = io.Copy(client, server)
			_ = client.Close()
		}()
	}
}

// requests forwards Modbus/TCP frames from the client and records them. A
// frame is the 7-byte MBAP header (transaction, protocol, length, unit)
// followed by length-1 bytes of PDU, the first of which is the function code.
func (w *WireTap) requests(client io.Reader, server io.Writer) {
	header := make([]byte, 7)
	for {
		if _, err := io.ReadFull(client, header); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(header[4:6])) - 1
		if n < 1 {
			return
		}
		pdu := make([]byte, n)
		if _, err := io.ReadFull(client, pdu); err != nil {
			return
		}
		w.mu.Lock()
		w.codes[pdu[0]]++
		w.mu.Unlock()
		if _, err := server.Write(append(header, pdu...)); err != nil {
			return
		}
	}
}
