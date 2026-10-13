// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// poller is the goroutine that polls one session.
type poller struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startPolling starts polling, replacing a running poller.
func (s *session) startPolling(cfg model.PollConfig) error {
	if cfg.IntervalS == 0 {
		cfg.IntervalS = DefaultIntervalS
	}
	if cfg.IntervalS < MinIntervalS || cfg.IntervalS > MaxIntervalS {
		return service.Errorf(service.CodeInvalidArgument, "poll interval %d s is out of range (%d..%d)",
			cfg.IntervalS, MinIntervalS, MaxIntervalS)
	}
	s.mu.Lock()
	n := len(s.info.Models)
	closed := s.info.Status == model.SessionClosed
	s.mu.Unlock()
	if closed {
		return notFound(s.id)
	}
	for _, i := range cfg.ModelIndexes {
		if i < 0 || i >= n {
			return service.Errorf(service.CodeInvalidArgument, "model index %d is out of range (0..%d)", i, n-1)
		}
	}

	s.stopPolling()

	ctx, cancel := context.WithCancel(context.Background())
	p := &poller{cancel: cancel, done: make(chan struct{})}
	now := time.Now().UTC()
	s.mu.Lock()
	s.poll = p
	s.pollState = model.PollState{
		Active:       true,
		IntervalS:    cfg.IntervalS,
		ModelIndexes: append([]int{}, cfg.ModelIndexes...),
		StartedAt:    &now,
	}
	s.lastSample = nil
	s.mu.Unlock()

	s.record("polling_started", fmt.Sprintf("interval %d s", cfg.IntervalS), nil)
	s.eng.log.Info("polling started", "session", s.id, "interval_s", cfg.IntervalS)
	go s.runPoll(ctx, p, cfg)
	return nil
}

// stopPolling stops the poller and waits until its cycle has ended.
func (s *session) stopPolling() {
	s.mu.Lock()
	p := s.poll
	s.poll = nil
	s.pollState.Active = false
	s.mu.Unlock()
	if p == nil {
		return
	}
	p.cancel()
	<-p.done
	s.record("gap", "polling stopped", nil)
	s.eng.log.Info("polling stopped", "session", s.id)
}

// runPoll runs poll cycles until ctx ends.
//
// Timing is start to start: a cycle is due every interval, counted from the
// first one. A cycle that is still running when the next ones are due makes
// them be skipped and counted as overruns; there is no catching up. While the
// device does not answer, cycles are spaced by a growing backoff instead.
func (s *session) runPoll(ctx context.Context, p *poller, cfg model.PollConfig) {
	defer close(p.done)

	interval := time.Duration(cfg.IntervalS) * s.eng.second
	timer := time.NewTimer(0)
	defer timer.Stop()
	due := time.Now()
	var backoff time.Duration
	var seq uint64

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		seq++
		start := time.Now()
		s.cycle(ctx, cfg, seq)
		if ctx.Err() != nil {
			return
		}

		if s.snapshotInfo().Status == model.SessionReconnecting {
			backoff = min(max(2*backoff, s.backoffUnit(backoffMin)), s.backoffUnit(backoffMax))
		} else {
			backoff = 0
		}
		if backoff > interval {
			due = start.Add(backoff)
		} else {
			due = due.Add(interval)
			if now := time.Now(); now.After(due) {
				skipped := now.Sub(due)/interval + 1
				due = due.Add(skipped * interval)
				s.mu.Lock()
				s.pollState.Overruns += uint64(skipped)
				s.mu.Unlock()
			}
		}
		timer.Reset(time.Until(due))
	}
}

// backoffUnit scales a backoff bound to the engine's unit of time.
func (s *session) backoffUnit(d time.Duration) time.Duration {
	return d / time.Second * s.eng.second
}

// cycle reads the poll scope once and publishes the sample.
func (s *session) cycle(ctx context.Context, cfg model.PollConfig, seq uint64) {
	if err := s.acquire(ctx); err != nil {
		return
	}
	defer s.release()

	res := s.read(ctx, s.pollScope(cfg, seq == 1), false, false)
	if ctx.Err() != nil {
		return
	}

	failed := false
	for i := range res.models {
		if res.models[i].Status != model.ModelOK {
			failed = true
		}
	}

	s.mu.Lock()
	s.pollState.Cycles++
	if failed {
		s.pollState.Failures++
	}
	at := res.start.UTC()
	s.pollState.LastSampleAt = &at
	sample := model.Sample{
		SessionID:      s.id,
		Seq:            seq,
		CapturedAt:     at,
		ReadDurationMS: res.duration.Milliseconds(),
		Overruns:       s.pollState.Overruns,
		Metadata:       model.MetadataCompact,
		Models:         res.models,
	}
	s.lastSample = &sample
	s.mu.Unlock()

	// The sample goes to the recording before it is announced, so that what
	// a client sees is on disk already. The event carries the counters.
	ev := model.Event{Kind: model.EventSample, SessionID: s.id, At: at, Sample: &sample}
	if rec := s.recording(); rec != nil {
		info, stopped := rec.sample(&sample)
		ev.Recording = &info
		if stopped {
			s.dropRecording(rec)
			defer func() {
				s.eng.log.Warn("recording stopped by the engine", "recording", info.Name, "reason", info.StopReason)
				s.eng.hub.publish(model.Event{Kind: model.EventRecordingStatus, SessionID: s.id, Recording: &info})
			}()
		}
	}
	s.eng.hub.publish(ev)

	if res.changed {
		// The sample above shows the gap; now find out what the device is.
		if err := s.rediscover(ctx); err != nil && ctx.Err() == nil {
			s.eng.log.Warn("re-discovery failed", "session", s.id, "error", err)
			s.eng.hub.publish(model.Event{
				Kind:      model.EventError,
				SessionID: s.id,
				Message:   fmt.Sprintf("the model chain changed and re-discovery failed: %v", err),
			})
		}
	}
}

// pollScope returns the chain positions of a cycle. The caller holds the I/O
// token.
//
// An explicit selection is used as it is, minus positions a re-discovery
// removed. Without one, the scope is every model that is not static-only,
// and the first cycle reads the static-only ones too, once.
func (s *session) pollScope(cfg model.PollConfig, first bool) []int {
	instances := s.dev.Discovery.Models
	if len(cfg.ModelIndexes) > 0 {
		out := make([]int, 0, len(cfg.ModelIndexes))
		seen := make(map[int]bool, len(cfg.ModelIndexes))
		for _, i := range cfg.ModelIndexes {
			if i < len(instances) && !seen[i] {
				seen[i] = true
				out = append(out, i)
			}
		}
		return out
	}
	all := make([]int, 0, len(instances))
	dynamic := make([]int, 0, len(instances))
	for i, inst := range instances {
		all = append(all, i)
		if inst.Schema == nil || !staticOnly(inst.Schema.Group) {
			dynamic = append(dynamic, i)
		}
	}
	if first || len(dynamic) == 0 {
		return all
	}
	return dynamic
}
