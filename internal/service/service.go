// SPDX-License-Identifier: MIT

// Package service defines the one interface the user interfaces talk to, and
// its errors. The in-process engine implements it, and so does the HTTP client
// of the daemon; both run the same contract tests.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/otfabric/sunspec-explorer/internal/model"
)

// Service is the read-only SunSpec engine as the user interfaces see it.
//
// Nothing in it takes a file path, a Modbus frame, a function code or a value
// to write.
type Service interface {
	// Health reports versions and uptime.
	Health(ctx context.Context) (model.Health, error)
	// OpenSession connects to the target, detects SunSpec and discovers the
	// model chain. A failure carries the probe attempts (see Error.Attempts).
	OpenSession(ctx context.Context, cfg model.TargetConfig) (model.SessionInfo, error)
	// CloseSession stops polling and closes the connection.
	CloseSession(ctx context.Context, id string) error
	// Sessions lists the open sessions.
	Sessions(ctx context.Context) ([]model.SessionInfo, error)
	// Rediscover walks the model chain again.
	Rediscover(ctx context.Context, id string) (model.SessionInfo, error)
	// Snapshot reads models once. It is queued behind a running poll cycle.
	Snapshot(ctx context.Context, id string, req model.SnapshotRequest) (model.Snapshot, error)
	// StartPolling starts polling, replacing a running poller.
	StartPolling(ctx context.Context, id string, cfg model.PollConfig) error
	// StopPolling stops polling. Stopping a session that is not polling is
	// not an error.
	StopPolling(ctx context.Context, id string) error
	// State returns the last sample and the counters of a session.
	State(ctx context.Context, id string) (model.SessionState, error)
	// Subscribe returns the event stream. Events with a sequence number above
	// sinceSeq that the engine still holds are replayed first; sinceSeq 0
	// delivers new events only. The channel is closed when ctx ends.
	Subscribe(ctx context.Context, sinceSeq uint64) (<-chan model.Event, error)
	// StartRecording starts recording the samples of a session.
	StartRecording(ctx context.Context, id string, cfg model.RecordingConfig) (model.RecordingInfo, error)
	// StopRecording stops a recording.
	StopRecording(ctx context.Context, recID string) (model.RecordingInfo, error)
	// Recordings lists the recordings in the data directory.
	Recordings(ctx context.Context) ([]model.RecordingInfo, error)
	// OpenRecording opens a recording for reading from offset, as one stream
	// over all its files. A running recording is read up to its size at the
	// time of the call; SizeBytes of the returned info is that size.
	OpenRecording(ctx context.Context, recID string, offset int64) (io.ReadCloser, model.RecordingInfo, error)
	// DeleteRecording removes a recording that is not running.
	DeleteRecording(ctx context.Context, recID string) error
	// SerialPorts lists the serial devices the engine may open.
	SerialPorts(ctx context.Context) ([]model.SerialPort, error)
}

// Error codes. They are part of the API: clients switch on them.
const (
	CodeInvalidArgument = "invalid_argument"
	CodeNotFound        = "not_found"
	CodeLimitExceeded   = "limit_exceeded"
	CodeNotAllowed      = "not_allowed"
	// CodeConnectFailed means the device could not be reached.
	CodeConnectFailed = "connect_failed"
	// CodeNotSunSpec means the device answered but no probed address holds
	// the SunSpec marker.
	CodeNotSunSpec = "not_sunspec"
	// CodeDiscoveryFailed means the model chain could not be walked.
	CodeDiscoveryFailed = "discovery_failed"
	CodeNotImplemented  = "not_implemented"
	// CodeUnauthorized means the request carried no valid bearer token, or a
	// Host header the daemon does not answer to.
	CodeUnauthorized = "unauthorized"
	// CodeCanceled means the caller gave up before the call finished.
	CodeCanceled = "canceled"
	CodeInternal = "internal"
)

// Error is the error type of every Service method.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Attempts holds the detection probes of a failed OpenSession or
	// Rediscover.
	Attempts []model.ProbeAttempt `json:"attempts,omitempty"`
	// Err is the underlying error, for errors.Is in the same process. It does
	// not cross the HTTP API.
	Err error `json:"-"`
}

// Error returns the message.
func (e *Error) Error() string { return e.Message }

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error { return e.Err }

// Errorf creates an Error with a formatted message.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap creates an Error from err with a message prefix.
func Wrap(code string, err error, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...) + ": " + err.Error(), Err: err}
}

// CodeOf returns the code of err, CodeInternal for an error that is not an
// *Error, and "" for nil.
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}
