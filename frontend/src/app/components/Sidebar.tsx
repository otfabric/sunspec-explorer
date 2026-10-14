import { useRef, useState, type KeyboardEvent } from 'react';
import { ChevronIcon, RefreshIcon, WarningIcon } from '../icons';
import { loadSidebarCollapsed, persistSidebarCollapsed } from '../platform/sidebarStorage';
import type { ModelInfo, ModelResult, SessionInfo } from '../service/types';
import type { Selection } from '../state/store';
import { ModelStatus } from './ModelStatus';
import { Button, Chip, IconButton } from './ui';

const STATIC_HINT = 'Nothing in this model changes by itself: it is read once, not polled. Tick the box to poll it anyway.';

interface SidebarProps {
  session: SessionInfo;
  results: Record<number, ModelResult>;
  selected: Selection;
  scope: number[];
  onSelect: (selected: Selection) => void;
  onToggleScope: (index: number) => void;
  onRediscover: () => void;
  offline: boolean;
}

const ROW = 'flex items-start gap-2 border-l-2 px-3 py-2';
const ROW_SELECTED = 'border-accent bg-accent-soft text-accent-strong';
const ROW_IDLE = 'border-transparent hover:bg-surface';
// A model in the collapsed rail: its ID only.
const RAIL = 'w-full cursor-pointer border-l-2 px-1 py-1.5 text-center font-mono text-[11px]';

function ModelEntry({ model, result }: { model: ModelInfo; result: ModelResult | undefined }) {
  return (
    <>
      <span className="flex items-baseline gap-2">
        <span className="font-mono text-[11px] text-muted">#{model.index}</span>
        <span className="font-mono text-[13px] font-medium">{model.id}</span>
        <span className="truncate">{model.name}</span>
      </span>
      <span className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[12px] text-muted">
        <span>
          @{model.start_address} · {model.length} reg
        </span>
        {!model.schema_known && <Chip tone="wire">no schema</Chip>}
        {model.static_only && <Chip title={STATIC_HINT}>static</Chip>}
        <ModelStatus result={result} />
      </span>
    </>
  );
}

/**
 * The model chain in discovery order. One row is in the tab order (roving
 * tabindex); arrow keys, Home and End move between rows and select them.
 */
export function Sidebar({ session, results, selected, scope, onSelect, onToggleScope, onRediscover, offline }: SidebarProps) {
  const list = useRef<HTMLUListElement>(null);
  // Row 0 is "All models", row n is the model at chain position n - 1.
  const [cursor, setCursor] = useState(0);
  const rowCount = session.models.length + 1;

  function onKeyDown(event: KeyboardEvent<HTMLUListElement>) {
    const moves: Record<string, number> = { ArrowDown: cursor + 1, ArrowUp: cursor - 1, Home: 0, End: rowCount - 1 };
    const target = moves[event.key];
    if (target === undefined) {
      return;
    }
    event.preventDefault();
    const next = Math.min(rowCount - 1, Math.max(0, target));
    const column = (event.target as HTMLElement).dataset.col;
    const inRow = (col: string) => list.current?.querySelector<HTMLElement>(`[data-row="${next}"][data-col="${col}"]`);
    (inRow(column ?? 'select') ?? inRow('select'))?.focus();
    setCursor(next);
    onSelect(next === 0 ? 'all' : (session.models[next - 1] as ModelInfo).index);
  }

  const tabIndex = (row: number) => (row === cursor ? 0 : -1);

  // Expanded unless the viewer collapsed it earlier, to give the table room.
  const [collapsed, setCollapsed] = useState(loadSidebarCollapsed);
  function toggleCollapsed() {
    persistSidebarCollapsed(!collapsed);
    setCollapsed(!collapsed);
  }

  if (collapsed) {
    return (
      <nav className="flex w-12 shrink-0 flex-col items-center gap-1 border-r border-line py-2" aria-label="Models">
        <IconButton label="Show the model list" aria-expanded={false} onClick={toggleCollapsed}>
          <ChevronIcon size={16} />
        </IconButton>
        <ul className="mt-1 flex min-h-0 w-full flex-1 flex-col overflow-y-auto">
          <li>
            <button
              type="button"
              className={`${RAIL} ${selected === 'all' ? ROW_SELECTED : ROW_IDLE}`}
              aria-current={selected === 'all'}
              title="All models"
              onClick={() => onSelect('all')}
            >
              all
            </button>
          </li>
          {session.models.map((model) => (
            <li key={model.index}>
              <button
                type="button"
                className={`${RAIL} ${selected === model.index ? ROW_SELECTED : ROW_IDLE}`}
                aria-current={selected === model.index}
                aria-label={`Model ${model.id} ${model.name} at position ${model.index}`}
                title={`#${model.index} · ${model.id} ${model.name}`}
                onClick={() => onSelect(model.index)}
              >
                {model.id}
              </button>
            </li>
          ))}
        </ul>
      </nav>
    );
  }

  return (
    <nav className="flex w-72 shrink-0 flex-col border-r border-line" aria-label="Models">
      <div className="flex items-center gap-2 border-b border-line px-3 py-2">
        <IconButton label="Hide the model list" aria-expanded onClick={toggleCollapsed}>
          <ChevronIcon size={16} className="rotate-180" />
        </IconButton>
        <h2 className="ot-label flex-1">Models · {session.models.length}</h2>
        <Button icon={<RefreshIcon size={16} />} onClick={onRediscover} disabled={offline} title="Walk the model chain again">
          Re-discover
        </Button>
      </div>
      <ul ref={list} className="min-h-0 flex-1 overflow-y-auto" onKeyDown={onKeyDown}>
        <li className={`${ROW} ${selected === 'all' ? ROW_SELECTED : ROW_IDLE}`}>
          <button
            type="button"
            className="flex-1 cursor-pointer text-left font-medium"
            data-row={0}
            data-col="select"
            tabIndex={tabIndex(0)}
            aria-current={selected === 'all'}
            onClick={() => onSelect('all')}
            onFocus={() => setCursor(0)}
          >
            All models
          </button>
        </li>
        {session.models.map((model, i) => (
          <li key={model.index} className={`${ROW} ${selected === model.index ? ROW_SELECTED : ROW_IDLE}`}>
            <input
              type="checkbox"
              className="mt-1"
              checked={scope.includes(model.index)}
              onChange={() => onToggleScope(model.index)}
              data-row={i + 1}
              data-col="scope"
              tabIndex={tabIndex(i + 1)}
              onFocus={() => setCursor(i + 1)}
              aria-label={`Include model ${model.id} at position ${model.index} in reads and polling`}
              title="Include in reads and polling"
            />
            <button
              type="button"
              className="min-w-0 flex-1 cursor-pointer text-left"
              data-row={i + 1}
              data-col="select"
              tabIndex={tabIndex(i + 1)}
              aria-current={selected === model.index}
              onClick={() => onSelect(model.index)}
              onFocus={() => setCursor(i + 1)}
            >
              <ModelEntry model={model} result={results[model.index]} />
            </button>
          </li>
        ))}
      </ul>
      {session.warnings.length > 0 && (
        <section className="max-h-40 overflow-y-auto border-t border-line px-3 py-2" aria-label="Discovery warnings">
          <h3 className="flex items-center gap-1.5 text-[13px] font-semibold text-wire">
            <WarningIcon size={16} />
            Discovery warnings
          </h3>
          <ul className="mt-1 list-disc pl-5 text-[13px]">
            {session.warnings.map((warning) => (
              <li key={warning}>{warning}</li>
            ))}
          </ul>
        </section>
      )}
    </nav>
  );
}
