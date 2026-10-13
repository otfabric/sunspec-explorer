import { CopyIcon } from '../icons';
import type { PointRow } from '../state/rows';
import { formatRaw, formatValue } from '../state/format';
import { NotImplemented } from './columns';
import { Chip, IconButton } from './ui';

interface PointDetailProps {
  row: PointRow | null;
  onCopy: (text: string) => void;
}

/** Everything about the focused point, including the description that does not fit in a table row. */
export function PointDetail({ row, onCopy }: PointDetailProps) {
  if (!row) {
    return (
      <aside className="shrink-0 border-t border-line bg-surface px-4 py-3 text-muted" aria-label="Point details">
        Select a point to see its description.
      </aside>
    );
  }
  const { point, model, path } = row;
  const value = point.implemented ? `${formatValue(point)}${point.units ? ` ${point.units}` : ''}` : 'not implemented';
  return (
    <aside className="max-h-44 shrink-0 overflow-y-auto border-t border-line bg-surface px-4 py-3" aria-label="Point details">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h3 className="font-mono text-[14px] font-medium text-fg-strong">{point.name}</h3>
        {point.label && <span>{point.label}</span>}
        <span className="font-mono text-[12px] text-muted">
          model {model.id} #{model.index} · {path}
        </span>
        <span className="ml-auto flex items-center gap-2 font-mono text-[13px]">
          {point.implemented ? <span className="text-fg-strong">{value}</span> : <NotImplemented />}
          <IconButton label="Copy value" onClick={() => onCopy(value)}>
            <CopyIcon size={16} />
          </IconButton>
        </span>
      </div>
      <p className="mt-1.5 max-w-4xl">{point.description ?? <span className="text-muted">No description.</span>}</p>
      {point.symbols && point.symbols.length > 0 && (
        <p className="mt-2 flex flex-wrap gap-1">
          {point.symbols.map((symbol) => (
            <Chip key={symbol} tone="accent">
              {symbol}
            </Chip>
          ))}
        </p>
      )}
      <p className="mt-2 font-mono text-[12px] text-muted">
        {point.type} · {point.size} reg · address {point.address} (zero-based) / {point.address + 1} (SunSpec documentation, one-based) · offset {point.offset}
        {point.implemented && ` · raw ${formatRaw(point.raw)}`}
        {point.sf_name && ` · ${point.sf_name} = ${point.sf_raw ?? '?'}`}
      </p>
    </aside>
  );
}
