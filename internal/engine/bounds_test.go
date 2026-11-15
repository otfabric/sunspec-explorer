// SPDX-License-Identifier: MIT

package engine

import (
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// timeout_ms is checked as the caller sent it, before it becomes a
// Duration: a value whose product with a millisecond does not fit is out of
// range, whatever the product would wrap to.
func TestTargetTimeoutBounds(t *testing.T) {
	cases := []struct {
		ms   int64
		want time.Duration // 0: invalid_argument
	}{
		{0, DefaultTimeout},
		{1, 0},
		{99, 0},
		{100, 100 * time.Millisecond},
		{101, 101 * time.Millisecond},
		{59999, 59999 * time.Millisecond},
		{60000, 60 * time.Second},
		{60001, 0},
		{-1, 0},
		{-100, 0},
		{math.MaxInt32, 0},
		{math.MinInt32, 0},
	}
	if strconv.IntSize == 64 {
		cases = append(cases, []struct {
			ms   int64
			want time.Duration
		}{
			{math.MaxInt32 + 1, 0},
			{1000 + 1<<58, 0}, // its product wraps to exactly 1000 ms
			{math.MaxInt64, 0},
			{math.MinInt64, 0},
		}...)
	}
	for _, c := range cases {
		tg, err := parseTarget(model.TargetConfig{URL: "tcp://10.0.0.5", TimeoutMS: int(c.ms)})
		switch {
		case c.want == 0 && service.CodeOf(err) != service.CodeInvalidArgument:
			t.Errorf("timeout_ms %d = %v, %v; want invalid_argument", c.ms, tg.timeout, err)
		case c.want != 0 && (err != nil || tg.timeout != c.want):
			t.Errorf("timeout_ms %d = %v, %v; want %v", c.ms, tg.timeout, err, c.want)
		}
	}
}

// rotate_bytes is 0 for the default, or a size from MinRotateBytes up to the
// size at which a recording is stopped.
func TestRecordingRotateBounds(t *testing.T) {
	d := newSim(t)
	e := recEngine(t, t.TempDir(), RecordingLimits{})
	info := mustOpen(t, e, d)
	max := int64(DefaultMaxRecordingBytes)
	cases := []struct {
		rotate int64
		ok     bool
	}{
		{0, true},
		{1, false},
		{-1, false},
		{MinRotateBytes - 1, false},
		{MinRotateBytes, true},
		{MinRotateBytes + 1, true},
		{max - 1, true},
		{max, true},
		{max + 1, false},
		{math.MaxInt32, false},
		{math.MaxInt64, false},
		{math.MinInt64, false},
	}
	for i, c := range cases {
		name := fmt.Sprintf("rot-%d", i)
		rec, err := e.StartRecording(ctx, info.ID, model.RecordingConfig{Name: name, RotateBytes: c.rotate})
		if !c.ok {
			if service.CodeOf(err) != service.CodeInvalidArgument {
				t.Errorf("rotate_bytes %d = %v, want invalid_argument", c.rotate, err)
			}
			if err == nil {
				_, _ = e.StopRecording(ctx, rec.ID)
			}
			continue
		}
		if err != nil {
			t.Errorf("rotate_bytes %d: %v", c.rotate, err)
			continue
		}
		want := c.rotate
		if want == 0 {
			want = DefaultRotateBytes
		}
		e.recs.mu.Lock()
		r := e.recs.recs[rec.ID]
		e.recs.mu.Unlock()
		r.mu.Lock()
		got := r.rotate
		r.mu.Unlock()
		if got != want {
			t.Errorf("rotate_bytes %d: the recording rotates at %d", c.rotate, got)
		}
		if _, err := e.StopRecording(ctx, rec.ID); err != nil {
			t.Fatal(err)
		}
	}
}
