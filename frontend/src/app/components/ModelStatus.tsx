import type { ModelResult } from '../service/types';
import { CheckIcon, ClockIcon, ErrorIcon, WarningIcon } from '../icons';

/**
 * The state of a model's last read as an icon plus a word, never as a color
 * alone. A failed read keeps its last good values, so it is also stale.
 */
export function ModelStatus({ result }: { result: ModelResult | undefined }) {
  if (!result) {
    return <span className="font-mono text-[12px] text-muted">not read</span>;
  }
  const { icon, label, tone } = describe(result);
  return (
    <span className={`inline-flex items-center gap-1 font-mono text-[12px] whitespace-nowrap ${tone}`}>
      {icon}
      {label}
    </span>
  );
}

function describe(result: ModelResult) {
  if (result.status === 'error') {
    return { icon: <ErrorIcon size={14} />, label: 'error · stale', tone: 'text-danger' };
  }
  if (result.status === 'changed') {
    return { icon: <WarningIcon size={14} />, label: 'changed · stale', tone: 'text-wire' };
  }
  if (result.stale) {
    return { icon: <ClockIcon size={14} />, label: 'stale', tone: 'text-wire' };
  }
  return { icon: <CheckIcon size={14} />, label: 'ok', tone: 'text-accent' };
}
