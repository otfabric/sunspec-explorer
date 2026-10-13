import type { ReactNode } from 'react';
import { Dialog } from './Dialog';
import { Button } from './ui';

interface ConfirmDialogProps {
  title: string;
  confirmLabel: string;
  children: ReactNode;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({ title, confirmLabel, children, onConfirm, onCancel }: ConfirmDialogProps) {
  return (
    <Dialog title={title} onClose={onCancel}>
      <div className="mt-2">{children}</div>
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onCancel} autoFocus>
          Cancel
        </Button>
        <Button variant="primary" onClick={onConfirm}>
          {confirmLabel}
        </Button>
      </div>
    </Dialog>
  );
}
