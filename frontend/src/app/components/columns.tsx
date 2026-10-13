import type { ReactNode } from 'react';
import { WarningIcon } from '../icons';
import type { PointView } from '../service/types';
import { formatRaw, formatValue } from '../state/format';
import { Chip } from './ui';

export interface Column {
  id: string;
  header: string;
  /** Explains an abbreviated header. */
  title?: string;
  /** Hidden unless "Show raw details" is on. */
  raw?: boolean;
  /** Column width; the one column without it takes the remaining space. */
  width: string;
  mono?: boolean;
  cell: (point: PointView) => ReactNode;
}

export function NotImplemented() {
  return (
    <span className="inline-flex items-center gap-1 font-mono text-[12px] text-wire">
      <WarningIcon size={14} />
      not implemented
    </span>
  );
}

export function PointValue({ point }: { point: PointView }) {
  if (!point.implemented) {
    return <NotImplemented />;
  }
  if (point.symbols && point.symbols.length > 0) {
    return (
      <span className="inline-flex gap-1" title={point.symbols.join(', ')}>
        {point.symbols.map((symbol) => (
          <Chip key={symbol} tone="accent">
            {symbol}
          </Chip>
        ))}
      </span>
    );
  }
  return (
    <>
      <span className="text-fg-strong">{formatValue(point)}</span>
      {point.units && <span className="ml-1.5 text-muted">{point.units}</span>}
    </>
  );
}

const flag = (value: boolean | undefined) => (value === undefined ? '' : value ? 'yes' : '–');
// The sentinel of a point that is not implemented is not a measurement: it is
// never printed as a number.
const rawOf = (point: PointView) => (point.implemented ? formatRaw(point.raw) : '—');

const COLUMNS: Column[] = [
  { id: 'name', header: 'Point', width: 'w-32', mono: true, cell: (p) => <span className="font-medium text-fg-strong">{p.name}</span> },
  { id: 'label', header: 'Label', width: '', cell: (p) => p.label },
  { id: 'value', header: 'Value', width: 'w-52', mono: true, cell: (p) => <PointValue point={p} /> },
  { id: 'type', header: 'Type', width: 'w-24', mono: true, raw: true, cell: (p) => p.type },
  { id: 'access', header: 'Acc', title: 'Access: R (read) or RW (read/write)', width: 'w-12', mono: true, cell: (p) => p.access },
  { id: 'mandatory', header: 'M', title: 'Mandatory', width: 'w-10', mono: true, cell: (p) => flag(p.mandatory) },
  { id: 'static', header: 'S', title: 'Static', width: 'w-10', mono: true, cell: (p) => flag(p.static) },
  { id: 'sf', header: 'SF', title: 'Scale factor: name = raw value', width: 'w-32', mono: true, raw: true, cell: (p) => p.sf_name && `${p.sf_name} = ${p.sf_raw ?? '?'}` },
  { id: 'raw', header: 'Raw', title: 'Unscaled value', width: 'w-28', mono: true, raw: true, cell: rawOf },
  { id: 'address', header: 'Address', title: 'Zero-based Modbus register address', width: 'w-20', mono: true, raw: true, cell: (p) => p.address },
  { id: 'docAddress', header: 'Doc (1-based)', title: 'Address as SunSpec documentation counts: one-based', width: 'w-28', mono: true, raw: true, cell: (p) => p.address + 1 },
  { id: 'offset', header: 'Offset', title: 'Register offset from the model ID register', width: 'w-16', mono: true, raw: true, cell: (p) => p.offset },
];

export function visibleColumns(showRaw: boolean, showDocAddress: boolean): Column[] {
  return COLUMNS.filter((column) => (showRaw || !column.raw) && (showDocAddress || column.id !== 'docAddress'));
}
