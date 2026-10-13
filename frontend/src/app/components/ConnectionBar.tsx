import { useState, type FormEvent } from 'react';
import { ConnectIcon, DisconnectIcon } from '../icons';
import type { RegType, TargetConfig } from '../service/types';
import { Button, Field, TextInput } from './ui';

interface ConnectionBarProps {
  connected: boolean;
  connecting: boolean;
  /** The engine cannot be reached: nothing can be opened or closed. */
  offline: boolean;
  onConnect: (cfg: TargetConfig) => void;
  onDisconnect: () => void;
}

/** Parses "0, 40000, 50000" into register addresses; null when an entry is not one. */
export function parseBaseAddresses(text: string): number[] | null {
  const parts = text.split(',').map((part) => part.trim()).filter((part) => part !== '');
  const addresses = parts.map(Number);
  const valid = parts.every((part, i) => /^\d+$/.test(part) && (addresses[i] as number) <= 65535);
  return valid ? addresses : null;
}

export function ConnectionBar({ connected, connecting, offline, onConnect, onDisconnect }: ConnectionBarProps) {
  const [url, setUrl] = useState('tcp://127.0.0.1:502');
  const [unitId, setUnitId] = useState('1');
  const [timeoutMs, setTimeoutMs] = useState('2000');
  const [regType, setRegType] = useState<RegType>('holding');
  const [bases, setBases] = useState('');
  const [maxModels, setMaxModels] = useState('');

  const baseAddresses = parseBaseAddresses(bases);
  const locked = connected || connecting || offline;

  function submit(event: FormEvent) {
    event.preventDefault();
    if (connected) {
      onDisconnect();
    } else if (baseAddresses !== null) {
      onConnect({
        url: url.trim(),
        unit_id: Number(unitId),
        timeout_ms: Number(timeoutMs) || undefined,
        reg_type: regType,
        base_addresses: baseAddresses.length > 0 ? baseAddresses : undefined,
        max_models: Number(maxModels) || undefined,
      });
    }
  }

  return (
    <form onSubmit={submit} className="flex shrink-0 flex-wrap items-end gap-3 border-b border-line bg-surface px-4 py-3" aria-label="Connection">
      <Field label="Device URL">
        <TextInput className="w-64" value={url} onChange={(e) => setUrl(e.target.value)} disabled={locked} required spellCheck={false} placeholder="tcp://host:port" />
      </Field>
      <Field label="Unit ID">
        <TextInput className="w-20" type="number" min={0} max={255} value={unitId} onChange={(e) => setUnitId(e.target.value)} disabled={locked} required />
      </Field>
      <Field label="Timeout (ms)">
        <TextInput className="w-24" type="number" min={0} step={100} value={timeoutMs} onChange={(e) => setTimeoutMs(e.target.value)} disabled={locked} />
      </Field>
      <details className="group">
        <summary className="flex h-8 cursor-pointer items-center rounded px-1 text-[13px] text-muted hover:text-accent">Advanced</summary>
        <div className="mt-2 flex flex-wrap items-end gap-3">
          <Field label="Register type">
            <select className="h-8 rounded border border-control bg-bg px-2 font-mono text-[13px] text-fg-strong disabled:opacity-60" value={regType} onChange={(e) => setRegType(e.target.value as RegType)} disabled={locked}>
              <option value="holding">holding (FC03)</option>
              <option value="input">input (FC04)</option>
            </select>
          </Field>
          <Field label="Base addresses">
            <TextInput className="w-48" value={bases} onChange={(e) => setBases(e.target.value)} disabled={locked} placeholder="defaults (0, 40000, 50000)" aria-invalid={baseAddresses === null} spellCheck={false} />
          </Field>
          <Field label="Max models">
            <TextInput className="w-24" type="number" min={1} value={maxModels} onChange={(e) => setMaxModels(e.target.value)} disabled={locked} placeholder="default" />
          </Field>
          {baseAddresses === null && (
            <p className="pb-2 text-[13px] text-danger" role="alert">
              Base addresses: a comma-separated list of register addresses (0 to 65535).
            </p>
          )}
        </div>
      </details>
      {connected ? (
        <Button type="submit" icon={<DisconnectIcon />} disabled={offline}>
          Disconnect
        </Button>
      ) : (
        <Button type="submit" variant="primary" icon={<ConnectIcon />} disabled={connecting || offline || baseAddresses === null}>
          {connecting ? 'Connecting…' : 'Connect'}
        </Button>
      )}
    </form>
  );
}
