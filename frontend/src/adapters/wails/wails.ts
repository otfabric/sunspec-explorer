// The only file that touches the Wails runtime. It assumes a Go service
// `App` in internal/gui whose methods mirror service.Service, plus SaveFile;
// adjust the names here when the Go binding is final.

import { Browser, Call, Clipboard, Events } from '@wailsio/runtime';
import type { Mode, Platform } from '../../app/platform/platform';
import { loadTheme, persistTheme } from '../../app/platform/themeStorage';
import { ServiceError, toServiceError, type ErrorCode, type Service } from '../../app/service/service';
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
} from '../../app/service/types';

const BINDING = 'github.com/otfabric/sunspec-explorer/internal/gui.App';
const EVENT_NAME = 'explorer:event';

interface ErrorBody {
  code: ErrorCode;
  message: string;
  attempts?: ProbeAttempt[];
}

const isErrorBody = (value: unknown): value is ErrorBody =>
  typeof value === 'object' && value !== null && 'code' in value && 'message' in value;

/** A Go service.Error arrives as the rejection's cause or as JSON in its message. */
function fromGo(err: unknown): ServiceError {
  const candidates: unknown[] = [];
  if (err instanceof Error) {
    candidates.push(err.cause);
    try {
      candidates.push(JSON.parse(err.message));
    } catch {
      /* a plain message */
    }
  }
  const body = candidates.find(isErrorBody);
  return body ? new ServiceError(body.code, body.message, body.attempts) : toServiceError(err);
}

async function call<T>(method: string, ...args: unknown[]): Promise<T> {
  try {
    return (await Call.ByName(`${BINDING}.${method}`, ...args)) as T;
  } catch (err) {
    throw fromGo(err);
  }
}

export class WailsService implements Service {
  health = () => call<Health>('Health');
  openSession = (cfg: TargetConfig) => call<SessionInfo>('OpenSession', cfg);
  closeSession = (id: string) => call<void>('CloseSession', id);
  sessions = () => call<SessionInfo[]>('Sessions');
  rediscover = (id: string) => call<SessionInfo>('Rediscover', id);
  snapshot = (id: string, req: SnapshotRequest) => call<Snapshot>('Snapshot', id, req);
  startPolling = (id: string, cfg: PollConfig) => call<void>('StartPolling', id, cfg);
  stopPolling = (id: string) => call<void>('StopPolling', id);
  state = (id: string) => call<SessionState>('State', id);
  startRecording = (id: string, cfg: RecordingConfig) => call<RecordingInfo>('StartRecording', id, cfg);
  stopRecording = (recId: string) => call<RecordingInfo>('StopRecording', recId);
  recordings = () => call<RecordingInfo[]>('Recordings');
  deleteRecording = (recId: string) => call<void>('DeleteRecording', recId);
  // The Go side shows the native Save dialog and copies the file itself.
  saveRecording = async (recId: string) => (await call<string>('SaveRecording', recId)) !== '';
  serialPorts = () => call<SerialPort[]>('SerialPorts');

  // The Go side forwards every engine event to the window as it happens, so
  // there is nothing to replay: sinceSeq is not used.
  subscribe(_sinceSeq: number, onEvent: (event: ExplorerEvent) => void): () => void {
    return Events.On(EVENT_NAME, (event) => onEvent(event.data as ExplorerEvent));
  }
}

export class WailsPlatform implements Platform {
  // The desktop application is always local: its engine runs in-process.
  readonly mode: Mode = { kind: 'local' };
  persistTheme = persistTheme;
  loadTheme = loadTheme;

  /** The Go side shows the native Save dialog and returns the path, or "" when cancelled. */
  async saveFile(name: string, mime: string, text: string): Promise<boolean> {
    return (await call<string>('SaveFile', name, mime, text)) !== '';
  }

  async saveBlob(name: string, blob: Blob): Promise<boolean> {
    return this.saveFile(name, blob.type, await blob.text());
  }

  openExternal(url: string): void {
    void Browser.OpenURL(url);
  }

  copyText(text: string): Promise<void> {
    return Clipboard.SetText(text);
  }
}
