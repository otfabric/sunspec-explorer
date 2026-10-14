import type { Platform } from '../../app/platform/platform';
import { ServiceError, type ErrorCode, type LinkStatus, type Service } from '../../app/service/service';
import type {
  ExplorerEvent,
  ExportFile,
  ExportFormat,
  Health,
  PollConfig,
  ProbeAttempt,
  RecordingConfig,
  RecordingFormat,
  RecordingInfo,
  SerialPort,
  SessionInfo,
  SessionState,
  Snapshot,
  SnapshotRequest,
  TargetConfig,
} from '../../app/service/types';
import { createSseParser } from './sse';

const BASE = '/api/v1';
const RETRY_MIN_MS = 1000;
const RETRY_MAX_MS = 30000;
// The daemon sends a keep-alive comment about every 15 s. A stream that stays
// silent this long is dead even if the socket has not noticed (a dropped
// tunnel, a sleeping laptop).
const SILENCE_MS = 45000;

interface ErrorBody {
  error?: { code?: ErrorCode; message?: string; attempts?: ProbeAttempt[] };
}

interface Subscription {
  onLink?: (status: LinkStatus) => void;
  /** Drops the current stream connection so that it is opened again. */
  reopen: () => void;
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener('abort', () => {
      clearTimeout(timer);
      resolve();
    });
  });
}

/** Service over the daemon's same-origin HTTP API. The token lives only in this object. */
export class HttpService implements Service {
  private readonly token: string | null;
  private readonly platform: Pick<Platform, 'saveBlob'>;
  private readonly subscriptions = new Set<Subscription>();

  constructor(token: string | null, platform: Pick<Platform, 'saveBlob'>) {
    this.token = token;
    this.platform = platform;
  }

  health = () => this.request<Health>('GET', '/health');
  openSession = (cfg: TargetConfig) => this.request<SessionInfo>('POST', '/sessions', cfg);
  closeSession = (id: string) => this.request<void>('DELETE', `/sessions/${encodeURIComponent(id)}`);
  sessions = () => this.request<SessionInfo[]>('GET', '/sessions');
  rediscover = (id: string) => this.request<SessionInfo>('POST', `/sessions/${encodeURIComponent(id)}/rediscover`);
  snapshot = (id: string, req: SnapshotRequest) => this.request<Snapshot>('POST', `/sessions/${encodeURIComponent(id)}/snapshot`, req);
  startPolling = (id: string, cfg: PollConfig) => this.request<void>('PUT', `/sessions/${encodeURIComponent(id)}/polling`, cfg);
  stopPolling = (id: string) => this.request<void>('DELETE', `/sessions/${encodeURIComponent(id)}/polling`);
  state = (id: string) => this.request<SessionState>('GET', `/sessions/${encodeURIComponent(id)}/state`);
  startRecording = (id: string, cfg: RecordingConfig) => this.request<RecordingInfo>('POST', `/sessions/${encodeURIComponent(id)}/recordings`, cfg);
  stopRecording = (recId: string) => this.request<RecordingInfo>('POST', `/recordings/${encodeURIComponent(recId)}/stop`);
  recordings = () => this.request<RecordingInfo[]>('GET', '/recordings');
  deleteRecording = (recId: string) => this.request<void>('DELETE', `/recordings/${encodeURIComponent(recId)}`);
  exportSession = (id: string, format: ExportFormat) => this.request<ExportFile>('POST', `/sessions/${encodeURIComponent(id)}/export`, { format });
  serialPorts = () => this.request<SerialPort[]>('GET', '/serial-ports');

  /**
   * Downloads the recording with the token in a header, as every request, and
   * hands the bytes to the browser as a download: a plain link to the file
   * would need the token in its URL.
   */
  async saveRecording(recId: string, format: RecordingFormat = 'jsonl'): Promise<boolean> {
    const query = format === 'csv' ? '?format=csv' : '';
    const response = await this.send('GET', `/recordings/${encodeURIComponent(recId)}/download${query}`);
    if (!response.ok) {
      throw this.failure(response, await response.text());
    }
    const name = /filename="([^"]+)"/.exec(response.headers.get('Content-Disposition') ?? '')?.[1] ?? `${recId}.${format}`;
    return this.platform.saveBlob(name, await response.blob());
  }

  subscribe(sinceSeq: number, onEvent: (event: ExplorerEvent) => void, onLink?: (status: LinkStatus) => void): () => void {
    const abort = new AbortController();
    const subscription: Subscription = { onLink, reopen: () => {} };
    this.subscriptions.add(subscription);
    void this.stream(sinceSeq, onEvent, subscription, abort.signal);
    return () => {
      this.subscriptions.delete(subscription);
      abort.abort();
    };
  }

  private headers(): Record<string, string> {
    return { Authorization: `Bearer ${this.token}` };
  }

  private unauthorized(message: string): ServiceError {
    this.subscriptions.forEach((subscription) => subscription.onLink?.('unauthorized'));
    return new ServiceError('unauthorized', message);
  }

  private async send(method: string, path: string, body?: unknown): Promise<Response> {
    if (this.token === null) {
      throw this.unauthorized('This page has no access token.');
    }
    try {
      return await fetch(BASE + path, {
        method,
        headers: body === undefined ? this.headers() : { ...this.headers(), 'Content-Type': 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
    } catch {
      // The stream may not have noticed yet: report the loss now and have the
      // stream reconnect, which is also what reports the link as up again.
      this.subscriptions.forEach((subscription) => {
        subscription.onLink?.('down');
        subscription.reopen();
      });
      throw new ServiceError('internal', 'The daemon cannot be reached.');
    }
  }

  /** The error an unsuccessful response stands for. */
  private failure(response: Response, text: string): ServiceError {
    let error: ErrorBody['error'];
    try {
      error = (JSON.parse(text) as ErrorBody).error;
    } catch {
      /* not the daemon's error format */
    }
    const message = error?.message ?? `HTTP ${response.status} ${response.statusText}`;
    return response.status === 401 ? this.unauthorized(message) : new ServiceError(error?.code ?? 'internal', message, error?.attempts);
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const response = await this.send(method, path, body);
    const text = await response.text();
    if (!response.ok) {
      throw this.failure(response, text);
    }
    try {
      return (text === '' ? undefined : JSON.parse(text)) as T;
    } catch {
      throw new ServiceError('internal', `HTTP ${response.status}: the answer is not JSON. Is this the daemon?`);
    }
  }

  /**
   * Reads the event stream until aborted. A broken stream is reopened with
   * backoff from the last sequence number seen, so the daemon replays what
   * was missed, or says with a `resync` event that it no longer can.
   */
  private async stream(sinceSeq: number, onEvent: (event: ExplorerEvent) => void, subscription: Subscription, signal: AbortSignal): Promise<void> {
    if (this.token === null) {
      subscription.onLink?.('unauthorized');
      return;
    }
    let lastSeq = sinceSeq;
    let delay = RETRY_MIN_MS;
    while (!signal.aborted) {
      const attempt = new AbortController();
      const stop = () => attempt.abort();
      signal.addEventListener('abort', stop);
      subscription.reopen = stop;
      let silence = setTimeout(stop, SILENCE_MS);
      try {
        const response = await fetch(`${BASE}/events?since=${lastSeq}`, { headers: { ...this.headers(), Accept: 'text/event-stream' }, signal: attempt.signal });
        if (response.status === 401) {
          subscription.onLink?.('unauthorized');
          return;
        }
        if (!response.ok || !response.body) {
          throw new Error(`HTTP ${response.status}`);
        }
        subscription.onLink?.('up');
        delay = RETRY_MIN_MS;
        const parse = createSseParser((frame) => {
          const event = JSON.parse(frame.data) as ExplorerEvent;
          // After a resync the daemon's numbering is what counts, even when it
          // is lower than before (a restarted daemon starts again at 1).
          lastSeq = event.kind === 'resync' ? event.seq : Math.max(lastSeq, event.seq);
          onEvent(event);
        });
        const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
        for (let read = await reader.read(); !read.done; read = await reader.read()) {
          clearTimeout(silence);
          silence = setTimeout(stop, SILENCE_MS);
          parse(read.value);
        }
      } catch {
        // Network failure, silence or a broken frame: reconnect below.
      } finally {
        clearTimeout(silence);
        signal.removeEventListener('abort', stop);
      }
      if (signal.aborted) {
        return;
      }
      subscription.onLink?.('down');
      await sleep(delay, signal);
      delay = Math.min(delay * 2, RETRY_MAX_MS);
    }
  }
}
