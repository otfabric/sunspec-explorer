import type { RecordingFormat, RecordingInfo } from '../service/types';
import { Button } from './ui';

interface SaveCopyProps {
  recording: RecordingInfo;
  disabled: boolean;
  onSave: (format: RecordingFormat) => void;
}

/** The two ways to keep a recording: as recorded, or converted to CSV. */
export function SaveCopy({ recording, disabled, onSave }: SaveCopyProps) {
  return (
    <span className="inline-flex items-center gap-2 font-sans">
      <span className="text-[13px] text-muted">Save a copy as</span>
      <Button onClick={() => onSave('jsonl')} disabled={disabled} aria-label={`Save a copy of ${recording.name} as JSON Lines`}>
        JSON Lines
      </Button>
      <Button onClick={() => onSave('csv')} disabled={disabled} aria-label={`Save a copy of ${recording.name} as CSV`} title="Long format, one row per point and sample">
        CSV
      </Button>
    </span>
  );
}
