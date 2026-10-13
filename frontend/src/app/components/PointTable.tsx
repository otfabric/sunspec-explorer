import { useEffect, useLayoutEffect, useMemo, useRef, type KeyboardEvent } from 'react';
import type { Row } from '../state/rows';
import { visibleColumns, type Column } from './columns';
import { ModelStatus } from './ModelStatus';
import { useWindow } from './useWindow';

// Must match the h-[30px] of rows and the h-8 of the header below: the window
// is computed from these, not measured.
const ROW_HEIGHT = 30;
const HEADER_HEIGHT = 32;

const CELL = 'overflow-hidden px-2 text-ellipsis whitespace-nowrap';

interface PointTableProps {
  rows: Row[];
  showRaw: boolean;
  showDocAddress: boolean;
  activeKey: string | null;
  onActivate: (key: string) => void;
}

function HeadingCells({ row, span }: { row: Exclude<Row, { kind: 'point' }>; span: number }) {
  if (row.kind === 'group') {
    return (
      <th colSpan={span} scope="colgroup" className={`${CELL} bg-surface text-left font-mono text-[12px] font-medium text-accent`}>
        {row.path}
        {row.label && <span className="ml-2 font-sans font-normal text-muted">{row.label}</span>}
      </th>
    );
  }
  if (row.kind === 'model') {
    return (
      <th colSpan={span} scope="colgroup" className={`${CELL} border-t border-line bg-surface text-left`}>
        <span className="font-display text-[15px] font-semibold text-fg-strong">
          {row.model.id} · {row.model.name}
        </span>
        <span className="mx-3 font-mono text-[12px] font-normal text-muted">
          #{row.model.index} @{row.model.start_address}
        </span>
        <ModelStatus result={row.model} />
        {row.model.error && <span className="ml-3 font-mono text-[12px] font-normal text-danger">{row.model.error}</span>}
      </th>
    );
  }
  return (
    <td colSpan={span} className={`${CELL} font-mono text-[12px] text-wire`}>
      undecoded — no schema · {row.model.raw_registers?.length ?? 0} raw registers · select the model to see them
    </td>
  );
}

function PointCells({ row, columns }: { row: Extract<Row, { kind: 'point' }>; columns: Column[] }) {
  // Values of a model whose last read failed are old: show them dimmed, next
  // to the status in the model heading and the sidebar.
  const dimmed = row.model.status !== 'ok' || row.model.stale ? 'opacity-60' : '';
  return (
    <>
      {columns.map((column) => (
        <td key={column.id} className={`${CELL} ${column.mono ? 'font-mono text-[13px]' : ''} ${column.id === 'value' ? dimmed : ''}`}>
          {column.cell(row.point)}
        </td>
      ))}
    </>
  );
}

/** The virtualized point table. Rows are focusable; arrow keys, Page Up/Down, Home and End move between them. */
export function PointTable({ rows, showRaw, showDocAddress, activeKey, onActivate }: PointTableProps) {
  const columns = useMemo(() => visibleColumns(showRaw, showDocAddress), [showRaw, showDocAddress]);
  const { viewport, start, end, reveal, pageSize } = useWindow(rows.length, ROW_HEIGHT, HEADER_HEIGHT);
  const above = useRef<HTMLTableRowElement>(null);
  const below = useRef<HTMLTableRowElement>(null);
  const focusPending = useRef(false);

  const activeIndex = useMemo(() => rows.findIndex((row) => row.key === activeKey), [rows, activeKey]);
  // Exactly one rendered row is in the tab order: the active one when it is in
  // the window, else the first rendered row.
  const tabStop = activeIndex >= start && activeIndex < end ? activeIndex : start;

  // The spacer heights are set through the DOM, not through a style
  // attribute, so the page works under a CSP without 'unsafe-inline'.
  useLayoutEffect(() => {
    if (above.current && below.current) {
      above.current.style.height = `${start * ROW_HEIGHT}px`;
      below.current.style.height = `${(rows.length - end) * ROW_HEIGHT}px`;
    }
  }, [start, end, rows.length]);

  useEffect(() => {
    if (!focusPending.current) {
      return;
    }
    const row = viewport.current?.querySelector<HTMLElement>(`[data-index="${activeIndex}"]`);
    if (row) {
      focusPending.current = false;
      row.focus({ preventScroll: true });
    }
  }, [activeIndex, start, end, viewport]);

  function onKeyDown(event: KeyboardEvent<HTMLTableSectionElement>) {
    const from = Math.max(0, activeIndex);
    const moves: Record<string, number> = {
      ArrowDown: from + 1,
      ArrowUp: from - 1,
      PageDown: from + pageSize,
      PageUp: from - pageSize,
      Home: 0,
      End: rows.length - 1,
    };
    const target = moves[event.key];
    const next = target === undefined ? undefined : rows[Math.min(rows.length - 1, Math.max(0, target))];
    if (!next) {
      return;
    }
    event.preventDefault();
    focusPending.current = true;
    reveal(rows.indexOf(next));
    onActivate(next.key);
  }

  return (
    <div ref={viewport} className="min-h-0 flex-1 overflow-auto">
      <table className={`w-full table-fixed border-collapse text-[13px] ${showRaw ? 'min-w-[66rem]' : 'min-w-[44rem]'}`}>
        <thead>
          <tr>
            {columns.map((column) => (
              <th key={column.id} scope="col" title={column.title} className={`ot-label sticky top-0 z-10 h-8 border-b border-line bg-bg px-2 text-left ${column.width}`}>
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody onKeyDown={onKeyDown}>
          <tr ref={above} role="presentation">
            <td colSpan={columns.length} className="p-0" />
          </tr>
          {rows.slice(start, end).map((row, i) => {
            const index = start + i;
            return (
              <tr
                key={row.key}
                data-index={index}
                tabIndex={index === tabStop ? 0 : -1}
                aria-selected={row.key === activeKey}
                onFocus={() => onActivate(row.key)}
                className={`h-[30px] border-b border-line -outline-offset-2 ${row.key === activeKey ? 'bg-accent-soft' : 'hover:bg-surface'}`}
              >
                {row.kind === 'point' ? <PointCells row={row} columns={columns} /> : <HeadingCells row={row} span={columns.length} />}
              </tr>
            );
          })}
          <tr ref={below} role="presentation">
            <td colSpan={columns.length} className="p-0" />
          </tr>
        </tbody>
      </table>
    </div>
  );
}
