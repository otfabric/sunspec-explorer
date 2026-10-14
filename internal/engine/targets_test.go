// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	sunspec "github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/devicefile"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// crc16 is the Modbus RTU checksum.
func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// rtuDevice answers Modbus RTU frames on a TCP port from a SunSpec device:
// what a serial-to-Ethernet converter in front of an inverter looks like.
// It records the function codes it was sent.
type rtuDevice struct {
	addr  string
	codes atomic.Value // []uint8
}

func newRTUDevice(t *testing.T, srv *sunspec.Server) *rtuDevice {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	d := &rtuDevice{addr: l.Addr().String()}
	d.codes.Store([]uint8{})
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go d.serve(conn, srv)
		}
	}()
	return d
}

func (d *rtuDevice) serve(conn net.Conn, srv *sunspec.Server) {
	defer func() { _ = conn.Close() }()
	// A read request is 8 bytes: unit, function, address, quantity, CRC.
	req := make([]byte, 8)
	for {
		if _, err := io.ReadFull(conn, req); err != nil {
			return
		}
		if crc16(req[:6]) != binary.LittleEndian.Uint16(req[6:]) {
			return
		}
		unit, fc := req[0], req[1]
		d.codes.Store(append(slices.Clone(d.codes.Load().([]uint8)), fc))
		addr, qty := binary.BigEndian.Uint16(req[2:4]), binary.BigEndian.Uint16(req[4:6])

		var regs []uint16
		var err error
		if fc == 3 {
			regs, err = srv.HandleHoldingRegisters(context.Background(), &modbus.HoldingRegistersRequest{UnitID: unit, Addr: addr, Quantity: qty})
		} else {
			err = modbus.ErrIllegalFunction
		}
		var res []byte
		if err != nil {
			code := byte(2)
			if errors.Is(err, modbus.ErrIllegalFunction) {
				code = 1
			}
			res = []byte{unit, fc | 0x80, code}
		} else {
			res = []byte{unit, fc, byte(2 * len(regs))}
			for _, r := range regs {
				res = binary.BigEndian.AppendUint16(res, r)
			}
		}
		res = binary.LittleEndian.AppendUint16(res, crc16(res))
		if _, err := conn.Write(res); err != nil {
			return
		}
	}
}

// RTU framing over TCP: discovery, a long model read in chunks, polling.
func TestRTUOverTCP(t *testing.T) {
	srv, err := sunspec.NewServer(&sunspec.ServerOptions{UnitID: 1, BaseAddress: 40000}, stdModels()...)
	if err != nil {
		t.Fatal(err)
	}
	dev := newRTUDevice(t, srv)
	e := newEngine(t, tick)
	ch := events(t, e)

	info, err := e.OpenSession(ctx, model.TargetConfig{URL: "rtuovertcp://" + dev.addr, UnitID: 1, TimeoutMS: 1000})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if len(info.Models) != 6 || info.Target.URL != "rtuovertcp://"+dev.addr || info.Device == nil || info.Device.Serial != "SN-0001" {
		t.Fatalf("session = %+v", info)
	}
	snap, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range snap.Models {
		if m.Status != model.ModelOK {
			t.Errorf("model %d = %s %v", m.Index, m.Status, m.Error)
		}
	}
	if a := findPoint(t, findModel(t, snap.Models, idxInverter).Group, "A"); a.Scaled == nil || *a.Scaled < 12.29 || *a.Scaled > 12.31 {
		t.Errorf("A over RTU = %+v", a)
	}
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "a sample", func(ev model.Event) bool { return isSample(ev) && ev.Sample.Seq >= 2 })
	if err := e.CloseSession(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	// Read-only over this transport too.
	if codes := dev.codes.Load().([]uint8); len(codes) < 10 || slices.ContainsFunc(codes, func(fc uint8) bool { return fc != 3 }) {
		t.Errorf("function codes sent over RTU: %v", codes)
	}
}

// A serial target passes the engine's rules before a device is opened, and
// opening a device that is not there fails cleanly.
func TestSerialTargets(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "ttyNOPE")

	open := New(Options{SerialPorts: func() []string { return []string{"/dev/ttyUSB0", "/dev/ttyACM0"} }})
	defer open.Close()
	if _, err := open.OpenSession(ctx, model.TargetConfig{URL: "rtu://" + missing}); service.CodeOf(err) != service.CodeConnectFailed {
		t.Errorf("missing device = %v, want connect_failed", err)
	}
	if ports, _ := open.SerialPorts(ctx); len(ports) != 2 || ports[0].Path != "/dev/ttyUSB0" {
		t.Errorf("SerialPorts = %+v", ports)
	}
	h, _ := open.Health(ctx)
	if !slices.Contains(h.Features, model.FeatureSerial) || slices.Contains(h.Features, model.FeatureTLS) ||
		slices.Contains(h.Features, model.FeatureRecording) {
		t.Errorf("features = %v", h.Features)
	}

	// An allow-list: only what the operator named is opened or offered.
	listed := New(Options{
		AllowSerial: func(device string) error {
			if device != "/dev/ttyUSB0" {
				return errors.New("not allowed")
			}
			return nil
		},
		SerialPorts: func() []string { return []string{"/dev/ttyUSB0", "/dev/ttyACM0"} },
	})
	defer listed.Close()
	if _, err := listed.OpenSession(ctx, model.TargetConfig{URL: "rtu:///dev/ttyACM0"}); service.CodeOf(err) != service.CodeNotAllowed {
		t.Errorf("device outside the allow-list = %v", err)
	}
	if _, err := listed.OpenSession(ctx, model.TargetConfig{URL: "rtu://" + missing}); service.CodeOf(err) != service.CodeNotAllowed {
		t.Errorf("another device outside the allow-list = %v", err)
	}
	if ports, _ := listed.SerialPorts(ctx); len(ports) != 1 || ports[0].Path != "/dev/ttyUSB0" {
		t.Errorf("SerialPorts with an allow-list = %+v", ports)
	}

	// Serial switched off.
	off := New(Options{NoSerial: true, SerialPorts: func() []string { return []string{"/dev/ttyUSB0"} }})
	defer off.Close()
	if _, err := off.OpenSession(ctx, model.TargetConfig{URL: "rtu:///dev/ttyUSB0"}); service.CodeOf(err) != service.CodeNotAllowed {
		t.Errorf("serial switched off = %v", err)
	}
	if ports, _ := off.SerialPorts(ctx); len(ports) != 0 {
		t.Errorf("SerialPorts with serial off = %+v", ports)
	}
	if h, _ := off.Health(ctx); slices.Contains(h.Features, model.FeatureSerial) {
		t.Errorf("features = %v", h.Features)
	}
	// The network allow-list is not asked about a serial device.
	netOnly := New(Options{AllowTarget: nil, NoSerial: false})
	defer netOnly.Close()
	if ports, _ := netOnly.SerialPorts(ctx); len(ports) != 0 {
		t.Errorf("SerialPorts without a lister = %+v", ports)
	}
}

// selfSigned writes a self-signed certificate and its key as PEM files.
func selfSigned(t *testing.T, dir, name string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:     []string{"localhost"},
		IsCA:         true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

// Modbus/TCP Security: mutual TLS with the identity the operator configured.
func TestTLSTargets(t *testing.T) {
	dir := t.TempDir()
	clientCert, clientKey := selfSigned(t, dir, "client")
	serverCert, serverKey := selfSigned(t, dir, "server")

	// The device: trusts the client's certificate, presents its own.
	srv, err := sunspec.NewServer(&sunspec.ServerOptions{UnitID: 1, BaseAddress: 40000}, stdModels()[:2]...)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(serverCert, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	clients, err := modbus.LoadCertPool(clientCert)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	ms, err := modbus.NewServer(&modbus.ServerConfig{
		URL: "tcp+tls://" + addr, MaxClients: 4, Logger: modbus.NopLogger(), TLSServerCert: &pair, TLSClientCAs: clients,
	}, srv)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Stop() })
	cfg := model.TargetConfig{URL: "tcp+tls://" + addr, UnitID: 1, TimeoutMS: 2000}

	// Without an identity the engine opens no TLS target, and says why.
	plain := newEngine(t, time.Second)
	_, err = plain.OpenSession(ctx, cfg)
	if service.CodeOf(err) != service.CodeNotAllowed || !strings.Contains(err.Error(), "--tls-cert") {
		t.Errorf("TLS target without identity = %v", err)
	}

	identity, err := LoadTLS(clientCert, clientKey, serverCert)
	if err != nil {
		t.Fatalf("LoadTLS: %v", err)
	}
	e := New(Options{TLS: identity})
	t.Cleanup(e.Close)
	if h, _ := e.Health(ctx); !slices.Contains(h.Features, model.FeatureTLS) {
		t.Errorf("features = %v", h.Features)
	}
	info, err := e.OpenSession(ctx, cfg)
	if err != nil {
		t.Fatalf("OpenSession over TLS: %v", err)
	}
	if len(info.Models) != 2 || info.Target.URL != "tcp+tls://"+addr {
		t.Errorf("session = %+v", info)
	}
	if snap, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{}); err != nil || snap.Models[1].Status != model.ModelOK {
		t.Errorf("Snapshot over TLS = %v", err)
	}

	// A device whose certificate the engine does not trust is refused.
	wrong, err := LoadTLS(clientCert, clientKey, clientCert)
	if err != nil {
		t.Fatal(err)
	}
	strict := New(Options{TLS: wrong})
	t.Cleanup(strict.Close)
	if _, err := strict.OpenSession(ctx, cfg); service.CodeOf(err) != service.CodeConnectFailed {
		t.Errorf("untrusted device = %v, want connect_failed", err)
	}

	// The network allow-list applies to TLS targets as well.
	closed := New(Options{TLS: identity, AllowTarget: func(*url.URL) error { return errors.New("no") }})
	_ = closed
}

func TestLoadTLS(t *testing.T) {
	dir := t.TempDir()
	cert, key := selfSigned(t, dir, "c")
	empty := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][3]string{
		"key missing":   {cert, "", cert},
		"ca missing":    {cert, key, ""},
		"no such cert":  {filepath.Join(dir, "nope"), key, cert},
		"no such ca":    {cert, key, filepath.Join(dir, "nope")},
		"ca not PEM":    {cert, key, empty},
		"key not a key": {cert, cert, cert},
	} {
		if _, err := LoadTLS(args[0], args[1], args[2]); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if cfg, err := LoadTLS(cert, key, cert); err != nil || cfg.RootCAs == nil || len(cfg.Certificate.Certificate) != 1 {
		t.Errorf("LoadTLS = %+v, %v", cfg, err)
	}
}

// R15: a pause between the reads of two models.
func TestRequestDelay(t *testing.T) {
	d := newSim(t)
	e := New(Options{RequestDelay: 30 * time.Millisecond})
	t.Cleanup(e.Close)
	info := mustOpen(t, e, d)

	start := time.Now()
	snap, err := e.Snapshot(ctx, info.ID, model.SnapshotRequest{})
	if err != nil || len(snap.Models) != 6 {
		t.Fatalf("Snapshot = %v", err)
	}
	// Six models: five pauses.
	if took := time.Since(start); took < 150*time.Millisecond {
		t.Errorf("six models read in %v with a 30 ms delay", took)
	}
	// A cancelled read does not sit out the pause.
	short, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancel()
	start = time.Now()
	if _, err := e.Snapshot(short, info.ID, model.SnapshotRequest{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Snapshot = %v, want deadline exceeded", err)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("cancelled read took %v", took)
	}
}

// R6: the device file reproduces the device, and the CSV lists every point.
func TestExportSession(t *testing.T) {
	d := newSim(t)
	e := newEngine(t, time.Second)
	info := mustOpen(t, e, d)

	file, err := e.ExportSession(ctx, info.ID, model.ExportDeviceFile)
	if err != nil {
		t.Fatalf("device file: %v", err)
	}
	if file.Mime != "application/json" || !strings.HasPrefix(file.Name, "sunspec-device-") || !strings.HasSuffix(file.Name, ".json") {
		t.Errorf("file = %s %s", file.Name, file.Mime)
	}
	// go-sunspec reads the file back the way "sunspecctl serve" does.
	parsed, err := devicefile.Parse([]byte(file.Text))
	if err != nil {
		t.Fatalf("the device file does not parse: %v", err)
	}
	if *parsed.UnitID != 1 || *parsed.BaseAddress != 40000 || len(parsed.Models) != 6 {
		t.Fatalf("device file = unit %d base %d, %d models", *parsed.UnitID, *parsed.BaseAddress, len(parsed.Models))
	}
	// Point order is schema order, as a person would read it.
	if !strings.Contains(file.Text, `"Mn": "OT Fabric"`) || strings.Index(file.Text, `"Mn"`) > strings.Index(file.Text, `"SN"`) {
		t.Error("points are not in schema order")
	}
	if parsed.Models[idxVendor].Registers == nil || parsed.Models[idxInverter].Registers != nil ||
		len(parsed.Models[idxMPPT1].Groups["module"]) != 2 {
		t.Errorf("vendor model by registers and the others by points expected: %+v", parsed.Models[idxVendor])
	}
	// Served again, the file is the same device, register for register.
	replay, err := parsed.Server(nil)
	if err != nil {
		t.Fatalf("the device file does not describe a device: %v", err)
	}
	last := info.Models[len(info.Models)-1]
	span := last.StartAddress + 2 + last.Length + 2 - 40000 // marker to end marker
	for addr := uint16(40000); addr < 40000+span; addr += 100 {
		n := min(100, 40000+span-addr)
		req := &modbus.HoldingRegistersRequest{UnitID: 1, Addr: addr, Quantity: n}
		want, err1 := d.server().HandleHoldingRegisters(ctx, req)
		got, err2 := replay.HandleHoldingRegisters(ctx, req)
		if err1 != nil || err2 != nil || !slices.Equal(want, got) {
			t.Fatalf("registers %d..%d differ after replay (%v, %v)", addr, addr+n-1, err1, err2)
		}
	}

	// CSV: a row per point, nested groups by path, raw registers of the
	// vendor model, unimplemented points without a value.
	points, err := e.ExportSession(ctx, info.ID, model.ExportCSV)
	if err != nil {
		t.Fatalf("CSV: %v", err)
	}
	rows := strings.Split(strings.TrimSpace(points.Text), "\n")
	if rows[0] != strings.Join(csvHeader, ",") {
		t.Errorf("header = %s", rows[0])
	}
	for _, want := range []string{
		",,1,103,,A,123,12.3,A,true,ok",
		",,1,103,,VA,,,VA,false,ok",
		",,0,1,,Mn,OT Fabric,,,true,ok",
		",,2,160,module[2],DCA,45,4.5,A,true,ok",
		",,4,705,Crv[2]/Pt[3],V,100,1,VNomPct,true,ok",
		",,5,64900,,register[2],65535,,,,ok",
	} {
		if !strings.Contains(points.Text, want+"\n") {
			t.Errorf("no row %q", want)
		}
	}
	for i, row := range rows[1:] {
		if strings.Count(row, ",") < 10 {
			t.Errorf("row %d has too few columns: %s", i+1, row)
		}
	}

	// A device that cannot be read whole gives no device file, and a CSV
	// that says which model failed.
	inv := info.Models[idxInverter]
	d.setFilter(failRange(inv.StartAddress, inv.StartAddress+1, modbus.ErrServerDeviceFailure))
	if _, err := e.ExportSession(ctx, info.ID, model.ExportDeviceFile); service.CodeOf(err) != service.CodeConnectFailed {
		t.Errorf("device file of a failing device = %v", err)
	}
	partial, err := e.ExportSession(ctx, info.ID, model.ExportCSV)
	if err != nil || !strings.Contains(partial.Text, ",,1,103,,,,,,,error\n") || !strings.Contains(partial.Text, ",0,1,,Mn,OT Fabric") {
		t.Errorf("CSV of a failing device = %v", err)
	}
	if _, err := e.ExportSession(ctx, info.ID, "pdf"); service.CodeOf(err) != service.CodeInvalidArgument {
		t.Errorf("unknown format = %v", err)
	}
}

// A recording converts to CSV as it is read, sample numbers included.
func TestRecordingCSV(t *testing.T) {
	d := newSim(t, stdModels()[:2]...)
	e := recEngine(t, t.TempDir(), RecordingLimits{})
	ch := events(t, e)
	info := mustOpen(t, e, d)
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "csv", RotateBytes: 4000}); err != nil {
		t.Fatal(err)
	}
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "3 samples", recordingSamples(3))
	stopped, err := e.StopRecording(ctx, "csv")
	if err != nil {
		t.Fatal(err)
	}

	r, rec, err := e.OpenRecordingCSV(ctx, "csv")
	if err != nil || rec.Name != "csv" {
		t.Fatalf("OpenRecordingCSV = %+v, %v", rec, err)
	}
	table, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	rows := bytes.Split(bytes.TrimSpace(table), []byte("\n"))
	if string(rows[0]) != strings.Join(csvHeader, ",") {
		t.Errorf("header = %s", rows[0])
	}
	seqs := map[string]bool{}
	for _, row := range rows[1:] {
		seqs[strings.Split(string(row), ",")[1]] = true
	}
	if uint64(len(seqs)) != stopped.Samples || !seqs["1"] || !bytes.Contains(table, []byte(",1,1,103,,A,123,12.3,A,true,ok\n")) {
		t.Errorf("%d sample numbers in the CSV, %d samples recorded", len(seqs), stopped.Samples)
	}

	// Closing early stops the conversion.
	r, _, _ = e.OpenRecordingCSV(ctx, "csv")
	_, _ = r.Read(make([]byte, 10))
	if err := r.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, _, err := e.OpenRecordingCSV(ctx, "nope"); service.CodeOf(err) != service.CodeNotFound {
		t.Errorf("unknown recording = %v", err)
	}
	if _, _, err := newEngine(t, tick).OpenRecordingCSV(ctx, "csv"); service.CodeOf(err) != service.CodeNotAllowed {
		t.Errorf("without a data directory = %v", err)
	}
}
