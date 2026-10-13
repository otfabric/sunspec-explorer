// TypeScript mirror of internal/service/service.go. OpenRecording (a byte
// stream) appears here as saveRecording: the window never handles the bytes
// itself, it asks for a copy to be saved.

import type {
  ExplorerEvent,
  Health,
  PollConfig,
  ProbeAttempt,
  RecordingConfig,
  RecordingInfo,
  SerialPort,
  SessionInfo,
  SessionState,
  Snapshot,
  SnapshotRequest,
  TargetConfig,
} from './types';

export type ErrorCode =
  | 'invalid_argument'
  | 'not_found'
  | 'limit_exceeded'
  | 'not_allowed'
  | 'connect_failed'
  | 'not_sunspec'
  | 'discovery_failed'
  | 'not_implemented'
  | 'unauthorized'
  | 'canceled'
  | 'internal';

/**
 * The link between this window and the engine. Only a transport that can lose
 * the engine reports it: 'down' while it retries, 'up' when the event stream
 * is open again, 'unauthorized' when the engine refuses the token (final).
 */
export type LinkStatus = 'up' | 'down' | 'unauthorized';

/** The error every Service method rejects with. */
export class ServiceError extends Error {
  readonly code: ErrorCode;
  /** Detection probes of a failed openSession or rediscover. */
  readonly attempts?: ProbeAttempt[];

  constructor(code: ErrorCode, message: string, attempts?: ProbeAttempt[]) {
    super(message);
    this.name = 'ServiceError';
    this.code = code;
    this.attempts = attempts;
  }
}

/** Turns whatever a transport threw into a ServiceError. */
export function toServiceError(err: unknown): ServiceError {
  if (err instanceof ServiceError) {
    return err;
  }
  return new ServiceError('internal', err instanceof Error ? err.message : String(err));
}

/** The read-only SunSpec engine as the user interface sees it. */
export interface Service {
  health(): Promise<Health>;
  openSession(cfg: TargetConfig): Promise<SessionInfo>;
  closeSession(id: string): Promise<void>;
  sessions(): Promise<SessionInfo[]>;
  rediscover(id: string): Promise<SessionInfo>;
  snapshot(id: string, req: SnapshotRequest): Promise<Snapshot>;
  startPolling(id: string, cfg: PollConfig): Promise<void>;
  stopPolling(id: string): Promise<void>;
  state(id: string): Promise<SessionState>;
  /**
   * Delivers the event stream. Events above sinceSeq that the engine still
   * holds are replayed first; 0 delivers new events only. onLink, when given,
   * is told about changes of the link to the engine; an in-process engine
   * never calls it. Returns the function that ends the subscription.
   */
  subscribe(sinceSeq: number, onEvent: (event: ExplorerEvent) => void, onLink?: (status: LinkStatus) => void): () => void;
  startRecording(id: string, cfg: RecordingConfig): Promise<RecordingInfo>;
  stopRecording(recId: string): Promise<RecordingInfo>;
  /** Every recording in the engine's data directory, newest first. */
  recordings(): Promise<RecordingInfo[]>;
  /** Deletes a recording that is not running. */
  deleteRecording(recId: string): Promise<void>;
  /** Saves a copy of a recording where the user wants it. Resolves to false when cancelled. */
  saveRecording(recId: string): Promise<boolean>;
  serialPorts(): Promise<SerialPort[]>;
}
