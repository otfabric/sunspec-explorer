import { useEffect, useState } from 'react';
import { CheckIcon, RecordIcon, WarningIcon } from '../icons';
import type { RecordingInfo } from '../service/types';
import { formatBytes } from '../state/format';
import { ConfirmDialog } from './ConfirmDialog';
import { Dialog } from './Dialog';
import { stopReason, TEMPORARY_STORAGE } from './RecordingBar';
import { Button } from './ui';

const INTERRUPTED = 'The engine stopped unexpectedly; the file is intact up to the last complete line.';

function State({ recording }: { recording: RecordingInfo }) {
  if (recording.state === 'running') {
    return (
      <span className="inline-flex items-center gap-1 text-accent">
        <RecordIcon size={14} />
        running
      </span>
    );
  }
  if (recording.state === 'interrupted') {
    return (
      <span className="inline-flex items-center gap-1 text-wire" title={INTERRUPTED}>
        <WarningIcon size={14} />
        interrupted
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1" title={stopReason(recording)}>
      <CheckIcon size={14} />
      stopped
    </span>
  );
}

interface RecordingsDialogProps {
  /** null while the list is being fetched. */
  recordings: RecordingInfo[] | null;
  offline: boolean;
  onRefresh: () => void;
  onSave: (id: string) => void;
  onDelete: (id: string) => void;
  onClose: () => void;
}

const HEADINGS = ['Name', 'State', 'Started', 'Samples', 'Failures', 'Size', 'Actions'];
const CELL = 'py-1.5 pr-4';

/** Every recording the engine holds, with the ways to keep or remove one. */
export function RecordingsDialog({ recordings, offline, onRefresh, onSave, onDelete, onClose }: RecordingsDialogProps) {
  const [deleting, setDeleting] = useState<RecordingInfo | null>(null);
  // The list is read when the dialog opens; after that the engine's
  // recording events keep it current.
  useEffect(onRefresh, [onRefresh]);

  const storage = recordings?.[0];
  return (
    <Dialog title="Recordings" wide onClose={onClose}>
      {storage && (
        <p className="mt-2 text-[13px] text-muted">
          Stored in <span className="font-mono">{storage.location}</span>
          {storage.temporary && (
            <span className="ml-2 inline-flex items-center gap-1 text-wire">
              <WarningIcon size={14} />
              {TEMPORARY_STORAGE}
            </span>
          )}
        </p>
      )}
      <div className="mt-3 min-h-0 flex-1 overflow-auto">
        {recordings === null ? (
          <p className="text-muted">Loading…</p>
        ) : recordings.length === 0 ? (
          <p className="text-muted">There are no recordings.</p>
        ) : (
          <table className="w-full text-left text-[13px]">
            <thead>
              <tr className="border-b border-line">
                {HEADINGS.map((heading) => (
                  <th key={heading} scope="col" className={`ot-label ${CELL}`}>
                    {heading}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="font-mono">
              {recordings.map((recording) => (
                <tr key={recording.id} className="border-b border-line">
                  <th scope="row" className={`${CELL} font-medium text-fg-strong`}>
                    {recording.name}
                  </th>
                  <td className={CELL}>
                    <State recording={recording} />
                  </td>
                  <td className={CELL} title={`${recording.started_at} (UTC)`}>
                    {new Date(recording.started_at).toLocaleString()}
                  </td>
                  <td className={CELL}>{recording.samples}</td>
                  <td className={CELL}>{recording.failures}</td>
                  <td className={CELL}>{formatBytes(recording.size_bytes)}</td>
                  <td className="py-1.5">
                    <span className="flex gap-2 font-sans">
                      <Button onClick={() => onSave(recording.id)} disabled={offline} aria-label={`Save a copy of ${recording.name}`}>
                        Save a copy
                      </Button>
                      <Button
                        onClick={() => setDeleting(recording)}
                        disabled={offline || recording.state === 'running'}
                        title={recording.state === 'running' ? 'Stop the recording before deleting it' : undefined}
                        aria-label={`Delete ${recording.name}`}
                      >
                        Delete
                      </Button>
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {recordings?.some((recording) => recording.state === 'interrupted') && (
        <p className="mt-3 flex items-center gap-1.5 text-[13px] text-wire">
          <WarningIcon size={14} />
          Interrupted: {INTERRUPTED}
        </p>
      )}
      <div className="mt-5 flex justify-end">
        <Button onClick={onClose} autoFocus>
          Close
        </Button>
      </div>
      {deleting && (
        <ConfirmDialog
          title="Delete recording"
          confirmLabel="Delete"
          onCancel={() => setDeleting(null)}
          onConfirm={() => {
            onDelete(deleting.id);
            setDeleting(null);
          }}
        >
          <p>
            Delete <span className="font-mono text-fg-strong">{deleting.name}</span> from the engine&apos;s storage? This cannot be undone.
          </p>
        </ConfirmDialog>
      )}
    </Dialog>
  );
}
