import type { ReactNode } from 'react';
import { WarningIcon } from '../icons';
import { Dialog } from './Dialog';
import { Button } from './ui';

export type ExportChoice = 'snapshot' | 'csv' | 'devicefile';

const CHOICES: { choice: ExportChoice; label: string; explanation: ReactNode }[] = [
  { choice: 'snapshot', label: 'Snapshot (JSON)', explanation: 'Every model with decoded points, metadata, warnings and raw registers.' },
  { choice: 'csv', label: 'Points (CSV)', explanation: 'Long format, one row per point.' },
  {
    choice: 'devicefile',
    label: 'Device file (sunspecctl)',
    explanation: (
      <>
        Replay this device with <code className="font-mono">sunspecctl serve</code>.
      </>
    ),
  },
];

/** The export formats, behind the warning that applies to all of them. */
export function ExportDialog({ onExport, onCancel }: { onExport: (choice: ExportChoice) => void; onCancel: () => void }) {
  return (
    <Dialog title="Export" onClose={onCancel}>
      <p className="mt-2 flex items-start gap-2 text-wire">
        <WarningIcon className="mt-0.5 shrink-0" />
        <span>Every export contains the device serial number and network addresses. Share the file only with people who may know them.</span>
      </p>
      <p className="mt-3 text-muted">The device is read once more for the export.</p>
      <ul className="mt-3 flex flex-col gap-3">
        {CHOICES.map(({ choice, label, explanation }) => (
          <li key={choice} className="flex items-center gap-3">
            <Button className="w-48 justify-center" onClick={() => onExport(choice)}>
              {label}
            </Button>
            <span className="text-[13px] text-muted">{explanation}</span>
          </li>
        ))}
      </ul>
      <div className="mt-5 flex justify-end">
        <Button onClick={onCancel} autoFocus>
          Cancel
        </Button>
      </div>
    </Dialog>
  );
}
