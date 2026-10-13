// SPDX-License-Identifier: MIT

// Package model holds the JSON data transfer objects of the Explorer: what the
// Service interface returns, what the HTTP API carries and what exports and
// recordings contain. The types are independent of go-sunspec so that the wire
// and file formats stay stable when the library changes.
//
// All timestamps are UTC. All register addresses are zero-based Modbus
// addresses, as go-sunspec reports them.
package model

import "time"

// Schema identifiers of the file formats.
const (
	SnapshotSchema  = "otfabric.sunspec-explorer.snapshot/1"
	RecordingSchema = "otfabric.sunspec-explorer.recording/1"
)

// AppName is the application name written to exports.
const AppName = "sunspec-explorer"

// APIVersion is the version of the Service contract and of the HTTP API. The
// major part changes on incompatible changes, the minor part on additions.
const APIVersion = "1.0"

// Register types of a target.
const (
	RegTypeHolding = "holding"
	RegTypeInput   = "input"
)

// Metadata levels of decoded points.
const (
	// MetadataFull includes label, description, access, mandatory and static.
	MetadataFull = "full"
	// MetadataCompact omits them; used for poll samples and recordings.
	MetadataCompact = "compact"
)

// Status of one model in a read.
const (
	// ModelOK means the model was read and, when a schema exists, decoded.
	ModelOK = "ok"
	// ModelError means the read failed; Error says why.
	ModelError = "error"
	// ModelChanged means the device no longer reports this model at this
	// address: the chain changed since discovery.
	ModelChanged = "changed"
)

// Status of a session's connection.
const (
	// SessionConnected means the last exchange with the device succeeded.
	SessionConnected = "connected"
	// SessionReconnecting means the device stopped answering and the engine is
	// retrying with backoff.
	SessionReconnecting = "reconnecting"
	// SessionClosed means the session was closed.
	SessionClosed = "closed"
)

// Event kinds.
const (
	EventSample           = "sample"
	EventSessionStatus    = "session_status"
	EventDiscoveryChanged = "discovery_changed"
	EventRecordingStatus  = "recording_status"
	EventError            = "error"
	// EventResync tells a subscriber that it missed events: it must fetch the
	// state of its sessions again.
	EventResync = "resync"
)

// Health describes a running engine.
type Health struct {
	Status         string    `json:"status"`
	Version        string    `json:"version"`
	APIVersion     string    `json:"api_version"`
	UptimeS        int64     `json:"uptime_s"`
	GoSunspec      string    `json:"go_sunspec"`
	RegistryModels int       `json:"registry_models"`
	Time           time.Time `json:"time"`
}

// AppInfo identifies the application that wrote a file.
type AppInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// LibInfo identifies the libraries that decoded the data.
type LibInfo struct {
	GoSunspec      string `json:"go_sunspec"`
	RegistryModels int    `json:"registry_models"`
}

// TargetConfig is what a client sends to open a session.
type TargetConfig struct {
	// URL is the device URL: tcp://host:port or rtuovertcp://host:port. It
	// must not contain credentials.
	URL string `json:"url"`
	// UnitID is the Modbus unit ID. Zero selects 1.
	UnitID uint8 `json:"unit_id"`
	// TimeoutMS is the request timeout in milliseconds. Zero selects the
	// engine default.
	TimeoutMS int `json:"timeout_ms,omitempty"`
	// RegType is "holding" (default when empty) or "input".
	RegType string `json:"reg_type,omitempty"`
	// BaseAddresses lists the addresses probed for the SunSpec marker. Empty
	// selects the go-sunspec defaults.
	BaseAddresses []uint16 `json:"base_addresses,omitempty"`
	// MaxModels caps the number of models read from the chain. Zero selects
	// the go-sunspec default.
	MaxModels int `json:"max_models,omitempty"`
}

// Target describes the device of an open session.
type Target struct {
	URL         string `json:"url"`
	UnitID      uint8  `json:"unit_id"`
	RegType     string `json:"reg_type"`
	BaseAddress uint16 `json:"base_address"`
}

// ProbeAttempt is one base-address probe made during detection.
type ProbeAttempt struct {
	BaseAddress uint16 `json:"base_address"`
	RegType     string `json:"reg_type"`
	Matched     bool   `json:"matched"`
	Error       string `json:"error,omitempty"`
}

// DeviceIdentity holds the identification points of the Common model.
type DeviceIdentity struct {
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Options      string `json:"options,omitempty"`
	Version      string `json:"version"`
	Serial       string `json:"serial"`
}

// ModelInfo describes one model instance found by discovery. Index is its
// position in the chain: a device can hold several models with the same ID.
type ModelInfo struct {
	Index             int    `json:"index"`
	ID                uint16 `json:"id"`
	Name              string `json:"name"`
	StartAddress      uint16 `json:"start_address"`
	Length            uint16 `json:"length"`
	SchemaKnown       bool   `json:"schema_known"`
	DecodingSupported bool   `json:"decoding_supported"`
	// StaticOnly is true when the model holds nothing that changes by itself
	// (every point is static or a writable setting, as in Common): such a
	// model is read once, not in every poll cycle.
	StaticOnly bool `json:"static_only"`
}

// SessionInfo describes a session and the discovery result it reads from.
type SessionInfo struct {
	ID           string          `json:"id"`
	Target       Target          `json:"target"`
	Status       string          `json:"status"`
	StatusDetail string          `json:"status_detail,omitempty"`
	OpenedAt     time.Time       `json:"opened_at"`
	DiscoveredAt time.Time       `json:"discovered_at"`
	Device       *DeviceIdentity `json:"device,omitempty"`
	Models       []ModelInfo     `json:"models"`
	Warnings     []string        `json:"warnings"`
	Attempts     []ProbeAttempt  `json:"attempts"`
}

// PointView is one decoded point.
type PointView struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Label, Description, Access, Mandatory and Static are metadata: present
	// at level "full", omitted at level "compact".
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Access      string `json:"access,omitempty"`
	Mandatory   *bool  `json:"mandatory,omitempty"`
	Static      *bool  `json:"static,omitempty"`
	Units       string `json:"units,omitempty"`
	// Implemented is false when the device reports the SunSpec "not
	// implemented" value; Raw then holds that sentinel, not a measurement.
	Implemented bool `json:"implemented"`
	// Raw is the unscaled value: a number, a string, or a list of registers
	// for a type that is not decoded. A float that is NaN or infinite is null.
	Raw any `json:"raw"`
	// Scaled is Raw times 10^SFRaw, or null when the point has no resolved
	// scale factor or is not implemented.
	Scaled *float64 `json:"scaled"`
	SFName string   `json:"sf_name,omitempty"`
	SFRaw  *int16   `json:"sf_raw,omitempty"`
	// Symbols holds the names of the active enum value or bitfield flags.
	Symbols []string `json:"symbols,omitempty"`
	// Address is the absolute zero-based register address of the point.
	Address int `json:"address"`
	// Offset is the register offset from the model's ID register.
	Offset int `json:"offset"`
	// Size is the number of registers the point occupies.
	Size int `json:"size"`
}

// GroupView is one decoded group instance: its points and nested instances.
type GroupView struct {
	Name string `json:"name"`
	// Label is metadata (level "full").
	Label string `json:"label,omitempty"`
	// Index is the 1-based instance number within the parent, 0 for the
	// top-level group.
	Index  int         `json:"index"`
	Points []PointView `json:"points"`
	Groups []GroupView `json:"groups"`
}

// ModelResult is the outcome of reading one model instance.
type ModelResult struct {
	Index          int      `json:"index"`
	ID             uint16   `json:"id"`
	Name           string   `json:"name"`
	StartAddress   uint16   `json:"start_address"`
	Length         uint16   `json:"length"`
	SchemaKnown    bool     `json:"schema_known"`
	Status         string   `json:"status"`
	Error          *string  `json:"error"`
	ReadDurationMS int64    `json:"read_duration_ms"`
	Warnings       []string `json:"warnings"`
	// Stale is true when the last good read of the model is older than two
	// poll intervals. LastOKAt is the time of that read.
	Stale    bool       `json:"stale"`
	LastOKAt *time.Time `json:"last_ok_at,omitempty"`
	// Group is the decoded model, or null when it was not decoded.
	Group *GroupView `json:"group"`
	// RawRegisters holds the data registers of a model without a schema, or
	// of every model when the request asked for raw registers.
	RawRegisters []uint16 `json:"raw_registers"`
}

// SnapshotRequest selects what a single read covers.
type SnapshotRequest struct {
	// ModelIndexes lists chain positions. Empty selects every model.
	ModelIndexes []int `json:"model_indexes,omitempty"`
	// Metadata is "full" (default when empty) or "compact".
	Metadata string `json:"metadata,omitempty"`
	// IncludeRaw adds the raw registers of decoded models too.
	IncludeRaw bool `json:"include_raw,omitempty"`
}

// Snapshot is one read of a device and, serialized, the snapshot export.
type Snapshot struct {
	Schema         string          `json:"schema"`
	App            AppInfo         `json:"app"`
	Libs           LibInfo         `json:"libs"`
	SessionID      string          `json:"session_id"`
	CapturedAt     time.Time       `json:"captured_at"`
	ReadDurationMS int64           `json:"read_duration_ms"`
	Metadata       string          `json:"metadata"`
	Target         Target          `json:"target"`
	Device         *DeviceIdentity `json:"device"`
	Models         []ModelResult   `json:"models"`
	Warnings       []string        `json:"warnings"`
}

// PollConfig configures polling.
type PollConfig struct {
	// IntervalS is the start-to-start interval in whole seconds (1..3600).
	// Zero selects 5.
	IntervalS int `json:"interval_s"`
	// ModelIndexes lists chain positions. Empty selects every model that is
	// not static-only; static-only models are then read once at the start.
	ModelIndexes []int `json:"model_indexes,omitempty"`
}

// PollState reports the polling of a session.
type PollState struct {
	Active       bool       `json:"active"`
	IntervalS    int        `json:"interval_s"`
	ModelIndexes []int      `json:"model_indexes"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	Cycles       uint64     `json:"cycles"`
	// Failures counts cycles in which at least one model failed.
	Failures uint64 `json:"failures"`
	// Overruns counts ticks skipped because a cycle was still running.
	Overruns     uint64     `json:"overruns"`
	LastSampleAt *time.Time `json:"last_sample_at,omitempty"`
}

// Sample is the result of one poll cycle. Models are read one after another:
// a sample is not an atomic view of the device.
type Sample struct {
	SessionID string `json:"session_id"`
	// Seq numbers the samples of a polling run, starting at 1.
	Seq            uint64        `json:"seq"`
	CapturedAt     time.Time     `json:"captured_at"`
	ReadDurationMS int64         `json:"read_duration_ms"`
	Overruns       uint64        `json:"overruns"`
	Metadata       string        `json:"metadata"`
	Models         []ModelResult `json:"models"`
}

// SessionState is everything a client needs to resume showing a session.
type SessionState struct {
	Session    SessionInfo    `json:"session"`
	Polling    PollState      `json:"polling"`
	LastSample *Sample        `json:"last_sample"`
	Recording  *RecordingInfo `json:"recording"`
}

// Event is one entry of the event stream.
type Event struct {
	// Seq increases by one per event of an engine.
	Seq       uint64         `json:"seq"`
	Kind      string         `json:"kind"`
	SessionID string         `json:"session_id,omitempty"`
	At        time.Time      `json:"at"`
	Sample    *Sample        `json:"sample,omitempty"`
	Session   *SessionInfo   `json:"session,omitempty"`
	Recording *RecordingInfo `json:"recording,omitempty"`
	Message   string         `json:"message,omitempty"`
}

// Recording states.
const (
	RecordingRunning     = "running"
	RecordingStopped     = "stopped"
	RecordingInterrupted = "interrupted"
)

// RecordingConfig configures a recording.
type RecordingConfig struct {
	// Name is the base name of the recording files: [a-zA-Z0-9._-]{1,64}.
	// Empty lets the engine choose one.
	Name string `json:"name,omitempty"`
	// RotateBytes starts a new file after this many bytes. Zero selects the
	// engine default.
	RotateBytes int64 `json:"rotate_bytes,omitempty"`
}

// Reasons a recording stopped.
const (
	StopUser          = "user"
	StopLimit         = "limit"
	StopSessionClosed = "session_closed"
	StopShutdown      = "shutdown"
	StopError         = "error"
)

// Types of the lines of a recording file.
const (
	LineHeader = "header"
	LineSample = "sample"
	LineEvent  = "event"
	LineFooter = "footer"
)

// RecordingHeader is the first line of a recording.
type RecordingHeader struct {
	Type      string          `json:"type"`
	Schema    string          `json:"schema"`
	Name      string          `json:"name"`
	StartedAt time.Time       `json:"started_at"`
	IntervalS int             `json:"interval_s"`
	App       AppInfo         `json:"app"`
	Libs      LibInfo         `json:"libs"`
	Target    Target          `json:"target"`
	Device    *DeviceIdentity `json:"device"`
	Models    []ModelInfo     `json:"models"`
}

// RecordingSample is a sample line: one poll cycle, at metadata level
// "compact" (the header and the model IDs identify the schema).
type RecordingSample struct {
	Type string `json:"type"`
	Sample
}

// RecordingEvent is an event line: something that explains a gap or a change
// in the samples around it.
type RecordingEvent struct {
	Type   string    `json:"type"`
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
	// Models is the new chain after a discovery_changed event.
	Models []ModelInfo `json:"models,omitempty"`
}

// RecordingFooter is the last line of a recording that was stopped. A
// recording without one was interrupted.
type RecordingFooter struct {
	Type      string    `json:"type"`
	StoppedAt time.Time `json:"stopped_at"`
	Samples   uint64    `json:"samples"`
	Failures  uint64    `json:"failures"`
	Reason    string    `json:"reason"`
}

// RecordingInfo describes a recording.
type RecordingInfo struct {
	ID        string     `json:"id"`
	SessionID string     `json:"session_id"`
	Name      string     `json:"name"`
	State     string     `json:"state"`
	StartedAt time.Time  `json:"started_at"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	// StopReason says why a stopped recording stopped.
	StopReason string `json:"stop_reason,omitempty"`
	Samples    uint64 `json:"samples"`
	// Failures counts the samples in which at least one model failed.
	Failures  uint64 `json:"failures"`
	SizeBytes int64  `json:"size_bytes"`
	// Files is the number of files the recording is split into.
	Files int `json:"files"`
	// Location is the directory that holds the recording, for display only.
	Location string `json:"location"`
	// Temporary is true when Location is ephemeral storage.
	Temporary bool `json:"temporary"`
}

// SerialPort is a serial device the engine is allowed to open.
type SerialPort struct {
	Path string `json:"path"`
}
