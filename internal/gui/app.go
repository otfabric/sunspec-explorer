// SPDX-License-Identifier: MIT

// Package gui binds the in-process engine to the Wails window: the frontend
// calls the methods of App, and App forwards the engine's events to the
// window.
//
// The desktop application is Local only. Remote use is the daemon with a
// browser; nothing here talks to a daemon or holds a token.
package gui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

// EventName is the name of the Wails event that carries a model.Event.
const EventName = "explorer:event"

// App is the Wails service of the Explorer. Its exported methods are the
// frontend's whole view of the backend.
type App struct {
	svc service.Service

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates the Wails service for svc.
func New(svc service.Service) *App {
	return &App{svc: svc}
}

// MarshalError encodes the error of a bound method for the frontend as
// {"code","message","attempts"}.
func MarshalError(err error) []byte {
	var se *service.Error
	if !errors.As(err, &se) {
		se = &service.Error{Code: service.CodeInternal, Message: err.Error()}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			se.Code = service.CodeCanceled
		}
	}
	b, merr := json.Marshal(se)
	if merr != nil {
		return nil
	}
	return b
}

// ServiceStartup starts forwarding events to the window.
func (a *App) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	ctx, cancel := context.WithCancel(context.Background())
	events, err := a.svc.Subscribe(ctx, 0)
	if err != nil {
		cancel()
		return err
	}
	done := make(chan struct{})
	a.mu.Lock()
	a.cancel, a.done = cancel, done
	a.mu.Unlock()
	go func() {
		defer close(done)
		for ev := range events {
			if app := application.Get(); app != nil {
				app.Event.Emit(EventName, ev)
			}
		}
	}()
	return nil
}

// ServiceShutdown stops forwarding events.
func (a *App) ServiceShutdown() error {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.cancel, a.done = nil, nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	return nil
}

// Health returns the engine's versions and uptime.
func (a *App) Health(ctx context.Context) (model.Health, error) { return a.svc.Health(ctx) }

// OpenSession connects to a device and discovers its models.
func (a *App) OpenSession(ctx context.Context, cfg model.TargetConfig) (model.SessionInfo, error) {
	return a.svc.OpenSession(ctx, cfg)
}

// CloseSession closes a session.
func (a *App) CloseSession(ctx context.Context, id string) error {
	return a.svc.CloseSession(ctx, id)
}

// Sessions lists the open sessions.
func (a *App) Sessions(ctx context.Context) ([]model.SessionInfo, error) {
	return a.svc.Sessions(ctx)
}

// Rediscover walks the model chain of a session again.
func (a *App) Rediscover(ctx context.Context, id string) (model.SessionInfo, error) {
	return a.svc.Rediscover(ctx, id)
}

// Snapshot reads models once.
func (a *App) Snapshot(ctx context.Context, id string, req model.SnapshotRequest) (model.Snapshot, error) {
	return a.svc.Snapshot(ctx, id, req)
}

// StartPolling starts polling a session.
func (a *App) StartPolling(ctx context.Context, id string, cfg model.PollConfig) error {
	return a.svc.StartPolling(ctx, id, cfg)
}

// StopPolling stops polling a session.
func (a *App) StopPolling(ctx context.Context, id string) error {
	return a.svc.StopPolling(ctx, id)
}

// State returns the last sample and the counters of a session.
func (a *App) State(ctx context.Context, id string) (model.SessionState, error) {
	return a.svc.State(ctx, id)
}

// StartRecording starts recording a session.
func (a *App) StartRecording(ctx context.Context, id string, cfg model.RecordingConfig) (model.RecordingInfo, error) {
	return a.svc.StartRecording(ctx, id, cfg)
}

// StopRecording stops a recording.
func (a *App) StopRecording(ctx context.Context, recID string) (model.RecordingInfo, error) {
	return a.svc.StopRecording(ctx, recID)
}

// Recordings lists the recordings.
func (a *App) Recordings(ctx context.Context) ([]model.RecordingInfo, error) {
	return a.svc.Recordings(ctx)
}

// DeleteRecording removes a recording that is not running.
func (a *App) DeleteRecording(ctx context.Context, recID string) error {
	return a.svc.DeleteRecording(ctx, recID)
}

// SaveRecording asks the user where to save a copy of a recording and writes
// it there. It returns the chosen path, or "" when the user cancelled. The
// frontend names the recording; the path comes from the native dialog.
func (a *App) SaveRecording(ctx context.Context, recID string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", service.Errorf(service.CodeInternal, "no application")
	}
	src, info, err := a.svc.OpenRecording(ctx, recID, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()
	path, err := app.Dialog.SaveFile().SetFilename(filepath.Base(info.Name)+".jsonl").CanCreateDirectories(true).
		AddFilter("JSON Lines", "*.jsonl").PromptForSingleSelection()
	if err != nil {
		return "", service.Wrap(service.CodeInternal, err, "save dialog")
	}
	if path == "" {
		return "", nil
	}
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", service.Wrap(service.CodeInternal, err, "cannot save the recording")
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return "", service.Wrap(service.CodeInternal, err, "cannot save the recording")
	}
	if err := dst.Close(); err != nil {
		return "", service.Wrap(service.CodeInternal, err, "cannot save the recording")
	}
	return path, nil
}

// SerialPorts lists the serial devices the engine may open.
func (a *App) SerialPorts(ctx context.Context) ([]model.SerialPort, error) {
	return a.svc.SerialPorts(ctx)
}

// SaveFile asks the user where to save text under the suggested name and
// writes it there. It returns the chosen path, or "" when the user cancelled.
// The path comes from the native dialog, never from the frontend.
func (a *App) SaveFile(name, _ string, text string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", service.Errorf(service.CodeInternal, "no application")
	}
	name = filepath.Base(name)
	dialog := app.Dialog.SaveFile().SetFilename(name).CanCreateDirectories(true)
	if ext := strings.TrimPrefix(filepath.Ext(name), "."); ext != "" {
		dialog.AddFilter(strings.ToUpper(ext)+" files", "*."+ext)
	}
	path, err := dialog.PromptForSingleSelection()
	if err != nil {
		return "", service.Wrap(service.CodeInternal, err, "save dialog")
	}
	if path == "" {
		return "", nil
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", service.Wrap(service.CodeInternal, err, "cannot save the file")
	}
	return path, nil
}

// OpenExternal opens an https link in the user's browser.
func (a *App) OpenExternal(link string) error {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return service.Errorf(service.CodeInvalidArgument, "only https links can be opened")
	}
	app := application.Get()
	if app == nil {
		return service.Errorf(service.CodeInternal, "no application")
	}
	return app.Browser.OpenURL(u.String())
}
