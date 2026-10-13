import { CheckIcon, RecordIcon, WarningIcon } from '../icons';
import type { PollState, RecordingInfo } from '../service/types';
import { formatBytes, formatElapsed } from '../state/format';
import { Button } from './ui';

const STOP_REASON: Record<NonNullable<RecordingInfo['stop_reason']>, string> = {
  user: 'stopped by you',
  limit: 'size limit reached',
  session_closed: 'the session was closed',
  shutdown: 'the engine shut down',
  error: 'writing failed',
};

export const TEMPORARY_STORAGE = 'Temporary storage: it is lost when the system clears its temp folder, for example on reboot.';

const plural = (count: number, word: string) => `${count} ${word}${count === 1 ? '' : 's'}`;

/** Why a recording is no longer running, in words. */
export function stopReason(recording: RecordingInfo): string {
  if (recording.state === 'interrupted') {
    return 'the engine stopped unexpectedly';
  }
  return recording.stop_reason ? STOP_REASON[recording.stop_reason] : 'ended';
}

function Counters({ recording, until }: { recording: RecordingInfo; until: string }) {
  return (
    <span className="font-mono text-[13px]">
      {formatElapsed(recording.started_at, until)} · {plural(recording.samples, 'sample')} · {plural(recording.failures, 'failure')} ·{' '}
      {formatBytes(recording.size_bytes)}
    </span>
  );
}

interface RecordingBarProps {
  recording: RecordingInfo;
  polling: PollState;
  offline: boolean;
  onSave: () => void;
  onDismiss: () => void;
}

/**
 * The recording of this session. While it runs: what has been written and
 * where. Once it has stopped: why, and the offer to save a copy. The elapsed
 * time advances with the samples; there is no clock here.
 */
export function RecordingBar({ recording, polling, offline, onSave, onDismiss }: RecordingBarProps) {
  if (recording.state === 'running') {
    const latest = polling.last_sample_at && polling.last_sample_at > recording.started_at ? polling.last_sample_at : recording.started_at;
    return (
      <section className="shrink-0 border-b border-line px-4 py-2" aria-label="Recording">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="inline-flex items-center gap-1.5 font-semibold text-accent">
            <RecordIcon />
            Recording
          </span>
          <span className="font-mono text-[13px] text-fg-strong">{recording.name}</span>
          <Counters recording={recording} until={latest} />
          {!polling.active && (
            <span className="inline-flex items-center gap-1 text-wire">
              <WarningIcon size={16} />
              recording waits for polling
            </span>
          )}
        </div>
        <p className="mt-1 text-[13px] text-muted">
          Stored in <span className="font-mono">{recording.location}</span>
          {recording.temporary && (
            <span className="ml-2 inline-flex items-center gap-1 text-wire">
              <WarningIcon size={14} />
              {TEMPORARY_STORAGE}
            </span>
          )}
        </p>
      </section>
    );
  }
  return (
    <section className={`shrink-0 border-b px-4 py-2 ${recording.temporary ? 'border-wire' : 'border-line'}`} aria-label="Recording" role="status">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className={`inline-flex items-center gap-1.5 font-semibold ${recording.temporary ? 'text-wire' : 'text-fg-strong'}`}>
          {recording.temporary ? <WarningIcon /> : <CheckIcon />}
          Recording {recording.name} stopped: {stopReason(recording)}
        </span>
        <Counters recording={recording} until={recording.stopped_at ?? recording.started_at} />
        <span className="ml-auto flex gap-2">
          <Button variant="primary" onClick={onSave} disabled={offline}>
            Save a copy
          </Button>
          <Button onClick={onDismiss}>Dismiss</Button>
        </span>
      </div>
      <p className="mt-1 text-[13px]">
        {recording.temporary ? (
          <>
            The file is in temporary storage (<span className="font-mono">{recording.location}</span>) and will be lost when the system clears its temp
            folder, for example on reboot. Save a copy now if you want to keep it.
          </>
        ) : (
          <>
            The file is kept in <span className="font-mono">{recording.location}</span>. You can save a copy somewhere else.
          </>
        )}
      </p>
    </section>
  );
}
