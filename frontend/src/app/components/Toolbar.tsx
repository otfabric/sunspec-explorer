import { useState } from 'react';
import { ExportIcon, PlayIcon, ReadIcon, RecordIcon, StopIcon } from '../icons';
import type { PollState } from '../service/types';
import { Button, TextInput } from './ui';

interface ToolbarProps {
  polling: PollState;
  reading: boolean;
  scopeSize: number;
  offline: boolean;
  recording: boolean;
  onReadOnce: () => void;
  onStartPolling: (intervalS: number) => void;
  onStopPolling: () => void;
  onExport: () => void;
  onRecord: () => void;
  onStopRecording: () => void;
}

export function Toolbar({ polling, reading, scopeSize, offline, recording, onReadOnce, onStartPolling, onStopPolling, onExport, onRecord, onStopRecording }: ToolbarProps) {
  const [interval, setInterval_] = useState('5');
  const seconds = Number(interval);
  const intervalValid = Number.isInteger(seconds) && seconds >= 1 && seconds <= 3600;
  const emptyScope = scopeSize === 0;
  const scopeHint = emptyScope ? 'Tick at least one model in the sidebar' : `${scopeSize} selected model${scopeSize === 1 ? '' : 's'}`;

  return (
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line px-4 py-2" role="toolbar" aria-label="Read and export">
      <Button icon={<ReadIcon />} onClick={onReadOnce} disabled={reading || emptyScope || offline} title={scopeHint}>
        {reading ? 'Reading…' : 'Read once'}
      </Button>
      <label className="ml-3 flex items-center gap-2 text-[13px]">
        Every
        <TextInput
          className="w-16"
          type="number"
          min={1}
          max={3600}
          step={1}
          value={interval}
          onChange={(event) => setInterval_(event.target.value)}
          disabled={polling.active}
          aria-label="Poll interval in whole seconds"
          aria-invalid={!intervalValid}
        />
        s
      </label>
      {polling.active ? (
        <Button icon={<StopIcon />} onClick={onStopPolling} disabled={offline}>
          Stop polling
        </Button>
      ) : (
        <Button variant="primary" icon={<PlayIcon />} onClick={() => onStartPolling(seconds)} disabled={!intervalValid || emptyScope || offline} title={scopeHint}>
          Start polling
        </Button>
      )}
      <Button className="ml-3" icon={<ExportIcon />} onClick={onExport} disabled={offline}>
        Export…
      </Button>
      {recording ? (
        <Button icon={<StopIcon />} onClick={onStopRecording} disabled={offline}>
          Stop recording
        </Button>
      ) : (
        <Button icon={<RecordIcon />} onClick={onRecord} disabled={offline} title="Record the poll samples to a file on the engine">
          Record
        </Button>
      )}
    </div>
  );
}
