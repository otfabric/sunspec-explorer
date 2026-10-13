// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/otfabric/go-modbus"
	gmsunspec "github.com/otfabric/go-modbus/sunspec"
	sunspec "github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/registry"

	"github.com/otfabric/sunspec-explorer/internal/buildinfo"
	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/readonly"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// commonModelID is the SunSpec Common model, which identifies the device.
const commonModelID = 1

// session is one device: a target, one Modbus connection, the discovery
// result and at most one poller.
//
// All Modbus traffic of a session is serialized by the io token: whoever
// holds it is the only one talking to the device. dev is read and replaced
// only while holding it. mu guards the remaining fields and is never held
// during I/O.
type session struct {
	id     string
	eng    *Engine
	target *target
	client *modbus.Client

	io  chan struct{}
	dev *sunspec.Device

	mu         sync.Mutex
	info       model.SessionInfo
	lastOK     map[int]time.Time // last good read per chain position
	poll       *poller
	pollState  model.PollState
	lastSample *model.Sample
	rec        *recording // the running recording, if any
}

// open connects, detects, discovers and reads the device identity.
func (e *Engine) open(ctx context.Context, t *target) (*session, error) {
	client, err := modbus.New(modbus.Config{
		URL:         t.display,
		Timeout:     t.timeout,
		DialTimeout: t.timeout,
		Metrics:     e.guard,
	})
	if err != nil {
		return nil, service.Wrap(service.CodeInvalidArgument, err, "target %s", t.display)
	}
	if err := client.Open(); err != nil {
		return nil, service.Wrap(service.CodeConnectFailed, err, "cannot connect to %s", t.display)
	}

	s := &session{
		id:     newID(),
		eng:    e,
		target: t,
		client: client,
		io:     make(chan struct{}, 1),
		lastOK: make(map[int]time.Time),
	}
	s.info = model.SessionInfo{
		ID:       s.id,
		Status:   model.SessionConnected,
		OpenedAt: time.Now().UTC(),
		Target:   model.Target{URL: t.display, UnitID: t.unitID, RegType: regTypeName(t.regType)},
	}
	// Nobody else knows the session yet, but discover expects the token.
	s.io <- struct{}{}
	err = s.discover(ctx)
	s.release()
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return s, nil
}

// acquire takes the I/O token, waiting behind whoever holds it.
func (s *session) acquire(ctx context.Context) error {
	select {
	case s.io <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) release() { <-s.io }

func (s *session) snapshotInfo() model.SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

// startRecording starts recording the session's samples.
func (s *session) startRecording(cfg model.RecordingConfig) (model.RecordingInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.Status == model.SessionClosed {
		return model.RecordingInfo{}, notFound(s.id)
	}
	if s.rec != nil {
		return model.RecordingInfo{}, service.Errorf(service.CodeInvalidArgument,
			"the session is recording already (%s): stop that recording first", s.rec.snapshot().Name)
	}
	interval := 0
	if s.pollState.Active {
		interval = s.pollState.IntervalS
	}
	rec, err := s.eng.recs.start(s.id, cfg, recordingHeader(s.info, interval))
	if err != nil {
		return model.RecordingInfo{}, err
	}
	if !s.pollState.Active {
		rec.event("gap", "polling is not running", nil)
	}
	s.rec = rec
	return rec.snapshot(), nil
}

// recording returns the running recording, or nil.
func (s *session) recording() *recording {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec
}

// dropRecording forgets rec, which has stopped.
func (s *session) dropRecording(rec *recording) {
	s.mu.Lock()
	if s.rec == rec {
		s.rec = nil
	}
	s.mu.Unlock()
}

// record notes an event in the running recording, if there is one.
func (s *session) record(kind, detail string, models []model.ModelInfo) {
	if rec := s.recording(); rec != nil {
		rec.event(kind, detail, models)
	}
}

// close stops polling, ends the recording and closes the connection.
func (s *session) close() {
	s.stopPolling()
	if rec := s.recording(); rec != nil {
		s.eng.endRecording(rec, model.StopSessionClosed)
	}
	// Wait for a running read to finish: it owns the connection.
	s.io <- struct{}{}
	_ = s.client.Close()
	s.release()
	s.mu.Lock()
	s.info.Status = model.SessionClosed
	s.info.StatusDetail = ""
	s.mu.Unlock()
}

// probe looks for the SunSpec marker at each base address in turn and
// reports every attempt, also when no address answers: with a wrong unit ID,
// the usual cause of a failed connect, every probe fails.
func (s *session) probe(ctx context.Context) (base uint16, attempts []model.ProbeAttempt, err error) {
	bases := s.target.bases
	if len(bases) == 0 {
		bases = gmsunspec.DefaultBaseAddresses
	}
	answered := false
	var last error
	for _, b := range bases {
		if ctx.Err() != nil {
			return 0, attempts, ctx.Err()
		}
		var res *sunspec.DetectionResult
		derr := readonly.Do(func() error {
			var e error
			res, e = sunspec.Detect(ctx, s.client, &sunspec.DiscoverOptions{
				UnitID:        s.target.unitID,
				RegType:       s.target.regType,
				BaseAddresses: []uint16{b},
			})
			return e
		})
		if res != nil {
			answered = true
			attempts = append(attempts, attemptsOf(res.Attempts)...)
			if res.Detected {
				return res.BaseAddress, attempts, nil
			}
			continue
		}
		// An exception is an answer too: the device is there, the address
		// is not.
		if deviceAnswered(derr) {
			answered = true
		}
		last = derr
		attempts = append(attempts, model.ProbeAttempt{
			BaseAddress: b,
			RegType:     regTypeName(s.target.regType),
			Error:       errText(derr),
		})
	}
	if answered {
		return 0, attempts, &service.Error{
			Code: service.CodeNotSunSpec,
			Message: fmt.Sprintf("no SunSpec marker at any probed address on %s unit %d",
				s.target.display, s.target.unitID),
			Attempts: attempts,
			Err:      sunspec.ErrNotSunSpec,
		}
	}
	return 0, attempts, &service.Error{
		Code: service.CodeConnectFailed,
		Message: fmt.Sprintf("%s unit %d did not answer any probe (check the unit ID): %s",
			s.target.display, s.target.unitID, errText(last)),
		Attempts: attempts,
		Err:      last,
	}
}

// discover probes for the marker, walks the model chain and reads the device
// identity. The caller holds the I/O token. On failure the session keeps the
// discovery result it had.
func (s *session) discover(ctx context.Context) error {
	base, attempts, err := s.probe(ctx)
	if err != nil {
		return err
	}
	var dev *sunspec.Device
	err = readonly.Do(func() error {
		var e error
		dev, e = sunspec.Discover(ctx, s.client, &sunspec.DiscoverOptions{
			UnitID:        s.target.unitID,
			RegType:       s.target.regType,
			BaseAddresses: []uint16{base},
			MaxModels:     s.target.maxModels,
		})
		return e
	})
	if err != nil {
		return &service.Error{
			Code:     service.CodeDiscoveryFailed,
			Message:  fmt.Sprintf("cannot read the model chain at address %d: %v", base, err),
			Attempts: attempts,
			Err:      err,
		}
	}

	warnings := append([]string{}, dev.Discovery.Warnings...)
	var identity *model.DeviceIdentity
	if inst := dev.ModelByID(commonModelID); inst != nil {
		var dm *sunspec.DecodedModel
		rerr := readonly.Do(func() error {
			var e error
			dm, e = dev.ReadModel(ctx, *inst)
			return e
		})
		if rerr != nil {
			warnings = append(warnings, fmt.Sprintf("device identity not read: %v", rerr))
		} else {
			identity = identityOf(dm)
		}
	}

	s.dev = dev
	s.mu.Lock()
	s.info.Target.BaseAddress = dev.Discovery.BaseAddress
	s.info.Target.RegType = regTypeName(dev.Discovery.RegType)
	s.info.DiscoveredAt = time.Now().UTC()
	s.info.Models = modelInfos(dev.Discovery)
	s.info.Warnings = warnings
	s.info.Attempts = attempts
	s.info.Device = identity
	s.info.Status = model.SessionConnected
	s.info.StatusDetail = ""
	s.lastOK = make(map[int]time.Time)
	s.mu.Unlock()
	return nil
}

// rediscover runs discovery again and announces the result. The caller holds
// the I/O token.
func (s *session) rediscover(ctx context.Context) error {
	if err := s.discover(ctx); err != nil {
		return err
	}
	info := s.snapshotInfo()
	s.record(model.EventDiscoveryChanged, "the model chain was discovered again", info.Models)
	s.eng.log.Info("discovery done", "session", s.id, "models", len(info.Models))
	s.eng.hub.publish(model.Event{Kind: model.EventDiscoveryChanged, SessionID: s.id, Session: &info})
	return nil
}

// readResult is the outcome of reading a set of models.
type readResult struct {
	start    time.Time
	duration time.Duration
	models   []model.ModelResult
	// changed is true when a model no longer matched discovery. Reading
	// stopped there.
	changed bool
}

// read reads the models at the given chain positions one after another. The
// caller holds the I/O token. A model that fails does not stop the others;
// a model that changed since discovery does, because the layout of everything
// after it is unknown.
func (s *session) read(ctx context.Context, indexes []int, full, includeRaw bool) readResult {
	res := readResult{start: time.Now(), models: make([]model.ModelResult, 0, len(indexes))}
	instances := s.dev.Discovery.Models
	transportFailures, answered := 0, 0
	var lastErr error

	for _, idx := range indexes {
		if ctx.Err() != nil {
			break
		}
		inst := instances[idx]
		mr := model.ModelResult{
			Index:        idx,
			ID:           inst.Header.ID,
			Name:         inst.Name,
			StartAddress: inst.Header.StartAddress,
			Length:       inst.Header.Length,
			SchemaKnown:  inst.SchemaKnown,
			Status:       model.ModelOK,
			Warnings:     []string{},
		}
		t0 := time.Now()
		var dm *sunspec.DecodedModel
		err := readonly.Do(func() error {
			var e error
			dm, e = s.dev.ReadModel(ctx, inst)
			return e
		})
		mr.ReadDurationMS = time.Since(t0).Milliseconds()

		switch {
		case err == nil:
			answered++
			mr.Warnings = append(mr.Warnings, dm.Warnings...)
			if dm.Group != nil {
				mr.Group = groupView(dm.Group, schemaGroup(dm.Schema), inst.Header.StartAddress, full)
				if includeRaw {
					mr.RawRegisters = dataRegisters(dm.RawRegisters)
				}
			} else {
				mr.RawRegisters = dm.RawRegisters
			}
		case errors.Is(err, sunspec.ErrModelChanged):
			answered++
			mr.Status = model.ModelChanged
			res.changed = true
		case errors.Is(err, modbus.ErrIllegalDataAddress) && s.headerGone(ctx, inst):
			answered++
			mr.Status = model.ModelChanged
			res.changed = true
			err = fmt.Errorf("%w: %w", sunspec.ErrModelChanged, err)
		default:
			mr.Status = model.ModelError
			if deviceAnswered(err) {
				answered++
			} else {
				transportFailures++
				lastErr = err
			}
		}
		if err != nil {
			text := err.Error()
			mr.Error = &text
		}
		res.models = append(res.models, mr)
		if res.changed {
			break
		}
	}
	res.duration = time.Since(res.start)
	s.account(&res, answered, transportFailures, lastErr)
	return res
}

// headerGone reports whether the device no longer holds the header of inst
// where discovery found it. A chain that became shorter does not answer a
// model read with other data but with "illegal data address", which go-sunspec
// reports as a plain read error; the header tells the two apart. The caller
// holds the I/O token.
func (s *session) headerGone(ctx context.Context, inst sunspec.ModelInstance) bool {
	var regs []uint16
	err := readonly.Do(func() error {
		var e error
		regs, e = s.client.ReadRegisters(ctx, s.dev.UnitID, inst.Header.StartAddress, 2, s.dev.RegType)
		return e
	})
	if err != nil {
		return errors.Is(err, modbus.ErrIllegalDataAddress)
	}
	return len(regs) != 2 || regs[0] != inst.Header.ID || regs[1] != inst.Header.Length
}

// account records the good reads, marks stale models and updates the
// connection status from what the read showed.
func (s *session) account(res *readResult, answered, transportFailures int, lastErr error) {
	now := time.Now().UTC()
	var statusEvent *model.SessionInfo

	s.mu.Lock()
	staleAfter := 2 * time.Duration(s.pollState.IntervalS) * s.eng.second
	for i := range res.models {
		mr := &res.models[i]
		if mr.Status == model.ModelOK {
			s.lastOK[mr.Index] = now
		}
		if at, ok := s.lastOK[mr.Index]; ok {
			at := at
			mr.LastOKAt = &at
		}
		if mr.Status != model.ModelOK && s.pollState.Active {
			mr.Stale = mr.LastOKAt == nil || now.Sub(*mr.LastOKAt) > staleAfter
		}
	}
	status, detail := s.info.Status, s.info.StatusDetail
	switch {
	case answered > 0:
		status, detail = model.SessionConnected, ""
	case transportFailures > 0:
		status, detail = model.SessionReconnecting, errText(lastErr)
	}
	if status != s.info.Status && s.info.Status != model.SessionClosed {
		s.info.Status, s.info.StatusDetail = status, detail
		info := s.info
		statusEvent = &info
	}
	s.mu.Unlock()

	if statusEvent != nil {
		s.record(statusEvent.Status, statusEvent.StatusDetail, nil)
		s.eng.log.Warn("session status", "session", s.id, "status", statusEvent.Status, "detail", statusEvent.StatusDetail)
		s.eng.hub.publish(model.Event{Kind: model.EventSessionStatus, SessionID: s.id, Session: statusEvent})
	}
}

// snapshot reads models once, queued behind a running poll cycle.
func (s *session) snapshot(ctx context.Context, req model.SnapshotRequest) (model.Snapshot, error) {
	full, err := metadataLevel(req.Metadata)
	if err != nil {
		return model.Snapshot{}, err
	}
	if err := s.acquire(ctx); err != nil {
		return model.Snapshot{}, err
	}
	defer s.release()

	indexes, err := s.resolveIndexes(req.ModelIndexes)
	if err != nil {
		return model.Snapshot{}, err
	}
	res := s.read(ctx, indexes, full, req.IncludeRaw)
	if err := ctx.Err(); err != nil {
		return model.Snapshot{}, err
	}

	var warnings []string
	if res.changed {
		warnings = append(warnings, "the model chain changed since discovery: models after the changed one were not read")
		if derr := s.rediscover(ctx); derr != nil {
			warnings = append(warnings, fmt.Sprintf("re-discovery failed: %v", derr))
		} else {
			warnings = append(warnings, "the device was discovered again: read once more")
		}
	}

	info := s.snapshotInfo()
	snap := model.Snapshot{
		Schema:         model.SnapshotSchema,
		App:            model.AppInfo{Name: model.AppName, Version: buildinfo.Version},
		Libs:           model.LibInfo{GoSunspec: buildinfo.GoSunspecVersion(), RegistryModels: registry.Count()},
		SessionID:      s.id,
		CapturedAt:     res.start.UTC(),
		ReadDurationMS: res.duration.Milliseconds(),
		Metadata:       levelName(full),
		Target:         info.Target,
		Device:         info.Device,
		Models:         res.models,
		Warnings:       append(append([]string{}, info.Warnings...), warnings...),
	}
	return snap, nil
}

// resolveIndexes validates chain positions against the current discovery
// result; none selects every model. The caller holds the I/O token.
func (s *session) resolveIndexes(indexes []int) ([]int, error) {
	n := len(s.dev.Discovery.Models)
	if len(indexes) == 0 {
		all := make([]int, n)
		for i := range all {
			all[i] = i
		}
		return all, nil
	}
	seen := make(map[int]bool, len(indexes))
	out := make([]int, 0, len(indexes))
	for _, i := range indexes {
		if i < 0 || i >= n {
			return nil, service.Errorf(service.CodeInvalidArgument, "model index %d is out of range (0..%d)", i, n-1)
		}
		if !seen[i] {
			seen[i] = true
			out = append(out, i)
		}
	}
	return out, nil
}

func (s *session) state() model.SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := model.SessionState{Session: s.info, Polling: s.pollState}
	if st.Polling.ModelIndexes == nil {
		st.Polling.ModelIndexes = []int{}
	}
	if s.lastSample != nil {
		sample := *s.lastSample
		st.LastSample = &sample
	}
	if s.rec != nil {
		info := s.rec.snapshot()
		st.Recording = &info
	}
	return st
}

func metadataLevel(level string) (full bool, err error) {
	switch level {
	case "", model.MetadataFull:
		return true, nil
	case model.MetadataCompact:
		return false, nil
	}
	return false, service.Errorf(service.CodeInvalidArgument, "metadata level %q: use %q or %q",
		level, model.MetadataFull, model.MetadataCompact)
}

func levelName(full bool) string {
	if full {
		return model.MetadataFull
	}
	return model.MetadataCompact
}

func schemaGroup(m *registry.ModelMeta) *registry.GroupMeta {
	if m == nil {
		return nil
	}
	return m.Group
}

// dataRegisters strips the ID and L registers a decoded model carries in
// front of its data, so that raw registers always mean the data registers.
func dataRegisters(regs []uint16) []uint16 {
	if len(regs) < 2 {
		return nil
	}
	return regs[2:]
}

// exceptions are the errors that prove the device answered: the connection is
// fine, the request was refused.
var exceptions = []error{
	modbus.ErrIllegalFunction, modbus.ErrIllegalDataAddress, modbus.ErrIllegalDataValue,
	modbus.ErrServerDeviceFailure, modbus.ErrAcknowledge, modbus.ErrServerDeviceBusy,
	modbus.ErrMemoryParityError,
}

// deviceAnswered reports whether err is an answer from the device (a Modbus
// exception, or data that could not be decoded) rather than a failure to
// reach it.
func deviceAnswered(err error) bool {
	for _, e := range exceptions {
		if errors.Is(err, e) {
			return true
		}
	}
	var de *sunspec.DecodeError
	return errors.As(err, &de)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
