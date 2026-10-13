// SPDX-License-Identifier: MIT

// Package engine is the in-process implementation of service.Service: it owns
// the sessions, their Modbus connections, polling and the event stream.
//
// The engine only reads. Every Modbus client it creates carries the
// readonly.Guard, and every call into go-sunspec runs inside readonly.Do.
package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/otfabric/go-sunspec/registry"

	"github.com/otfabric/sunspec-explorer/internal/buildinfo"
	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/readonly"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// Defaults and bounds.
const (
	DefaultMaxSessions = 4
	DefaultEventBuffer = 1024
	DefaultTimeout     = 3 * time.Second
	DefaultIntervalS   = 5
	MinIntervalS       = 1
	MaxIntervalS       = 3600

	minTimeout = 100 * time.Millisecond
	maxTimeout = 60 * time.Second

	backoffMin = 1 * time.Second
	backoffMax = 30 * time.Second
)

// Options configures an Engine. The zero value selects the defaults.
type Options struct {
	// MaxSessions limits the number of open sessions.
	MaxSessions int
	// EventBuffer is the number of events kept for replay.
	EventBuffer int
	// Logger receives structured logs. Nil discards them.
	Logger *slog.Logger
	// AllowTarget, when set, decides whether the engine may connect to a
	// target URL. The daemon installs its allow-list here.
	AllowTarget func(u *url.URL) error
	// DataDir is the directory recordings are kept in (under "recordings").
	// Without one the engine does not record. The caller has created and
	// checked it.
	DataDir string
	// DataDirTemporary says that DataDir is ephemeral storage; recordings
	// then carry a warning.
	DataDirTemporary bool
	// Recording bounds what recordings may use.
	Recording RecordingLimits
}

// Engine implements service.Service in process.
type Engine struct {
	opts    Options
	log     *slog.Logger
	started time.Time
	hub     *hub
	guard   *readonly.Guard
	// recs is nil when the engine has no data directory.
	recs *recStore

	// second is the unit of poll intervals and backoff delays. It is one
	// second; tests shorten it.
	second time.Duration

	mu       sync.Mutex
	sessions map[string]*session
	order    []string
	opening  int
	closed   bool
}

var _ service.Service = (*Engine)(nil)

// New creates an Engine. A data directory that cannot be used for recordings
// is reported in the log and leaves the engine without recording.
func New(opts Options) *Engine {
	if opts.MaxSessions <= 0 {
		opts.MaxSessions = DefaultMaxSessions
	}
	if opts.EventBuffer <= 0 {
		opts.EventBuffer = DefaultEventBuffer
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	e := &Engine{
		opts:     opts,
		log:      log,
		started:  time.Now(),
		hub:      newHub(opts.EventBuffer),
		guard:    &readonly.Guard{},
		second:   time.Second,
		sessions: make(map[string]*session),
	}
	if opts.DataDir != "" {
		recs, err := newRecStore(opts.DataDir, opts.DataDirTemporary, opts.Recording, log)
		if err != nil {
			log.Error("recording is not available", "error", err)
		}
		e.recs = recs
	}
	return e
}

// Guard returns the read-only guard of the engine's Modbus clients.
func (e *Engine) Guard() *readonly.Guard { return e.guard }

// Close closes every session. The engine cannot be used afterwards.
func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	sessions := make([]*session, 0, len(e.sessions))
	for _, s := range e.sessions {
		sessions = append(sessions, s)
	}
	e.mu.Unlock()
	// Recordings end with the engine, not with their sessions: the footer
	// says "shutdown".
	if e.recs != nil {
		e.recs.stopAll(model.StopShutdown)
	}
	for _, s := range sessions {
		_ = e.CloseSession(context.Background(), s.id)
	}
}

// Health implements service.Service.
func (e *Engine) Health(context.Context) (model.Health, error) {
	return model.Health{
		Status:         "ok",
		Version:        buildinfo.Version,
		APIVersion:     model.APIVersion,
		UptimeS:        int64(time.Since(e.started).Seconds()),
		GoSunspec:      buildinfo.GoSunspecVersion(),
		RegistryModels: registry.Count(),
		Time:           time.Now().UTC(),
	}, nil
}

// OpenSession implements service.Service.
func (e *Engine) OpenSession(ctx context.Context, cfg model.TargetConfig) (model.SessionInfo, error) {
	t, err := parseTarget(cfg)
	if err != nil {
		return model.SessionInfo{}, err
	}
	if e.opts.AllowTarget != nil {
		if err := e.opts.AllowTarget(t.url); err != nil {
			return model.SessionInfo{}, service.Wrap(service.CodeNotAllowed, err, "target %s", t.display)
		}
	}

	e.mu.Lock()
	switch {
	case e.closed:
		e.mu.Unlock()
		return model.SessionInfo{}, service.Errorf(service.CodeInternal, "engine is closed")
	case len(e.sessions)+e.opening >= e.opts.MaxSessions:
		e.mu.Unlock()
		return model.SessionInfo{}, service.Errorf(service.CodeLimitExceeded,
			"session limit reached (%d): close a session first", e.opts.MaxSessions)
	}
	e.opening++
	e.mu.Unlock()

	s, err := e.open(ctx, t)

	e.mu.Lock()
	e.opening--
	if err == nil && e.closed {
		err = service.Errorf(service.CodeInternal, "engine is closed")
		_ = s.client.Close()
	}
	if err != nil {
		e.mu.Unlock()
		return model.SessionInfo{}, err
	}
	e.sessions[s.id] = s
	e.order = append(e.order, s.id)
	e.mu.Unlock()

	info := s.snapshotInfo()
	e.log.Info("session opened", "session", s.id, "target", t.display, "unit", t.unitID, "models", len(info.Models))
	e.hub.publish(model.Event{Kind: model.EventSessionStatus, SessionID: s.id, Session: &info})
	return info, nil
}

// CloseSession implements service.Service.
func (e *Engine) CloseSession(_ context.Context, id string) error {
	e.mu.Lock()
	s, ok := e.sessions[id]
	if ok {
		delete(e.sessions, id)
		for i, sid := range e.order {
			if sid == id {
				e.order = append(e.order[:i], e.order[i+1:]...)
				break
			}
		}
	}
	e.mu.Unlock()
	if !ok {
		return notFound(id)
	}
	s.close()
	info := s.snapshotInfo()
	e.log.Info("session closed", "session", id)
	e.hub.publish(model.Event{Kind: model.EventSessionStatus, SessionID: id, Session: &info})
	return nil
}

// Sessions implements service.Service.
func (e *Engine) Sessions(context.Context) ([]model.SessionInfo, error) {
	e.mu.Lock()
	sessions := make([]*session, 0, len(e.order))
	for _, id := range e.order {
		sessions = append(sessions, e.sessions[id])
	}
	e.mu.Unlock()
	out := make([]model.SessionInfo, len(sessions))
	for i, s := range sessions {
		out[i] = s.snapshotInfo()
	}
	return out, nil
}

// Rediscover implements service.Service.
func (e *Engine) Rediscover(ctx context.Context, id string) (model.SessionInfo, error) {
	s, err := e.session(id)
	if err != nil {
		return model.SessionInfo{}, err
	}
	if err := s.acquire(ctx); err != nil {
		return model.SessionInfo{}, err
	}
	defer s.release()
	if err := s.rediscover(ctx); err != nil {
		return model.SessionInfo{}, err
	}
	return s.snapshotInfo(), nil
}

// Snapshot implements service.Service.
func (e *Engine) Snapshot(ctx context.Context, id string, req model.SnapshotRequest) (model.Snapshot, error) {
	s, err := e.session(id)
	if err != nil {
		return model.Snapshot{}, err
	}
	return s.snapshot(ctx, req)
}

// StartPolling implements service.Service.
func (e *Engine) StartPolling(_ context.Context, id string, cfg model.PollConfig) error {
	s, err := e.session(id)
	if err != nil {
		return err
	}
	return s.startPolling(cfg)
}

// StopPolling implements service.Service.
func (e *Engine) StopPolling(_ context.Context, id string) error {
	s, err := e.session(id)
	if err != nil {
		return err
	}
	s.stopPolling()
	return nil
}

// State implements service.Service.
func (e *Engine) State(_ context.Context, id string) (model.SessionState, error) {
	s, err := e.session(id)
	if err != nil {
		return model.SessionState{}, err
	}
	return s.state(), nil
}

// Subscribe implements service.Service.
func (e *Engine) Subscribe(ctx context.Context, sinceSeq uint64) (<-chan model.Event, error) {
	return e.hub.subscribe(ctx, sinceSeq), nil
}

// StartRecording implements service.Service.
func (e *Engine) StartRecording(_ context.Context, id string, cfg model.RecordingConfig) (model.RecordingInfo, error) {
	s, err := e.session(id)
	if err != nil {
		return model.RecordingInfo{}, err
	}
	if e.recs == nil {
		return model.RecordingInfo{}, errNoDataDir()
	}
	info, err := s.startRecording(cfg)
	if err != nil {
		return model.RecordingInfo{}, err
	}
	e.log.Info("recording started", "session", id, "recording", info.Name)
	e.hub.publish(model.Event{Kind: model.EventRecordingStatus, SessionID: id, Recording: &info})
	return info, nil
}

// StopRecording implements service.Service. Stopping a recording that is not
// running returns it as it is.
func (e *Engine) StopRecording(_ context.Context, recID string) (model.RecordingInfo, error) {
	if e.recs == nil {
		return model.RecordingInfo{}, errNoDataDir()
	}
	rec, err := e.recs.get(recID)
	if err != nil {
		return model.RecordingInfo{}, err
	}
	if !rec.running() {
		return rec.snapshot(), nil
	}
	e.endRecording(rec, model.StopUser)
	return rec.snapshot(), nil
}

// endRecording stops rec, releases it from its session and announces it.
func (e *Engine) endRecording(rec *recording, reason string) {
	info := rec.stop(reason)
	e.mu.Lock()
	s := e.sessions[info.SessionID]
	e.mu.Unlock()
	if s != nil {
		s.dropRecording(rec)
	}
	e.log.Info("recording stopped", "recording", info.Name, "reason", info.StopReason, "samples", info.Samples)
	e.hub.publish(model.Event{Kind: model.EventRecordingStatus, SessionID: info.SessionID, Recording: &info})
}

// Recordings implements service.Service.
func (e *Engine) Recordings(context.Context) ([]model.RecordingInfo, error) {
	if e.recs == nil {
		return []model.RecordingInfo{}, nil
	}
	return e.recs.list(), nil
}

// OpenRecording implements service.Service.
func (e *Engine) OpenRecording(_ context.Context, recID string, offset int64) (io.ReadCloser, model.RecordingInfo, error) {
	if e.recs == nil {
		return nil, model.RecordingInfo{}, errNoDataDir()
	}
	return e.recs.open(recID, offset)
}

// DeleteRecording implements service.Service.
func (e *Engine) DeleteRecording(_ context.Context, recID string) error {
	if e.recs == nil {
		return errNoDataDir()
	}
	if err := e.recs.remove(recID); err != nil {
		return err
	}
	e.log.Info("recording deleted", "recording", recID)
	return nil
}

// SerialPorts implements service.Service. Serial targets are not supported yet.
func (e *Engine) SerialPorts(context.Context) ([]model.SerialPort, error) {
	return []model.SerialPort{}, nil
}

func errNoDataDir() error {
	return service.Errorf(service.CodeNotAllowed, "this engine has no data directory: it cannot record")
}

func notFound(id string) error {
	return service.Errorf(service.CodeNotFound, "no session %q", id)
}

func (e *Engine) session(id string) (*session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[id]
	if !ok {
		return nil, notFound(id)
	}
	return s, nil
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // never fails (crypto/rand, Go 1.24+)
	return hex.EncodeToString(b[:])
}
