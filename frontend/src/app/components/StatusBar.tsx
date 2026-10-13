import { CheckIcon, ClockIcon, ErrorIcon, RecordIcon, RefreshIcon } from '../icons';
import type { LinkStatus } from '../service/service';
import type { Health, ModelResult, PollState, RecordingInfo, SessionInfo } from '../service/types';
import { formatLocalTime } from '../state/format';

interface StatusBarProps {
  health: Health | null;
  link: LinkStatus;
  session: SessionInfo | null;
  connecting: boolean;
  polling: PollState;
  results: Record<number, ModelResult>;
  recording: RecordingInfo | null;
  onShowRecordings: () => void;
}

/** The last two segments of a directory path; the full path is in the tooltip. */
const shortPath = (path: string) => {
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts.length > 2 ? `…/${parts.slice(-2).join('/')}` : path;
};

function Connection({ link, session, connecting }: { link: LinkStatus; session: SessionInfo | null; connecting: boolean }) {
  if (link !== 'up') {
    return (
      <span className="inline-flex items-center gap-1 text-danger">
        <ErrorIcon size={14} />
        {link === 'down' ? 'daemon unreachable' : 'no valid token'}
      </span>
    );
  }
  if (!session) {
    return <span>{connecting ? 'connecting…' : 'not connected'}</span>;
  }
  if (session.status === 'reconnecting') {
    return (
      <span className="inline-flex items-center gap-1 text-wire" title={session.status_detail}>
        <RefreshIcon size={14} />
        reconnecting
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 text-accent">
      <CheckIcon size={14} />
      connected · {session.target.url} · unit {session.target.unit_id}
    </span>
  );
}

export function StatusBar({ health, link, session, connecting, polling, results, recording, onShowRecordings }: StatusBarProps) {
  const stale = Object.values(results).filter((result) => result.stale).length;
  const device = session?.device;
  return (
    <footer className="flex h-8 shrink-0 items-center gap-5 overflow-hidden border-t border-line bg-surface px-4 font-mono text-[12px] whitespace-nowrap text-muted">
      <Connection link={link} session={session} connecting={connecting} />
      {device && (
        <span title="Manufacturer / model / serial number">
          {device.manufacturer} / {device.model} / {device.serial}
        </span>
      )}
      {session && (
        <>
          <span>{polling.active ? `polling every ${polling.interval_s} s` : 'polling stopped'}</span>
          <span title={polling.last_sample_at ? `${polling.last_sample_at} (UTC)` : undefined}>
            last sample {polling.last_sample_at ? formatLocalTime(polling.last_sample_at) : '—'}
          </span>
          <span>
            cycles {polling.cycles} · failures {polling.failures} · overruns {polling.overruns}
          </span>
          {stale > 0 && (
            <span className="inline-flex items-center gap-1 text-wire">
              <ClockIcon size={14} />
              {stale} stale model{stale === 1 ? '' : 's'}
            </span>
          )}
        </>
      )}
      {recording?.state === 'running' && (
        <span className="inline-flex min-w-0 items-center gap-1 text-accent" title={recording.location}>
          <RecordIcon size={14} className="shrink-0" />
          <span className="truncate">
            recording {recording.name} to {recording.temporary ? 'temporary storage' : shortPath(recording.location)}
          </span>
        </span>
      )}
      <span className="ml-auto flex shrink-0 items-center gap-5">
        <button type="button" className="cursor-pointer underline hover:text-accent disabled:cursor-not-allowed disabled:no-underline" onClick={onShowRecordings} disabled={link === 'unauthorized'}>
          Recordings
        </button>
        {health && <span>engine {health.version}</span>}
      </span>
    </footer>
  );
}
