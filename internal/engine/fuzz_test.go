// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	sunspec "github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/devicefile"
	"github.com/otfabric/go-sunspec/registry"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// A target the API accepted is what the engine stores, shows and dials, so it
// must mean the same when read back: its display form is accepted again, is
// stable, and carries neither credentials nor anything but scheme, host and
// port (or a device). A refused target is an invalid_argument, never anything
// else, and go-modbus must accept what the engine accepted.
func FuzzParseTarget(f *testing.F) {
	for _, s := range []string{
		"tcp://192.168.1.10:502", "tcp://inverter", "tcp://[::1]:1502", "tcp://[fe80::1%25eth0]:502",
		"rtuovertcp://10.0.0.1:502", "tcp+tls://device.local:802", "rtu:///dev/ttyUSB0", "rtu://COM3",
		"tcp://user:secret@host:502", "tcp://host:99999", "tcp://host/path?q#f", "udp://x", "://", "",
		" tcp://h ", "tcp://%41:1", "rtu://a b", "tcp://[::1", "tcp://1.2.3.4:0",
	} {
		f.Add(s, uint8(1), "holding", uint16(0), 0, []byte(nil))
	}
	f.Add("rtu:///dev/ttyS0", uint8(255), "input", uint16(3000), 16, []byte{0x9c, 0x40})
	f.Add("tcp://h", uint8(248), "other", uint16(1), -1, []byte{0, 0, 0xc3, 0x50})

	f.Fuzz(func(t *testing.T, raw string, unit uint8, regType string, timeoutMS uint16, maxModels int, bases []byte) {
		cfg := model.TargetConfig{URL: raw, UnitID: unit, RegType: regType, TimeoutMS: int(timeoutMS), MaxModels: maxModels}
		for b := bases; len(b) >= 2; b = b[2:] {
			cfg.BaseAddresses = append(cfg.BaseAddresses, binary.BigEndian.Uint16(b))
		}
		tgt, err := parseTarget(cfg)
		if err != nil {
			if service.CodeOf(err) != service.CodeInvalidArgument {
				t.Fatalf("parseTarget(%q) = %v, want invalid_argument", raw, err)
			}
			return
		}
		if tgt.display == "" || tgt.unitID == 0 || (tgt.unitID > 247 && tgt.unitID != 255) {
			t.Fatalf("parseTarget(%q) = %+v", raw, tgt)
		}
		cfg.URL = tgt.display
		again, err := parseTarget(cfg)
		if err != nil {
			t.Fatalf("display form %q of %q is refused: %v", tgt.display, raw, err)
		}
		if again.display != tgt.display {
			t.Fatalf("display form of %q is not stable: %q then %q", raw, tgt.display, again.display)
		}
		if tgt.scheme == schemeRTU {
			return
		}
		u, err := url.Parse(tgt.display)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			t.Fatalf("display form %q of %q holds more than a host: %+v, %v", tgt.display, raw, u, err)
		}
		// The allow-list looks at t.url and the client dials display: they
		// must name the same host.
		if u.Hostname() != tgt.url.Hostname() || u.Port() != tgt.url.Port() {
			t.Fatalf("display %q and allow-list URL %v name different hosts", tgt.display, tgt.url)
		}
		conf := modbus.Config{URL: tgt.display, Timeout: time.Second}
		if tgt.scheme == schemeTLS {
			conf.TLSClientCert, conf.TLSRootCAs = &tls.Certificate{}, x509.NewCertPool()
		}
		if _, err := modbus.New(conf); err != nil {
			t.Fatalf("go-modbus refuses %q, which the engine accepted: %v", tgt.display, err)
		}
	})
}

// modelIDs lists the registry's model IDs in a fixed order, for FuzzDecodeModel
// to pick from.
var modelIDs = sync.OnceValue(func() []uint16 {
	ids := make([]uint16, 0, registry.Count())
	for id := range registry.All() {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
})

func registersOf(b []byte) []uint16 {
	regs := make([]uint16, len(b)/2)
	for i := range regs {
		regs[i] = binary.BigEndian.Uint16(b[2*i:])
	}
	return regs
}

// Any register values a device sends for any model in the registry decode to
// a view that JSON and CSV can carry: nothing a device says stops a recording
// or an event from being written. Every CSV row has every column.
func FuzzDecodeModel(f *testing.F) {
	indexOf := func(id uint16) uint16 {
		i, _ := slices.BinarySearch(modelIDs(), id)
		return uint16(i)
	}
	for _, id := range []uint16{1, 103, 160, 705} {
		meta := registry.ByID(id)
		for _, fill := range []byte{0x00, 0xFF, 0x80, 0x7F} {
			regs := bytes.Repeat([]byte{fill}, 2*(2+meta.FixedLength()+40))
			binary.BigEndian.PutUint16(regs, id)
			f.Add(indexOf(id), regs)
		}
		f.Add(indexOf(id), []byte{0, byte(id)})
	}

	f.Fuzz(func(t *testing.T, pick uint16, data []byte) {
		ids := modelIDs()
		meta := registry.ByID(ids[int(pick)%len(ids)])
		dm, err := sunspec.DecodeModel(registersOf(data), meta, 40002)
		if err != nil && !errors.Is(err, sunspec.ErrDecode) {
			t.Fatalf("DecodeModel: %v", err)
		}
		if dm == nil || dm.Group == nil {
			return
		}
		for _, full := range []bool{true, false} {
			view := groupView(dm.Group, schemaGroup(dm.Schema), 40002, full)
			if _, err := json.Marshal(view); err != nil {
				t.Fatalf("the view of model %d cannot be encoded: %v", meta.ID, err)
			}
			checkCSV(t, []model.ModelResult{{Index: 0, ID: meta.ID, Status: model.ModelOK, Group: view}})
		}
	})
}

// checkCSV writes the rows of models and reads them back.
func checkCSV(t *testing.T, models []model.ModelResult) {
	t.Helper()
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	writeCSVRows(w, time.Unix(0, 0).UTC(), "1", models)
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatalf("CSV: %v", err)
	}
	checkCSVText(t, buf.Bytes(), false)
}

// checkCSVText reads CSV back: every row must have every column.
func checkCSVText(t *testing.T, text []byte, header bool) {
	t.Helper()
	r := csv.NewReader(bytes.NewReader(text))
	r.FieldsPerRecord = len(csvHeader)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("the CSV does not read back: %v\n%.300s", err, text)
	}
	if header && (len(rows) == 0 || !slices.Equal(rows[0], csvHeader)) {
		t.Fatalf("the CSV has no header: %.200s", text)
	}
}

// imageDevice is the least a Modbus/TCP device can be: FC03 answered from a
// register image at address 40000, and "illegal data address" elsewhere. The
// image can be replaced between requests. Unlike go-sunspec's Server it will
// hold registers that make no sense, which is what the fuzz target needs.
type imageDevice struct {
	listener net.Listener
	image    atomic.Pointer[[]uint16]
}

const imageBase = 40000

func newImageDevice(t testing.TB) *imageDevice {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &imageDevice{listener: l}
	empty := []uint16{}
	d.image.Store(&empty)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go d.serve(c)
		}
	}()
	return d
}

func (d *imageDevice) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	for {
		req := make([]byte, 12)
		if _, err := io.ReadFull(c, req); err != nil {
			return
		}
		addr, n := int(binary.BigEndian.Uint16(req[8:])), int(binary.BigEndian.Uint16(req[10:]))
		image := *d.image.Load()
		var pdu []byte
		if req[7] != 3 || addr < imageBase || addr+n > imageBase+len(image) {
			pdu = []byte{req[7] | 0x80, 2}
		} else {
			pdu = append(make([]byte, 0, 2+2*n), 3, byte(2*n))
			for _, r := range image[addr-imageBase : addr-imageBase+n] {
				pdu = binary.BigEndian.AppendUint16(pdu, r)
			}
		}
		res := append(req[:4:4], 0, byte(len(pdu)+1), req[6])
		if _, err := c.Write(append(res, pdu...)); err != nil {
			return
		}
	}
}

// imageOf reads the whole chain of a simulated device, to seed the fuzzer with
// something that gets past the marker.
func imageOf(t testing.TB, models ...sunspec.ServerModel) []byte {
	t.Helper()
	srv, err := sunspec.NewServer(&sunspec.ServerOptions{UnitID: 1, BaseAddress: imageBase}, models...)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for addr := uint16(imageBase); ; addr += 50 {
		regs, err := srv.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, Addr: addr, Quantity: 50})
		if err != nil {
			break
		}
		for _, r := range regs {
			out = binary.BigEndian.AppendUint16(out, r)
		}
	}
	for addr := uint16(imageBase + len(out)/2); ; addr++ {
		regs, err := srv.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, Addr: addr, Quantity: 1})
		if err != nil {
			return out
		}
		out = binary.BigEndian.AppendUint16(out, regs[0])
	}
}

// The register image of a device, read over Modbus/TCP by the engine. Whatever
// a device holds, discovery fails cleanly or finds a chain that can be read:
// the snapshot validates against the published schema, the point CSV has every
// column, and a device file, when one comes out, is one go-sunspec can load.
//
// One session lives through all images of a worker, so that a run costs no
// connection; a failed discovery ends the run, because the session then keeps
// the previous image's result.
func FuzzDeviceRegisters(f *testing.F) {
	std := imageOf(f, stdModels()...)
	f.Add(std)
	f.Add(std[:len(std)/2])
	f.Add([]byte("SunS"))
	f.Add([]byte("SunS\x00\x01\x00\x42"))
	f.Add([]byte("SunS\xff\xff\x00\x00"))
	f.Add(append([]byte("SunS\x00\x01\xff\xff"), make([]byte, 64)...))

	dev := newImageDevice(f)
	f.Cleanup(func() { _ = dev.listener.Close() })
	image := registersOf(std)
	dev.image.Store(&image)
	eng := New(Options{})
	f.Cleanup(eng.Close)
	info, err := eng.OpenSession(ctx, model.TargetConfig{
		URL: "tcp://" + dev.listener.Addr().String(), UnitID: 1, TimeoutMS: 2000, BaseAddresses: []uint16{imageBase},
	})
	if err != nil {
		f.Fatalf("OpenSession: %v", err)
	}
	id := info.ID
	schema := newSchemas(f, snapshotSchemaFile).def("")

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 8192 {
			data = data[:8192]
		}
		image := registersOf(data)
		dev.image.Store(&image)

		info, err := eng.Rediscover(ctx, id)
		if err != nil {
			switch code := service.CodeOf(err); code {
			case service.CodeNotSunSpec, service.CodeDiscoveryFailed:
			default:
				t.Fatalf("Rediscover failed with %s: %v", code, err)
			}
			return
		}
		snap, err := eng.Snapshot(ctx, id, model.SnapshotRequest{IncludeRaw: true})
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		if len(snap.Models) > len(info.Models) {
			t.Fatalf("%d results for %d models", len(snap.Models), len(info.Models))
		}
		doc, err := json.Marshal(snap)
		if err != nil {
			t.Fatalf("the snapshot cannot be encoded: %v", err)
		}
		if err := validate(t, schema, doc); err != nil {
			t.Fatalf("the snapshot does not match the schema: %v\n%.500s", err, doc)
		}
		export, err := eng.ExportSession(ctx, id, model.ExportCSV)
		if err != nil {
			t.Fatalf("CSV export: %v", err)
		}
		checkCSVText(t, []byte(export.Text), true)
		export, err = eng.ExportSession(ctx, id, model.ExportDeviceFile)
		if err != nil {
			if service.CodeOf(err) != service.CodeConnectFailed {
				t.Fatalf("device file: %v", err)
			}
			return
		}
		if _, err := devicefile.Parse([]byte(export.Text)); err != nil {
			t.Fatalf("the device file does not load: %v\n%.500s", err, export.Text)
		}
	})
}

// storeWith writes a sidecar and a first file and opens the store over them.
func storeWith(t *testing.T, meta, lines []byte) (*recStore, string) {
	t.Helper()
	data := t.TempDir()
	dir := filepath.Join(data, recordingsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	const name = "fuzz"
	if err := os.WriteFile(filepath.Join(dir, name+metaSuffix), meta, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".000.jsonl"), lines, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := newRecStore(data, true, RecordingLimits{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("a recording must not stop the store from opening: %v", err)
	}
	return st, name
}

const (
	seedHeader = `{"type":"header","schema":"sunspec-explorer/recording/1","name":"fuzz"}`
	seedSample = `{"type":"sample","session_id":"s","seq":1,"captured_at":"2026-10-30T00:00:00Z","models":[` +
		`{"index":1,"id":103,"status":"ok","group":{"name":"inverter","points":[{"name":"A","raw":123,"implemented":true}],` +
		`"groups":[{"name":"g","index":1,"points":[{"name":"s","raw":"a,b\n\"c\"","implemented":true},{"name":"r","raw":[1,2],"implemented":true}]}]}},` +
		`{"index":2,"id":64900,"status":"ok","raw_registers":[1,2]},{"index":3,"id":160,"status":"error","error":"x"}]}`
	seedFooter = `{"type":"footer","samples":1,"failures":1,"reason":"stopped"}`
)

// What a previous run left in the data directory: a sidecar that says anything
// and a recording file cut off anywhere. The store opens over it, a recording
// that said "running" comes back interrupted, what the sidecar claims about the
// files never outlives a look at the files, and the recording can be read to
// its end and deleted, leaving nothing behind and nothing counted.
func FuzzRecordingStore(f *testing.F) {
	lines := seedHeader + "\n" + seedSample + "\n" + seedFooter + "\n"
	stopped := `{"info":{"state":"stopped","samples":1,"files":1,"size_bytes":10},"rotate_bytes":1024}`
	f.Add([]byte(stopped), []byte(lines))
	f.Add([]byte(`{"info":{"state":"running","files":1},"rotate_bytes":1024}`), []byte(lines[:len(lines)-5]))
	f.Add([]byte(`{"info":{"state":"interrupted","files":-1,"size_bytes":-5}}`), []byte{})
	f.Add([]byte(`{"info":{"state":"stopped","files":2000000000,"size_bytes":9000000000000000000}}`), []byte("x\n"))
	f.Add([]byte(`{"info":{"id":"../../x","name":"../../x","location":"/etc"},"rotate_bytes":-1}`), []byte{})
	f.Add([]byte(`{`), []byte(`{`))

	f.Fuzz(func(t *testing.T, meta, data []byte) {
		st, name := storeWith(t, meta, data)
		list := st.list()
		if len(list) == 0 {
			return // the sidecar was not one
		}
		info := list[0]
		if len(list) != 1 || info.ID != name || info.Name != name || info.State == model.RecordingRunning {
			t.Fatalf("recordings = %+v", list)
		}
		if info.Location != st.location {
			t.Fatalf("location %q comes from the sidecar", info.Location)
		}
		if info.Files != 1 || info.SizeBytes != int64(len(data)) || st.used.Load() != int64(len(data)) {
			t.Fatalf("files %d, %d bytes, %d used; on disk: 1 file of %d bytes", info.Files, info.SizeBytes, st.used.Load(), len(data))
		}

		r, got, err := st.open(name, 0)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		read, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil || !bytes.Equal(read, data) {
			t.Fatalf("read %d of %d bytes (%+v): %v", len(read), len(data), got, err)
		}
		if err := st.remove(name); err != nil {
			t.Fatalf("remove: %v", err)
		}
		left, err := os.ReadDir(st.dir)
		if err != nil || len(left) != 0 || st.used.Load() != 0 {
			t.Fatalf("after remove: %v left, %d used, %v", left, st.used.Load(), err)
		}
	})
}

// A recording file of any content converts to CSV: lines that are not samples
// are passed over, the conversion ends, and every row has every column.
func FuzzRecordingCSV(f *testing.F) {
	f.Add([]byte(seedHeader + "\n" + seedSample + "\n" + seedFooter + "\n"))
	f.Add([]byte(seedSample[:len(seedSample)/2]))
	f.Add([]byte(`{"type":"sample","models":[{"group":{"points":[{"name":"a\nb","raw":{"x":[1]},"implemented":true,"scaled":1e308}]}}]}` + "\n"))
	f.Add([]byte(`{"type":"sample","seq":-1,"captured_at":"x"}` + "\n" + `{"type":"sample","models":[null,{}]}` + "\n"))
	f.Add([]byte(`{"type":"sample","models":[{"raw_registers":[65535],"status":"\u0000"}]}`))
	f.Add([]byte("\n\n\x00\xff\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		meta := `{"info":{"state":"stopped","files":1,"size_bytes":` + strconv.Itoa(len(data)) + `}}`
		st, name := storeWith(t, []byte(meta), data)
		e := &Engine{recs: st}
		r, _, err := e.OpenRecordingCSV(ctx, name)
		if err != nil {
			t.Fatalf("OpenRecordingCSV: %v", err)
		}
		defer func() { _ = r.Close() }()
		text, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("conversion: %v", err)
		}
		checkCSVText(t, text, true)
	})
}

// The PEM files an operator names. Whatever they hold, LoadTLS returns an
// identity with a certificate and authorities, or an error.
func FuzzLoadTLS(f *testing.F) {
	certFile, keyFile := selfSigned(f, f.TempDir(), "seed")
	cert, err := os.ReadFile(certFile)
	if err != nil {
		f.Fatal(err)
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(cert, key, cert)
	f.Add(key, cert, cert)
	f.Add(cert, key, []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"))
	f.Add([]byte{}, []byte{}, []byte{})

	f.Fuzz(func(t *testing.T, certPEM, keyPEM, caPEM []byte) {
		dir := t.TempDir()
		paths := []string{filepath.Join(dir, "cert"), filepath.Join(dir, "key"), filepath.Join(dir, "ca")}
		for i, b := range [][]byte{certPEM, keyPEM, caPEM} {
			if err := os.WriteFile(paths[i], b, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cfg, err := LoadTLS(paths[0], paths[1], paths[2])
		if err != nil {
			if strings.Contains(err.Error(), "PRIVATE KEY-----") {
				t.Fatalf("the error quotes key material: %v", err)
			}
			return
		}
		if cfg == nil || len(cfg.Certificate.Certificate) == 0 || cfg.RootCAs == nil {
			t.Fatalf("LoadTLS = %+v", cfg)
		}
	})
}

// safeFileName is what FuzzRecordingName holds an accepted name to.
var safeFileName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// A client names a recording, and the name becomes part of file names: what
// the store accepts stays inside the recordings directory and is listed under
// that name.
func FuzzRecordingName(f *testing.F) {
	for _, s := range []string{"run", "", ".hidden", "../../etc/passwd", "a/b", `a\b`, "a\x00b", "-x", "a.meta.json", strings.Repeat("x", 65), "ünicode"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		st, err := newRecStore(t.TempDir(), true, RecordingLimits{MinFreeBytes: -1}, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		rec, err := st.start("s", model.RecordingConfig{Name: name}, model.RecordingHeader{StartedAt: time.Unix(0, 0).UTC()})
		if err != nil {
			if name == "" || service.CodeOf(err) != service.CodeInvalidArgument {
				t.Fatalf("start(%q): %v", name, err)
			}
			return
		}
		rec.stop(model.StopUser)
		if name != "" && (rec.info.Name != name || !safeFileName.MatchString(name) || strings.HasPrefix(name, ".")) {
			t.Fatalf("accepted %q as %q", name, rec.info.Name)
		}
		entries, err := os.ReadDir(st.dir)
		if err != nil || len(entries) != 2 {
			t.Fatalf("recordings directory: %v, %v", entries, err)
		}
	})
}
