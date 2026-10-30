// SPDX-License-Identifier: MIT

package engine

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/otfabric/go-sunspec/registry"

	"github.com/otfabric/sunspec-explorer/internal/buildinfo"
	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// Recording defaults. They are small because the default data directory is
// temporary storage, which on Debian 13 is RAM.
const (
	DefaultMaxRecordingBytes  = 64 << 20
	DefaultMaxRecordingsBytes = 256 << 20
	DefaultRotateBytes        = 16 << 20
	// DefaultMinFreeBytes is the free space recording leaves alone. In a
	// temporary directory in RAM that space is the system's memory.
	DefaultMinFreeBytes     = 64 << 20
	DefaultMaxRecordingTime = 7 * 24 * time.Hour

	recordingsDir = "recordings"
	metaSuffix    = ".meta.json"
	// metaEvery is how often the counters of a running recording are saved,
	// in samples. The files themselves are written line by line; the saved
	// counters only spare a recount when the engine restarts.
	metaEvery = 30
)

// RecordingLimits bounds what recordings may use. Zero values select the
// defaults.
type RecordingLimits struct {
	// MaxBytes is the size at which a recording is stopped.
	MaxBytes int64
	// MaxTotalBytes is the size of all recordings together at which the
	// running ones are stopped and no new one starts.
	MaxTotalBytes int64
	// RotateBytes is the size at which a recording continues in a new file.
	RotateBytes int64
	// MaxDuration is the age at which a recording is stopped.
	MaxDuration time.Duration
	// MinFreeBytes is the free space of the data directory's file system
	// below which no recording starts and the running ones are stopped.
	// Negative switches the check off.
	MinFreeBytes int64
}

func (l RecordingLimits) withDefaults() RecordingLimits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultMaxRecordingBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = DefaultMaxRecordingsBytes
	}
	if l.RotateBytes <= 0 {
		l.RotateBytes = DefaultRotateBytes
	}
	if l.MaxDuration <= 0 {
		l.MaxDuration = DefaultMaxRecordingTime
	}
	if l.MinFreeBytes == 0 {
		l.MinFreeBytes = DefaultMinFreeBytes
	}
	return l
}

// recordingName is what a client may call a recording. Clients send names,
// never paths: the name becomes part of file names in a fixed directory.
var recordingName = regexp.MustCompile(`^[a-zA-Z0-9_-][a-zA-Z0-9._-]{0,63}$`)

// recMeta is the sidecar file of a recording: what the list shows, and what
// tells a recording that was interrupted from one that was stopped.
type recMeta struct {
	Info        model.RecordingInfo `json:"info"`
	RotateBytes int64               `json:"rotate_bytes"`
}

// recStore owns the recordings directory.
type recStore struct {
	dir       string
	location  string
	temporary bool
	limits    RecordingLimits
	log       *slog.Logger
	// free reports the free space under dir; tests replace it.
	free func(dir string) (int64, bool)

	// used is the size of all recordings together.
	used atomic.Int64

	mu   sync.Mutex
	recs map[string]*recording
}

// recording is one recording: its counters and, while it runs, its file.
// The store's lock comes before a recording's lock.
type recording struct {
	store *recStore

	mu        sync.Mutex
	info      model.RecordingInfo
	rotate    int64
	file      *os.File // nil when not running
	fileSize  int64
	sinceMeta int
}

// newRecStore opens the recordings directory under dataDir, creating it, and
// marks the recordings a previous run left running as interrupted.
func newRecStore(dataDir string, temporary bool, limits RecordingLimits, log *slog.Logger) (*recStore, error) {
	dir := filepath.Join(dataDir, recordingsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	st := &recStore{
		dir: dir, location: dir, temporary: temporary, limits: limits.withDefaults(), log: log, free: freeBytes,
		recs: make(map[string]*recording),
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), metaSuffix)
		if !ok || !recordingName.MatchString(name) {
			continue
		}
		rec, err := st.load(name)
		if err != nil {
			log.Warn("recording not listed", "name", name, "error", err)
			continue
		}
		st.recs[name] = rec
		st.used.Add(rec.info.SizeBytes)
	}
	return st, nil
}

// load reads the sidecar of a recording. A recording that still says
// "running" was cut off by a crash or a kill: its files are valid up to the
// last complete line, so it is counted again from them and kept as
// interrupted.
func (st *recStore) load(name string) (*recording, error) {
	b, err := os.ReadFile(st.metaPath(name))
	if err != nil {
		return nil, err
	}
	var meta recMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return nil, err
	}
	rec := &recording{store: st, info: meta.Info, rotate: meta.RotateBytes}
	rec.info.ID, rec.info.Name = name, name
	rec.info.Location, rec.info.Temporary = st.location, st.temporary
	// The sidecar is what the list shows, but the files are what is there. A
	// count it gets wrong would be walked by a delete and served by a download.
	rec.measure()
	if rec.info.State != model.RecordingRunning {
		return rec, nil
	}
	rec.info.State = model.RecordingInterrupted
	rec.recount()
	if err := rec.saveMeta(); err != nil {
		return nil, err
	}
	st.log.Warn("recording was interrupted", "name", name, "samples", rec.info.Samples)
	return rec, nil
}

// measure sets the number of files and their size from what is on disk.
func (r *recording) measure() {
	r.info.Files, r.info.SizeBytes = 0, 0
	for {
		fi, err := os.Stat(r.store.filePath(r.info.Name, r.info.Files))
		if err != nil {
			return
		}
		r.info.Files++
		r.info.SizeBytes += fi.Size()
	}
}

// recount rebuilds the counters of a recording from its files. A truncated
// last line, what a crash leaves, is ignored.
func (r *recording) recount() {
	r.info.Samples, r.info.Failures = 0, 0
	var last time.Time
	for i := 0; ; i++ {
		f, err := os.Open(r.store.filePath(r.info.Name, i))
		if err != nil {
			break
		}
		if fi, err := f.Stat(); err == nil && fi.ModTime().After(last) {
			last = fi.ModTime()
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			var line struct {
				Type   string `json:"type"`
				Models []struct {
					Status string `json:"status"`
				} `json:"models"`
			}
			if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != model.LineSample {
				continue
			}
			r.info.Samples++
			for _, m := range line.Models {
				if m.Status != model.ModelOK {
					r.info.Failures++
					break
				}
			}
		}
		_ = f.Close()
	}
	if !last.IsZero() {
		at := last.UTC()
		r.info.StoppedAt = &at
	}
}

func (st *recStore) metaPath(name string) string { return filepath.Join(st.dir, name+metaSuffix) }

func (st *recStore) filePath(name string, index int) string {
	return filepath.Join(st.dir, fmt.Sprintf("%s.%03d.jsonl", name, index))
}

// lowOnSpace reports whether the file system of the recordings has less free
// space than the floor. A file system that does not tell is not low.
func (st *recStore) lowOnSpace() (free int64, low bool) {
	if st.limits.MinFreeBytes < 0 {
		return 0, false
	}
	free, ok := st.free(st.dir)
	return free, ok && free < st.limits.MinFreeBytes
}

// start creates a recording and writes its header.
func (st *recStore) start(sessionID string, cfg model.RecordingConfig, header model.RecordingHeader) (*recording, error) {
	name := cfg.Name
	if name == "" {
		name = "rec-" + header.StartedAt.Format("20060102T150405Z")
	}
	if !recordingName.MatchString(name) {
		return nil, service.Errorf(service.CodeInvalidArgument,
			"recording name %q: use 1 to 64 letters, digits, dots, dashes and underscores, not starting with a dot", name)
	}
	if cfg.RotateBytes < 0 {
		return nil, service.Errorf(service.CodeInvalidArgument, "rotate_bytes must not be negative")
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if _, exists := st.recs[name]; exists {
		if cfg.Name != "" {
			return nil, service.Errorf(service.CodeInvalidArgument, "a recording named %q exists already", name)
		}
		// Two unnamed recordings started within the same second.
		for i := 2; ; i++ {
			if _, exists := st.recs[fmt.Sprintf("%s-%d", name, i)]; !exists {
				name = fmt.Sprintf("%s-%d", name, i)
				break
			}
		}
	}
	if used := st.used.Load(); used >= st.limits.MaxTotalBytes {
		return nil, service.Errorf(service.CodeLimitExceeded,
			"the recordings use %.1f of %.1f MiB: save and delete some first",
			float64(used)/(1<<20), float64(st.limits.MaxTotalBytes)/(1<<20))
	}
	if free, low := st.lowOnSpace(); low {
		return nil, service.Errorf(service.CodeLimitExceeded,
			"only %.1f MiB are free where recordings are kept (%s), and %.1f MiB are left alone: free some space, or choose another --data-dir",
			float64(free)/(1<<20), st.location, float64(st.limits.MinFreeBytes)/(1<<20))
	}

	rec := &recording{
		store:  st,
		rotate: st.limits.RotateBytes,
		info: model.RecordingInfo{
			ID: name, Name: name, SessionID: sessionID, State: model.RecordingRunning,
			StartedAt: header.StartedAt, Location: st.location, Temporary: st.temporary,
		},
	}
	if cfg.RotateBytes > 0 {
		rec.rotate = cfg.RotateBytes
	}
	header.Type, header.Schema, header.Name = model.LineHeader, model.RecordingSchema, name
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if err := rec.openFile(); err != nil {
		return nil, service.Wrap(service.CodeInternal, err, "cannot create the recording")
	}
	if err := rec.writeLine(header); err != nil {
		rec.closeFile()
		_ = os.Remove(st.filePath(name, 0))
		return nil, service.Wrap(service.CodeInternal, err, "cannot write the recording")
	}
	if err := rec.saveMeta(); err != nil {
		rec.closeFile()
		_ = os.Remove(st.filePath(name, 0))
		return nil, service.Wrap(service.CodeInternal, err, "cannot write the recording")
	}
	st.recs[name] = rec
	return rec, nil
}

// openFile starts the next file of the recording. The caller holds r.mu.
func (r *recording) openFile() error {
	f, err := os.OpenFile(r.store.filePath(r.info.Name, r.info.Files), os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	r.file, r.fileSize = f, 0
	r.info.Files++
	return nil
}

func (r *recording) closeFile() {
	if r.file != nil {
		_ = r.file.Close()
		r.file = nil
	}
}

// writeLine appends one JSON line, in a single write, so that a reader and a
// crash see whole lines (bar the last one). It starts a new file first when
// this one is full. The caller holds r.mu.
func (r *recording) writeLine(v any) error {
	if r.file == nil {
		return errors.New("recording is not running")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if r.fileSize > 0 && r.fileSize+int64(len(b)) > r.rotate {
		r.closeFile()
		if err := r.openFile(); err != nil {
			return err
		}
	}
	n, err := r.file.Write(b)
	r.fileSize += int64(n)
	r.info.SizeBytes += int64(n)
	r.store.used.Add(int64(n))
	return err
}

// saveMeta writes the sidecar atomically. The caller holds r.mu.
func (r *recording) saveMeta() error {
	b, err := json.Marshal(recMeta{Info: r.info, RotateBytes: r.rotate})
	if err != nil {
		return err
	}
	path := r.store.metaPath(r.info.Name)
	tmp, err := os.CreateTemp(r.store.dir, ".meta-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after the rename
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	r.sinceMeta = 0
	return os.Rename(tmp.Name(), path)
}

func (r *recording) snapshot() model.RecordingInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.info
}

func (r *recording) running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file != nil
}

// sample appends a sample. stopped is true when that made the recording
// reach a limit or fail, and end.
func (r *recording) sample(s *model.Sample) (info model.RecordingInfo, stopped bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return r.info, false
	}
	failed := false
	for i := range s.Models {
		if s.Models[i].Status != model.ModelOK {
			failed = true
			break
		}
	}
	if err := r.writeLine(model.RecordingSample{Type: model.LineSample, Sample: *s}); err != nil {
		r.store.log.Error("recording stopped: write failed", "name", r.info.Name, "error", err)
		r.finish(model.StopError)
		return r.info, true
	}
	r.info.Samples++
	if failed {
		r.info.Failures++
	}
	limits := r.store.limits
	if r.info.SizeBytes >= limits.MaxBytes || r.store.used.Load() >= limits.MaxTotalBytes ||
		time.Since(r.info.StartedAt) >= limits.MaxDuration {
		r.finish(model.StopLimit)
		return r.info, true
	}
	if free, low := r.store.lowOnSpace(); low {
		r.store.log.Warn("recording stopped: low on disk space", "name", r.info.Name, "free_bytes", free)
		r.finish(model.StopDiskSpace)
		return r.info, true
	}
	if r.sinceMeta++; r.sinceMeta >= metaEvery {
		_ = r.saveMeta()
	}
	return r.info, false
}

// event appends an event line.
func (r *recording) event(kind, detail string, models []model.ModelInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return
	}
	_ = r.writeLine(model.RecordingEvent{Type: model.LineEvent, At: time.Now().UTC(), Kind: kind, Detail: detail, Models: models})
}

// stop ends the recording with a footer. Stopping a recording that is not
// running changes nothing.
func (r *recording) stop(reason string) model.RecordingInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file != nil {
		r.finish(reason)
	}
	return r.info
}

// finish writes the footer and closes the recording. The caller holds r.mu.
func (r *recording) finish(reason string) {
	now := time.Now().UTC()
	_ = r.writeLine(model.RecordingFooter{
		Type: model.LineFooter, StoppedAt: now, Samples: r.info.Samples, Failures: r.info.Failures, Reason: reason,
	})
	r.closeFile()
	r.info.State, r.info.StoppedAt, r.info.StopReason = model.RecordingStopped, &now, reason
	if err := r.saveMeta(); err != nil {
		r.store.log.Error("recording state not saved", "name", r.info.Name, "error", err)
	}
}

func (st *recStore) get(id string) (*recording, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	rec, ok := st.recs[id]
	if !ok {
		return nil, service.Errorf(service.CodeNotFound, "no recording %q", id)
	}
	return rec, nil
}

// list returns every recording, newest first.
func (st *recStore) list() []model.RecordingInfo {
	st.mu.Lock()
	out := make([]model.RecordingInfo, 0, len(st.recs))
	for _, r := range st.recs {
		out = append(out, r.snapshot())
	}
	st.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].Name > out[j].Name
	})
	return out
}

// remove deletes a recording that is not running.
func (st *recStore) remove(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	rec, ok := st.recs[id]
	if !ok {
		return service.Errorf(service.CodeNotFound, "no recording %q", id)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.file != nil {
		return service.Errorf(service.CodeInvalidArgument, "recording %q is running: stop it first", id)
	}
	for i := 0; i < rec.info.Files; i++ {
		if err := os.Remove(st.filePath(id, i)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return service.Wrap(service.CodeInternal, err, "cannot delete the recording")
		}
	}
	if err := os.Remove(st.metaPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return service.Wrap(service.CodeInternal, err, "cannot delete the recording")
	}
	st.used.Add(-rec.info.SizeBytes)
	delete(st.recs, id)
	return nil
}

// open returns the recording's files as one stream, from offset up to the
// size the recording has now.
func (st *recStore) open(id string, offset int64) (io.ReadCloser, model.RecordingInfo, error) {
	rec, err := st.get(id)
	if err != nil {
		return nil, model.RecordingInfo{}, err
	}
	info := rec.snapshot()
	if offset < 0 || offset > info.SizeBytes {
		return nil, info, service.Errorf(service.CodeInvalidArgument, "offset %d is outside the recording (%d bytes)", offset, info.SizeBytes)
	}
	return &recReader{store: st, name: info.Name, files: info.Files, skip: offset, left: info.SizeBytes - offset}, info, nil
}

// recReader reads the files of a recording one after another.
type recReader struct {
	store *recStore
	name  string
	files int
	index int
	skip  int64 // bytes still to skip
	left  int64 // bytes still to deliver
	cur   *os.File
}

func (r *recReader) Read(p []byte) (int, error) {
	for r.left > 0 {
		if r.cur == nil {
			if r.index >= r.files {
				return 0, io.EOF
			}
			f, err := os.Open(r.store.filePath(r.name, r.index))
			if err != nil {
				return 0, err
			}
			r.index++
			if r.skip > 0 {
				fi, err := f.Stat()
				if err != nil {
					_ = f.Close()
					return 0, err
				}
				if fi.Size() <= r.skip {
					r.skip -= fi.Size()
					_ = f.Close()
					continue
				}
				if _, err := f.Seek(r.skip, io.SeekStart); err != nil {
					_ = f.Close()
					return 0, err
				}
				r.skip = 0
			}
			r.cur = f
		}
		if int64(len(p)) > r.left {
			p = p[:r.left]
		}
		n, err := r.cur.Read(p)
		r.left -= int64(n)
		if err == io.EOF {
			_ = r.cur.Close()
			r.cur = nil
			if n == 0 {
				continue
			}
			err = nil
		}
		return n, err
	}
	return 0, io.EOF
}

func (r *recReader) Close() error {
	if r.cur != nil {
		err := r.cur.Close()
		r.cur = nil
		return err
	}
	return nil
}

// stopAll ends every running recording, when the engine closes.
func (st *recStore) stopAll(reason string) {
	st.mu.Lock()
	recs := make([]*recording, 0, len(st.recs))
	for _, r := range st.recs {
		recs = append(recs, r)
	}
	st.mu.Unlock()
	for _, r := range recs {
		r.stop(reason)
	}
}

// recordingHeader describes a session at the start of a recording.
func recordingHeader(info model.SessionInfo, intervalS int) model.RecordingHeader {
	return model.RecordingHeader{
		StartedAt: time.Now().UTC(),
		IntervalS: intervalS,
		App:       model.AppInfo{Name: model.AppName, Version: buildinfo.Version},
		Libs:      model.LibInfo{GoSunspec: buildinfo.GoSunspecVersion(), RegistryModels: registry.Count()},
		Target:    info.Target,
		Device:    info.Device,
		Models:    info.Models,
	}
}
