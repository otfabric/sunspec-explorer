// TypeScript mirror of internal/model/model.go. Field names are the Go JSON
// tags; keep the two files in step.

/** RFC 3339 timestamp in UTC, as Go's time.Time serializes. */
export type Timestamp = string;

export const SNAPSHOT_SCHEMA = 'otfabric.sunspec-explorer.snapshot/1';
export const API_VERSION = '1.0';

export type RegType = 'holding' | 'input';
export type MetadataLevel = 'full' | 'compact';
export type ModelStatus = 'ok' | 'error' | 'changed';
export type SessionStatus = 'connected' | 'reconnecting' | 'closed';
export type EventKind =
  | 'sample'
  | 'session_status'
  | 'discovery_changed'
  | 'recording_status'
  | 'error'
  | 'resync';
export type RecordingState = 'running' | 'stopped' | 'interrupted';
export type RecordingStopReason = 'user' | 'limit' | 'disk_space' | 'session_closed' | 'shutdown' | 'error';

/** What an engine can do, depending on how it was built and started. */
export type Feature = 'recording' | 'serial' | 'tls';

export interface Health {
  status: string;
  version: string;
  api_version: string;
  uptime_s: number;
  go_sunspec: string;
  registry_models: number;
  /** The engine's clock. */
  time: Timestamp;
  features: Feature[];
}

export interface AppInfo {
  name: string;
  version: string;
}

export interface LibInfo {
  go_sunspec: string;
  registry_models: number;
}

/** Line settings of an rtu:// target; the engine applies defaults for what is left out. */
export interface SerialConfig {
  baud?: number;
  data_bits?: number;
  parity?: 'none' | 'even' | 'odd';
  stop_bits?: number;
}

export type ExportFormat = 'csv' | 'devicefile';
export type RecordingFormat = 'jsonl' | 'csv';

/** A file rendered by the engine, for the platform to save. */
export interface ExportFile {
  name: string;
  mime: string;
  text: string;
}

export interface TargetConfig {
  /** tcp://host:port, rtuovertcp://host:port, tcp+tls://host:port or rtu://<device>. */
  url: string;
  unit_id: number;
  timeout_ms?: number;
  reg_type?: RegType;
  base_addresses?: number[];
  max_models?: number;
  serial?: SerialConfig;
}

export interface Target {
  url: string;
  unit_id: number;
  reg_type: RegType;
  base_address: number;
}

export interface ProbeAttempt {
  base_address: number;
  reg_type: RegType;
  matched: boolean;
  error?: string;
}

export interface DeviceIdentity {
  manufacturer: string;
  model: string;
  options?: string;
  version: string;
  serial: string;
}

export interface ModelInfo {
  index: number;
  id: number;
  name: string;
  start_address: number;
  length: number;
  schema_known: boolean;
  decoding_supported: boolean;
  static_only: boolean;
}

export interface SessionInfo {
  id: string;
  target: Target;
  status: SessionStatus;
  status_detail?: string;
  opened_at: Timestamp;
  discovered_at: Timestamp;
  device?: DeviceIdentity;
  models: ModelInfo[];
  warnings: string[];
  attempts: ProbeAttempt[];
}

/** A number, a string, or a list of registers for a type that is not decoded. */
export type RawValue = number | string | number[] | null;

export interface PointView {
  name: string;
  type: string;
  label?: string;
  description?: string;
  access?: string;
  mandatory?: boolean;
  static?: boolean;
  units?: string;
  implemented: boolean;
  raw: RawValue;
  scaled: number | null;
  sf_name?: string;
  sf_raw?: number;
  symbols?: string[];
  address: number;
  offset: number;
  size: number;
}

export interface GroupView {
  name: string;
  label?: string;
  index: number;
  points: PointView[];
  groups: GroupView[];
}

export interface ModelResult {
  index: number;
  id: number;
  name: string;
  start_address: number;
  length: number;
  schema_known: boolean;
  status: ModelStatus;
  error: string | null;
  read_duration_ms: number;
  warnings: string[];
  stale: boolean;
  last_ok_at?: Timestamp;
  group: GroupView | null;
  raw_registers: number[] | null;
}

export interface SnapshotRequest {
  model_indexes?: number[];
  metadata?: MetadataLevel;
  include_raw?: boolean;
}

export interface Snapshot {
  schema: string;
  app: AppInfo;
  libs: LibInfo;
  session_id: string;
  captured_at: Timestamp;
  read_duration_ms: number;
  metadata: MetadataLevel;
  target: Target;
  device: DeviceIdentity | null;
  models: ModelResult[];
  warnings: string[];
}

export interface PollConfig {
  interval_s: number;
  model_indexes?: number[];
}

export interface PollState {
  active: boolean;
  interval_s: number;
  model_indexes: number[];
  started_at?: Timestamp;
  cycles: number;
  failures: number;
  overruns: number;
  last_sample_at?: Timestamp;
}

export interface Sample {
  session_id: string;
  seq: number;
  captured_at: Timestamp;
  read_duration_ms: number;
  overruns: number;
  metadata: MetadataLevel;
  models: ModelResult[];
}

export interface SessionState {
  session: SessionInfo;
  polling: PollState;
  last_sample: Sample | null;
  recording: RecordingInfo | null;
}

export interface ExplorerEvent {
  seq: number;
  kind: EventKind;
  session_id?: string;
  at: Timestamp;
  sample?: Sample;
  session?: SessionInfo;
  recording?: RecordingInfo;
  message?: string;
}

export interface RecordingConfig {
  name?: string;
  rotate_bytes?: number;
}

export interface RecordingInfo {
  id: string;
  session_id: string;
  name: string;
  state: RecordingState;
  started_at: Timestamp;
  stopped_at?: Timestamp;
  stop_reason?: RecordingStopReason;
  samples: number;
  failures: number;
  size_bytes: number;
  /** Number of files the recording was rotated into. */
  files: number;
  /** Directory that holds the recording, for display only. */
  location: string;
  /** True when location is storage the system may clear (e.g. on reboot). */
  temporary: boolean;
}

export interface SerialPort {
  path: string;
}
