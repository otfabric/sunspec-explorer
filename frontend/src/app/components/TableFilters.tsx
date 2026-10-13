import { SearchIcon } from '../icons';
import { TextInput, Toggle } from './ui';

interface TableFiltersProps {
  query: string;
  onQuery: (query: string) => void;
  showRaw: boolean;
  onShowRaw: (show: boolean) => void;
  showDocAddress: boolean;
  onShowDocAddress: (show: boolean) => void;
  matchCount: number;
}

export function TableFilters({ query, onQuery, showRaw, onShowRaw, showDocAddress, onShowDocAddress, matchCount }: TableFiltersProps) {
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-4 border-b border-line px-4 py-2">
      <label className="relative flex items-center">
        <SearchIcon size={16} className="pointer-events-none absolute left-2 text-muted" />
        <TextInput
          className="w-72 pl-8"
          type="search"
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Filter name, label, description"
          aria-label="Filter points by name, label or description"
        />
      </label>
      <span className="font-mono text-[12px] text-muted" aria-live="polite">
        {matchCount} point{matchCount === 1 ? '' : 's'}
      </span>
      <span className="ml-auto flex items-center gap-4">
        <Toggle label="Show raw details" checked={showRaw} onChange={onShowRaw} />
        {showRaw && <Toggle label="1-based address (SunSpec docs)" checked={showDocAddress} onChange={onShowDocAddress} />}
      </span>
    </div>
  );
}
