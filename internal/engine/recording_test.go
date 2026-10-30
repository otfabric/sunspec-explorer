// SPDX-License-Identifier: MIT

package engine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// recEngine returns an engine that records into dir.
func recEngine(t testing.TB, dir string, limits RecordingLimits) *Engine {
	t.Helper()
	e := New(Options{DataDir: dir, DataDirTemporary: true, Recording: limits})
	e.second = tick
	t.Cleanup(e.Close)
	return e
}

// line is any line of a recording.
type line struct {
	Type   string              `json:"type"`
	Schema string              `json:"schema"`
	Seq    uint64              `json:"seq"`
	Kind   string              `json:"kind"`
	Reason string              `json:"reason"`
	Models []model.ModelResult `json:"models"`
	// Footer counters.
	Samples  uint64 `json:"samples"`
	Failures uint64 `json:"failures"`
}

// readRecording returns the bytes and the parsed lines of a recording.
func readRecording(t testing.TB, e *Engine, id string) ([]byte, []line) {
	t.Helper()
	r, _, err := e.OpenRecording(ctx, id, 0)
	if err != nil {
		t.Fatalf("OpenRecording: %v", err)
	}
	defer func() { _ = r.Close() }()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	var lines []line
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(nil, 16<<20)
	for sc.Scan() {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("line %d is not JSON: %v\n%.200s", len(lines)+1, err, sc.Text())
		}
		lines = append(lines, l)
	}
	return raw, lines
}

func typesOf(lines []line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Type
		if l.Type == model.LineEvent {
			out[i] += ":" + l.Kind
		}
	}
	return out
}

func recordingSamples(n uint64) func(model.Event) bool {
	return func(ev model.Event) bool { return isSample(ev) && ev.Recording != nil && ev.Recording.Samples >= n }
}

// R7: a recording is a header, one line per sample, event lines for what
// happens to the session, and a footer; failures are in it.
func TestRecording(t *testing.T) {
	dir := t.TempDir()
	d := newSim(t)
	e := recEngine(t, dir, RecordingLimits{})
	ch := events(t, e)
	info := mustOpen(t, e, d)

	// Recording may start before polling: it waits.
	rec, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "run-1"})
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if rec.ID != "run-1" || rec.State != model.RecordingRunning || rec.SessionID != info.ID || !rec.Temporary ||
		rec.Location != filepath.Join(dir, "recordings") || rec.Files != 1 || rec.SizeBytes == 0 || rec.StartedAt.IsZero() {
		t.Errorf("recording = %+v", rec)
	}
	started := waitEvent(t, ch, "recording_status", func(ev model.Event) bool { return ev.Kind == model.EventRecordingStatus })
	if started.Recording == nil || started.Recording.State != model.RecordingRunning || started.SessionID != info.ID {
		t.Errorf("event = %+v", started)
	}
	if st, _ := e.State(ctx, info.ID); st.Recording == nil || st.Recording.ID != "run-1" {
		t.Errorf("state.recording = %+v", st.Recording)
	}

	// One recording per session; names are unique and never paths.
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "other"}); service.CodeOf(err) != service.CodeInvalidArgument {
		t.Errorf("second recording on the session = %v", err)
	}
	if err := e.DeleteRecording(ctx, "run-1"); service.CodeOf(err) != service.CodeInvalidArgument {
		t.Errorf("delete while running = %v", err)
	}

	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, ch, "2 recorded samples", recordingSamples(2))
	if ev.Recording.Failures != 0 || ev.Recording.SizeBytes <= rec.SizeBytes {
		t.Errorf("counters = %+v", ev.Recording)
	}

	// A failing model is recorded as such, and counted.
	inv := info.Models[idxInverter]
	d.setFilter(failRange(inv.StartAddress, inv.StartAddress+1, modbus.ErrServerDeviceFailure))
	waitEvent(t, ch, "a recorded failure", func(ev model.Event) bool {
		return isSample(ev) && ev.Recording != nil && ev.Recording.Failures >= 1
	})
	d.setFilter(nil)

	// A changed chain leaves an event line with the new chain.
	d.setModels(stdModels()[:3]...)
	waitEvent(t, ch, "discovery_changed", func(ev model.Event) bool { return ev.Kind == model.EventDiscoveryChanged })
	waitEvent(t, ch, "a sample of the new chain", func(ev model.Event) bool {
		return isSample(ev) && len(ev.Sample.Models) == 2 && ev.Sample.Models[0].Status == model.ModelOK
	})

	if err := e.StopPolling(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	stopped, err := e.StopRecording(ctx, "run-1")
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	if stopped.State != model.RecordingStopped || stopped.StopReason != model.StopUser || stopped.StoppedAt == nil ||
		stopped.Samples < 4 || stopped.Failures == 0 {
		t.Errorf("stopped = %+v", stopped)
	}
	waitEvent(t, ch, "recording stopped", func(ev model.Event) bool {
		return ev.Kind == model.EventRecordingStatus && ev.Recording.State == model.RecordingStopped
	})
	if again, err := e.StopRecording(ctx, "run-1"); err != nil || again.Samples != stopped.Samples {
		t.Errorf("second stop = %+v, %v", again, err)
	}
	if st, _ := e.State(ctx, info.ID); st.Recording != nil {
		t.Errorf("state.recording after stop = %+v", st.Recording)
	}

	raw, lines := readRecording(t, e, "run-1")
	if int64(len(raw)) != stopped.SizeBytes {
		t.Errorf("read %d bytes, size is %d", len(raw), stopped.SizeBytes)
	}
	types := typesOf(lines)
	if types[0] != model.LineHeader || lines[0].Schema != model.RecordingSchema || types[len(types)-1] != model.LineFooter {
		t.Fatalf("lines = %v", types)
	}
	for _, want := range []string{"event:gap", "event:polling_started", "event:discovery_changed"} {
		if !slices.Contains(types, want) {
			t.Errorf("no %s line in %v", want, types)
		}
	}
	// The header describes the device; samples are compact and numbered.
	var header model.RecordingHeader
	if err := json.Unmarshal(raw[:bytes.IndexByte(raw, '\n')], &header); err != nil {
		t.Fatal(err)
	}
	if header.Name != "run-1" || len(header.Models) != 6 || header.Target.BaseAddress != 40000 || header.Device == nil ||
		header.Libs.RegistryModels == 0 || header.App.Name != model.AppName {
		t.Errorf("header = %+v", header)
	}
	var samples, failures, seq uint64
	for _, l := range lines {
		if l.Type != model.LineSample {
			continue
		}
		samples++
		if l.Seq != seq+1 {
			t.Errorf("sample seq %d after %d", l.Seq, seq)
		}
		seq = l.Seq
		if slices.ContainsFunc(l.Models, func(m model.ModelResult) bool { return m.Status != model.ModelOK }) {
			failures++
		}
	}
	footer := lines[len(lines)-1]
	if samples != stopped.Samples || failures != stopped.Failures || footer.Samples != samples || footer.Failures != failures ||
		footer.Reason != model.StopUser {
		t.Errorf("file: %d samples, %d failures; info: %+v; footer: %+v", samples, failures, stopped, footer)
	}
	// Compact: no labels or descriptions in a recording.
	if bytes.Contains(raw, []byte(`"label"`)) || bytes.Contains(raw, []byte(`"description"`)) {
		t.Error("the recording carries point metadata")
	}

	// Listed, then deleted with its files.
	list, _ := e.Recordings(ctx)
	if len(list) != 1 || list[0].ID != "run-1" || list[0].State != model.RecordingStopped {
		t.Errorf("Recordings = %+v", list)
	}
	if err := e.DeleteRecording(ctx, "run-1"); err != nil {
		t.Fatalf("DeleteRecording: %v", err)
	}
	if list, _ := e.Recordings(ctx); len(list) != 0 {
		t.Errorf("after delete = %+v", list)
	}
	if left, _ := os.ReadDir(filepath.Join(dir, "recordings")); len(left) != 0 {
		t.Errorf("files left: %v", left)
	}
	for _, call := range []func() error{
		func() error { return e.DeleteRecording(ctx, "run-1") },
		func() error { _, err := e.StopRecording(ctx, "run-1"); return err },
		func() error { _, _, err := e.OpenRecording(ctx, "run-1", 0); return err },
		func() error { _, err := e.StartRecording(ctx, "nope", model.RecordingConfig{}); return err },
	} {
		if err := call(); service.CodeOf(err) != service.CodeNotFound {
			t.Errorf("unknown recording or session = %v, want not_found", err)
		}
	}
}

// A13: clients send names, never paths.
func TestRecordingNames(t *testing.T) {
	d := newSim(t, stdModels()[:2]...)
	e := recEngine(t, t.TempDir(), RecordingLimits{})
	info := mustOpen(t, e, d)

	for _, name := range []string{"../escape", "a/b", `a\b`, ".hidden", "..", "with space", "ümlaut", strings.Repeat("x", 65), "a\x00b"} {
		if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: name}); service.CodeOf(err) != service.CodeInvalidArgument {
			t.Errorf("name %q = %v, want invalid_argument", name, err)
		}
	}
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{RotateBytes: -1}); service.CodeOf(err) != service.CodeInvalidArgument {
		t.Errorf("negative rotation = %v", err)
	}

	// Without a name the engine picks one; twice in the same second too.
	first, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{})
	if err != nil || !strings.HasPrefix(first.Name, "rec-") {
		t.Fatalf("unnamed = %+v, %v", first, err)
	}
	if _, err := e.StopRecording(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{})
	if err != nil || second.Name == first.Name {
		t.Fatalf("second unnamed = %+v, %v", second, err)
	}
	if _, err := e.StopRecording(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: first.Name}); service.CodeOf(err) != service.CodeInvalidArgument {
		t.Errorf("duplicate name = %v", err)
	}
	if list, _ := e.Recordings(ctx); len(list) != 2 {
		t.Errorf("Recordings = %+v", list)
	}
	if entries, _ := os.ReadDir(filepath.Dir(e.recs.dir)); len(entries) != 1 {
		t.Errorf("files outside the recordings directory: %v", entries)
	}
}

func TestRecordingNeedsDataDir(t *testing.T) {
	d := newSim(t, stdModels()[:2]...)
	e := newEngine(t, tick)
	info := mustOpen(t, e, d)
	for _, call := range []func() error{
		func() error { _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{}); return err },
		func() error { _, err := e.StopRecording(ctx, "x"); return err },
		func() error { _, _, err := e.OpenRecording(ctx, "x", 0); return err },
		func() error { return e.DeleteRecording(ctx, "x") },
	} {
		if err := call(); service.CodeOf(err) != service.CodeNotAllowed {
			t.Errorf("without a data directory = %v, want not_allowed", err)
		}
	}
	if list, err := e.Recordings(ctx); err != nil || len(list) != 0 {
		t.Errorf("Recordings = %v, %v", list, err)
	}
	// A data directory that cannot be used leaves the engine without
	// recording, not without everything else.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	broken := New(Options{DataDir: file})
	defer broken.Close()
	if broken.recs != nil {
		t.Error("a file was accepted as data directory")
	}
}

// Rotation by size, and reading the files as one stream from any offset.
func TestRecordingRotation(t *testing.T) {
	d := newSim(t)
	e := recEngine(t, t.TempDir(), RecordingLimits{})
	ch := events(t, e)
	info := mustOpen(t, e, d)

	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "rot", RotateBytes: 6000}); err != nil {
		t.Fatal(err)
	}
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, ch, "several files", func(ev model.Event) bool {
		return isSample(ev) && ev.Recording != nil && ev.Recording.Files >= 3
	})

	// While it runs: readable up to its size at this moment.
	r, live, err := e.OpenRecording(ctx, "rot", 0)
	if err != nil {
		t.Fatal(err)
	}
	part, _ := io.ReadAll(r)
	_ = r.Close()
	if int64(len(part)) != live.SizeBytes || live.SizeBytes < ev.Recording.SizeBytes || part[len(part)-1] != '\n' {
		t.Errorf("read %d bytes of a running recording of %d", len(part), live.SizeBytes)
	}

	stopped, err := e.StopRecording(ctx, "rot")
	if err != nil {
		t.Fatal(err)
	}
	raw, lines := readRecording(t, e, "rot")
	if !bytes.HasPrefix(raw, part) {
		t.Error("the finished recording does not start with what was read while it ran")
	}
	if lines[0].Type != model.LineHeader || lines[len(lines)-1].Type != model.LineFooter {
		t.Errorf("lines = %v", typesOf(lines))
	}

	// The files on disk are what the stream is made of; every one of them
	// holds whole lines.
	var joined []byte
	for i := 0; i < stopped.Files; i++ {
		b, err := os.ReadFile(e.recs.filePath("rot", i))
		if err != nil {
			t.Fatal(err)
		}
		if len(b) == 0 || b[len(b)-1] != '\n' {
			t.Errorf("file %d does not end with a whole line", i)
		}
		joined = append(joined, b...)
	}
	if !bytes.Equal(joined, raw) || stopped.Files < 3 {
		t.Errorf("%d files, %d bytes on disk, %d bytes read", stopped.Files, len(joined), len(raw))
	}

	// Any offset, across file boundaries.
	for _, offset := range []int64{1, 5999, 6000, 6001, int64(len(raw)) / 2, int64(len(raw)) - 1, int64(len(raw))} {
		r, _, err := e.OpenRecording(ctx, "rot", offset)
		if err != nil {
			t.Fatalf("offset %d: %v", offset, err)
		}
		got, _ := io.ReadAll(r)
		if err := r.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		if !bytes.Equal(got, raw[offset:]) {
			t.Errorf("offset %d: got %d bytes, want %d", offset, len(got), len(raw)-int(offset))
		}
	}
	for _, offset := range []int64{-1, int64(len(raw)) + 1} {
		if _, _, err := e.OpenRecording(ctx, "rot", offset); service.CodeOf(err) != service.CodeInvalidArgument {
			t.Errorf("offset %d = %v", offset, err)
		}
	}
	// Small reads work too.
	r, _, _ = e.OpenRecording(ctx, "rot", 0)
	var small []byte
	buf := make([]byte, 7)
	for {
		n, err := r.Read(buf)
		small = append(small, buf[:n]...)
		if err != nil {
			break
		}
	}
	_ = r.Close()
	if !bytes.Equal(small, raw) {
		t.Errorf("small reads gave %d bytes", len(small))
	}
}

// R15: a recording that reaches its size limit is stopped by the engine, and
// a full store takes no new recording.
func TestRecordingLimits(t *testing.T) {
	d := newSim(t)
	e := recEngine(t, t.TempDir(), RecordingLimits{MaxBytes: 40000, MaxTotalBytes: 70000})
	ch := events(t, e)
	info := mustOpen(t, e, d)
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}

	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "one"}); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, ch, "stopped by the limit", func(ev model.Event) bool {
		return ev.Kind == model.EventRecordingStatus && ev.Recording.State == model.RecordingStopped
	})
	if ev.Recording.StopReason != model.StopLimit || ev.Recording.SizeBytes < 40000 {
		t.Errorf("event = %+v", ev.Recording)
	}
	_, lines := readRecording(t, e, "one")
	if last := lines[len(lines)-1]; last.Type != model.LineFooter || last.Reason != model.StopLimit {
		t.Errorf("last line = %+v", last)
	}
	// The session is free again, and polling went on.
	if st, _ := e.State(ctx, info.ID); st.Recording != nil || !st.Polling.Active {
		t.Errorf("state = %+v", st)
	}

	// The second recording is stopped by the total; then nothing starts.
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "two"}); err != nil {
		t.Fatal(err)
	}
	ev = waitEvent(t, ch, "stopped by the total", func(ev model.Event) bool {
		return ev.Kind == model.EventRecordingStatus && ev.Recording.Name == "two" && ev.Recording.State == model.RecordingStopped
	})
	if ev.Recording.StopReason != model.StopLimit || ev.Recording.SizeBytes >= 40000 {
		t.Errorf("event = %+v", ev.Recording)
	}
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "three"}); service.CodeOf(err) != service.CodeLimitExceeded {
		t.Errorf("start with a full store = %v", err)
	}
	// Deleting makes room.
	if err := e.DeleteRecording(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "three"}); err != nil {
		t.Errorf("start after delete = %v", err)
	}

	// A recording older than the limit is stopped too.
	old := recEngine(t, t.TempDir(), RecordingLimits{MaxDuration: time.Nanosecond})
	oldCh := events(t, old)
	oldInfo := mustOpen(t, old, d)
	if _, err := old.StartRecording(ctx, oldInfo.ID, model.RecordingConfig{Name: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := old.StartPolling(ctx, oldInfo.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	ev = waitEvent(t, oldCh, "stopped by age", func(ev model.Event) bool {
		return ev.Kind == model.EventRecordingStatus && ev.Recording.State == model.RecordingStopped
	})
	if ev.Recording.StopReason != model.StopLimit || ev.Recording.Samples != 1 {
		t.Errorf("event = %+v", ev.Recording)
	}
}

// R15: recording leaves a floor of free space alone: it does not start below
// it, and a running recording stops when the disk gets there.
func TestRecordingFreeSpaceFloor(t *testing.T) {
	d := newSim(t, stdModels()[:2]...)
	e := recEngine(t, t.TempDir(), RecordingLimits{MinFreeBytes: 10 << 20})
	ch := events(t, e)
	info := mustOpen(t, e, d)

	var free atomic.Int64
	known := atomic.Bool{}
	known.Store(true)
	var asked atomic.Value
	e.recs.free = func(dir string) (int64, bool) {
		asked.Store(dir)
		return free.Load(), known.Load()
	}

	// Too little room: nothing starts, and the message says where and how
	// much.
	free.Store(4 << 20)
	_, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "full"})
	if service.CodeOf(err) != service.CodeLimitExceeded || !strings.Contains(err.Error(), "4.0 MiB") ||
		!strings.Contains(err.Error(), "10.0 MiB") || !strings.Contains(err.Error(), e.recs.location) {
		t.Fatalf("start on a full disk = %v", err)
	}
	if asked.Load() != e.recs.dir {
		t.Errorf("free space asked for %v, want the recordings directory", asked.Load())
	}
	if list, _ := e.Recordings(ctx); len(list) != 0 {
		t.Errorf("a refused recording left something: %+v", list)
	}

	// Room enough: it runs, until the disk fills up.
	free.Store(500 << 20)
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "run"}); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "2 recorded samples", recordingSamples(2))
	free.Store(10<<20 - 1)
	ev := waitEvent(t, ch, "stopped for disk space", func(ev model.Event) bool {
		return ev.Kind == model.EventRecordingStatus && ev.Recording.State == model.RecordingStopped
	})
	if ev.Recording.StopReason != model.StopDiskSpace || ev.Recording.Samples < 3 {
		t.Errorf("event = %+v", ev.Recording)
	}
	_, lines := readRecording(t, e, "run")
	if last := lines[len(lines)-1]; last.Type != model.LineFooter || last.Reason != model.StopDiskSpace {
		t.Errorf("last line = %+v", last)
	}
	// Polling goes on; only the recording ended.
	if st, _ := e.State(ctx, info.ID); st.Recording != nil || !st.Polling.Active {
		t.Errorf("state = %+v", st)
	}

	// Exactly at the floor is enough; a file system that does not tell is
	// not treated as full.
	free.Store(10 << 20)
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "edge"}); err != nil {
		t.Errorf("start at the floor = %v", err)
	}
	if _, err := e.StopRecording(ctx, "edge"); err != nil {
		t.Fatal(err)
	}
	free.Store(0)
	known.Store(false)
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "unknown"}); err != nil {
		t.Errorf("start on a file system without figures = %v", err)
	}

	// Switched off, and the default.
	off := recEngine(t, t.TempDir(), RecordingLimits{MinFreeBytes: -1})
	off.recs.free = func(string) (int64, bool) { return 0, true }
	if _, low := off.recs.lowOnSpace(); low {
		t.Error("the check is off and still reports low space")
	}
	def := recEngine(t, t.TempDir(), RecordingLimits{})
	if def.recs.limits.MinFreeBytes != DefaultMinFreeBytes {
		t.Errorf("default floor = %d", def.recs.limits.MinFreeBytes)
	}
	// The real figure for a real directory.
	if free, ok := freeBytes(t.TempDir()); !ok || free <= 0 {
		t.Errorf("freeBytes = %d, %v", free, ok)
	}
	if _, ok := freeBytes(filepath.Join(t.TempDir(), "missing")); ok {
		t.Error("free space of a missing directory")
	}
}

// A9, A14: after a kill the recording is listed as interrupted, counted from
// its files, and readable; a truncated last line is tolerated.
func TestRecordingRecovery(t *testing.T) {
	dir := t.TempDir()
	d := newSim(t)
	e := recEngine(t, dir, RecordingLimits{})
	ch := events(t, e)
	info := mustOpen(t, e, d)
	if _, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: "long", RotateBytes: 8000}); err != nil {
		t.Fatal(err)
	}
	if err := e.StartPolling(ctx, info.ID, model.PollConfig{IntervalS: 1}); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "some samples", recordingSamples(4))
	if err := e.StopPolling(ctx, info.ID); err != nil {
		t.Fatal(err)
	}

	// The kill: the file is abandoned without a footer, in the middle of a
	// line, and the sidecar still says "running".
	rec, _ := e.recs.get("long")
	rec.mu.Lock()
	written, files := rec.info.Samples, rec.info.Files
	_, _ = rec.file.WriteString(`{"type":"sample","seq":99,"models":[{"index":1,"sta`)
	_ = rec.file.Close()
	rec.file = nil
	rec.mu.Unlock()

	// The restart.
	e2 := recEngine(t, dir, RecordingLimits{})
	list, err := e2.Recordings(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("Recordings after restart = %+v, %v", list, err)
	}
	got := list[0]
	if got.State != model.RecordingInterrupted || got.Samples != written || got.Files != files || got.StoppedAt == nil ||
		got.StopReason != "" || got.SessionID != info.ID {
		t.Errorf("recovered = %+v, want interrupted with %d samples in %d files", got, written, files)
	}
	r, opened, err := e2.OpenRecording(ctx, "long", 0)
	if err != nil {
		t.Fatalf("OpenRecording: %v", err)
	}
	raw, _ := io.ReadAll(r)
	_ = r.Close()
	if int64(len(raw)) != opened.SizeBytes || !bytes.HasSuffix(raw, []byte(`"sta`)) {
		t.Errorf("read %d of %d bytes", len(raw), opened.SizeBytes)
	}
	// Every complete line is intact.
	complete := raw[:bytes.LastIndexByte(raw, '\n')+1]
	for i, l := range bytes.Split(bytes.TrimSuffix(complete, []byte("\n")), []byte("\n")) {
		if !json.Valid(l) {
			t.Errorf("line %d is damaged", i+1)
		}
	}
	// It cannot be stopped again, and it can be deleted.
	if again, err := e2.StopRecording(ctx, "long"); err != nil || again.State != model.RecordingInterrupted {
		t.Errorf("stop of an interrupted recording = %+v, %v", again, err)
	}

	// A second restart finds it as it was left: interrupted, not recounted
	// into something else.
	e3 := recEngine(t, dir, RecordingLimits{})
	if list, _ := e3.Recordings(ctx); len(list) != 1 || list[0].State != model.RecordingInterrupted || list[0].Samples != written {
		t.Errorf("second restart = %+v", list)
	}
	if err := e3.DeleteRecording(ctx, "long"); err != nil {
		t.Errorf("delete: %v", err)
	}

	// Files that are not recordings are left alone.
	if err := os.WriteFile(filepath.Join(dir, "recordings", "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recordings", "broken.meta.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	e4 := recEngine(t, dir, RecordingLimits{})
	if list, _ := e4.Recordings(ctx); len(list) != 0 {
		t.Errorf("stray files listed: %+v", list)
	}
}

// A recording ends with its session, and with the engine.
func TestRecordingEndsWithSessionAndEngine(t *testing.T) {
	dir := t.TempDir()
	d := newSim(t, stdModels()[:2]...)
	e := recEngine(t, dir, RecordingLimits{})
	ch := events(t, e)

	first := mustOpen(t, e, d)
	if _, err := e.StartRecording(ctx, first.ID, model.RecordingConfig{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := e.CloseSession(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, ch, "stopped with the session", func(ev model.Event) bool {
		return ev.Kind == model.EventRecordingStatus && ev.Recording.State == model.RecordingStopped
	})
	if ev.Recording.StopReason != model.StopSessionClosed {
		t.Errorf("reason = %s", ev.Recording.StopReason)
	}

	second := mustOpen(t, e, d)
	if _, err := e.StartRecording(ctx, second.ID, model.RecordingConfig{Name: "b"}); err != nil {
		t.Fatal(err)
	}
	e.Close()

	// A clean shutdown is not an interruption.
	e2 := recEngine(t, dir, RecordingLimits{})
	list, _ := e2.Recordings(ctx)
	if len(list) != 2 {
		t.Fatalf("Recordings = %+v", list)
	}
	for _, r := range list {
		want := map[string]string{"a": model.StopSessionClosed, "b": model.StopShutdown}[r.Name]
		if r.State != model.RecordingStopped || r.StopReason != want {
			t.Errorf("%s = %s (%s), want stopped (%s)", r.Name, r.State, r.StopReason, want)
		}
	}
	_, lines := readRecording(t, e2, "b")
	if last := lines[len(lines)-1]; last.Type != model.LineFooter || last.Reason != model.StopShutdown {
		t.Errorf("last line = %+v", last)
	}
}

// The sidecar's counts give way to the files: a delete does not walk a count
// that was never true, and what is used is what is on disk.
func TestRecordingSidecarCountsAreChecked(t *testing.T) {
	dir := filepath.Join(t.TempDir(), recordingsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := `{"info":{"state":"stopped","files":2000000000,"size_bytes":9000000000000000000}}`
	if err := os.WriteFile(filepath.Join(dir, "x"+metaSuffix), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.000.jsonl"), []byte("ab\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := newRecStore(filepath.Dir(dir), true, RecordingLimits{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if list := st.list(); len(list) != 1 || list[0].Files != 1 || list[0].SizeBytes != 3 || st.used.Load() != 3 {
		t.Fatalf("recordings = %+v, used %d", list, st.used.Load())
	}
	if err := st.remove("x"); err != nil {
		t.Fatal(err)
	}
}
