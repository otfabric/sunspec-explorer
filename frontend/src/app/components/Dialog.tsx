import { useId, type ReactNode } from 'react';

interface DialogProps {
  title: string;
  wide?: boolean;
  onClose: () => void;
  children: ReactNode;
}

/** A modal panel over the window. Escape closes the topmost one. */
export function Dialog({ title, wide = false, onClose, children }: DialogProps) {
  const titleId = useId();
  return (
    <div
      className="fixed inset-0 z-20 flex items-center justify-center bg-bg/80 p-4"
      onKeyDown={(event) => {
        if (event.key === 'Escape') {
          event.stopPropagation();
          onClose();
        }
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className={`flex max-h-full w-full flex-col rounded border border-control bg-surface p-5 ${wide ? 'max-w-5xl' : 'max-w-md'}`}
      >
        <h2 id={titleId} className="font-display text-xl font-semibold text-fg-strong">
          {title}
        </h2>
        {children}
      </div>
    </div>
  );
}
