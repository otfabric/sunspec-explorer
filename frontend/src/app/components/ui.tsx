import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from 'react';

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'default';
  icon?: ReactNode;
}

const BUTTON_BASE =
  'inline-flex h-8 shrink-0 cursor-pointer items-center gap-1.5 rounded border px-3 text-[13px] font-medium whitespace-nowrap ' +
  'disabled:cursor-not-allowed disabled:opacity-50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50';
const BUTTON_VARIANT = {
  primary: 'border-accent bg-accent text-bg',
  default: 'border-control text-fg enabled:hover:border-accent enabled:hover:text-accent',
};

export function Button({ variant = 'default', icon, children, className = '', ...props }: ButtonProps) {
  return (
    <button type="button" className={`${BUTTON_BASE} ${BUTTON_VARIANT[variant]} ${className}`} {...props}>
      {icon}
      {children}
    </button>
  );
}

/** An icon-only button: the label is its accessible name and its tooltip. */
export function IconButton({ label, children, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      className="inline-flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-full border border-line text-fg hover:border-accent hover:text-accent"
      {...props}
    >
      {children}
    </button>
  );
}

const INPUT = 'h-8 rounded border border-control bg-bg px-2 font-mono text-[13px] text-fg-strong disabled:opacity-60';

export function TextInput({ className = '', ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={`${INPUT} ${className}`} {...props} />;
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="ot-label">{label}</span>
      {children}
    </label>
  );
}

export function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label className="flex cursor-pointer items-center gap-1.5 text-[13px] whitespace-nowrap">
      <input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} />
      {label}
    </label>
  );
}

const CHIP_TONE = {
  accent: 'border-accent bg-accent-soft text-accent-strong',
  neutral: 'border-line text-muted',
  wire: 'border-wire text-wire',
};

export function Chip({ tone = 'neutral', title, children }: { tone?: keyof typeof CHIP_TONE; title?: string; children: ReactNode }) {
  return (
    <span title={title} className={`inline-flex h-5 items-center gap-1 rounded border px-1.5 font-mono text-[11px] whitespace-nowrap ${CHIP_TONE[tone]}`}>
      {children}
    </span>
  );
}
