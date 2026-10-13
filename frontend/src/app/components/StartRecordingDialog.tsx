import { useState, type FormEvent } from 'react';
import { RecordIcon } from '../icons';
import { Dialog } from './Dialog';
import { Button, Field, TextInput } from './ui';

const NAME = /^[a-zA-Z0-9._-]{0,64}$/;

interface StartRecordingDialogProps {
  polling: boolean;
  /** Called with the chosen name; empty lets the engine pick one. */
  onStart: (name: string) => void;
  onCancel: () => void;
}

export function StartRecordingDialog({ polling, onStart, onCancel }: StartRecordingDialogProps) {
  const [name, setName] = useState('');
  const valid = NAME.test(name);

  function submit(event: FormEvent) {
    event.preventDefault();
    if (valid) {
      onStart(name);
    }
  }

  return (
    <Dialog title="Start recording" onClose={onCancel}>
      <form onSubmit={submit}>
        <p className="mt-2">
          The engine writes every poll sample to a JSON Lines file until you stop the recording.
          {!polling && ' Polling is not running: the recording waits until you start it.'}
        </p>
        <div className="mt-4">
          <Field label="Name (optional)">
            <TextInput value={name} onChange={(event) => setName(event.target.value)} placeholder="chosen by the engine" aria-invalid={!valid} spellCheck={false} autoFocus />
          </Field>
          {!valid && (
            <p className="mt-1 text-[13px] text-danger" role="alert">
              Use up to 64 letters, digits, dots, underscores or hyphens.
            </p>
          )}
        </div>
        <div className="mt-5 flex justify-end gap-2">
          <Button onClick={onCancel}>Cancel</Button>
          <Button type="submit" variant="primary" icon={<RecordIcon />} disabled={!valid}>
            Start recording
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
