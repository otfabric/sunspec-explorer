import { WarningIcon } from '../icons';
import type { ModelInfo, ModelResult } from '../service/types';
import { ModelStatus } from './ModelStatus';

/** Heading of the selected model: where it sits and how its last read went. */
export function ModelSummary({ model, result }: { model: ModelInfo; result: ModelResult | undefined }) {
  return (
    <div className="shrink-0 border-b border-line px-4 py-2">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h2 className="font-display text-lg font-semibold text-fg-strong">
          {model.id} · {model.name}
        </h2>
        <span className="font-mono text-[12px] text-muted">
          #{model.index} · address {model.start_address} · {model.length} registers
          {result && ` · read in ${result.read_duration_ms} ms`}
        </span>
        <ModelStatus result={result} />
      </div>
      {result?.error && (
        <p className="mt-1 font-mono text-[13px] text-danger" role="alert">
          {result.error}
          {result.group && ' — showing the last good values'}
        </p>
      )}
      {result?.warnings.map((warning) => (
        <p key={warning} className="mt-1 flex items-center gap-1.5 text-[13px] text-wire">
          <WarningIcon size={14} />
          {warning}
        </p>
      ))}
    </div>
  );
}
