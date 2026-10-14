import { useState, type FormEvent } from 'react';
import { ConnectIcon, DisconnectIcon, WarningIcon } from '../icons';
import type { Feature, RegType, SerialConfig, SerialPort, TargetConfig } from '../service/types';
import { Button, Field, TextInput } from './ui';

interface ConnectionBarProps {
  connected: boolean;
  connecting: boolean;
  /** The engine cannot be reached: nothing can be opened or closed. */
  offline: boolean;
  /** What the engine can do; null while that is not known yet. */
  features: Feature[] | null;
  /** Serial devices the engine may open. */
  serialPorts: SerialPort[];
  /** The engine runs on another machine than this window (the browser UI of a daemon). */
  engineIsRemote: boolean;
  onConnect: (cfg: TargetConfig) => void;
  onDisconnect: () => void;
}

const URL_FORMS = 'tcp://host:port · rtuovertcp://host:port · tcp+tls://host:port · rtu://<device> (rtu:///dev/ttyUSB0, rtu://COM3)';

/** Parses "0, 40000, 50000" into register addresses; null when an entry is not one. */
export function parseBaseAddresses(text: string): number[] | null {
  const parts = text.split(',').map((part) => part.trim()).filter((part) => part !== '');
  const addresses = parts.map(Number);
  const valid = parts.every((part, i) => /^\d+$/.test(part) && (addresses[i] as number) <= 65535);
  return valid ? addresses : null;
}

const SELECT = 'h-8 rounded border border-control bg-bg px-2 font-mono text-[13px] text-fg-strong disabled:opacity-60';

/** Why the engine cannot open this kind of target, or null when it can (or that is not known). */
export function unsupportedReason(url: string, features: Feature[] | null): string | null {
  if (features === null) {
    return null;
  }
  if (url.startsWith('rtu://') && !features.includes('serial')) {
    return 'Serial targets are off: the daemon was not started with --allow-serial.';
  }
  if (url.startsWith('tcp+tls://') && !features.includes('tls')) {
    return 'TLS targets need the engine to be started with --tls-cert, --tls-key and --tls-ca.';
  }
  return null;
}

export function ConnectionBar({ connected, connecting, offline, features, serialPorts, engineIsRemote, onConnect, onDisconnect }: ConnectionBarProps) {
  const [url, setUrl] = useState('tcp://127.0.0.1:502');
  const [unitId, setUnitId] = useState('1');
  const [timeoutMs, setTimeoutMs] = useState('2000');
  const [regType, setRegType] = useState<RegType>('holding');
  const [bases, setBases] = useState('');
  const [maxModels, setMaxModels] = useState('');
  const [baud, setBaud] = useState('9600');
  const [dataBits, setDataBits] = useState('8');
  const [parity, setParity] = useState<NonNullable<SerialConfig['parity']>>('none');
  const [stopBits, setStopBits] = useState('1');

  const baseAddresses = parseBaseAddresses(bases);
  const locked = connected || connecting || offline;
  const target = url.trim();
  const serial = target.startsWith('rtu://');
  const unsupported = unsupportedReason(target, features);

  function submit(event: FormEvent) {
    event.preventDefault();
    if (connected) {
      onDisconnect();
    } else if (baseAddresses !== null && unsupported === null) {
      onConnect({
        url: target,
        unit_id: Number(unitId),
        timeout_ms: Number(timeoutMs) || undefined,
        reg_type: regType,
        base_addresses: baseAddresses.length > 0 ? baseAddresses : undefined,
        max_models: Number(maxModels) || undefined,
        serial: serial ? { baud: Number(baud) || undefined, data_bits: Number(dataBits), parity, stop_bits: Number(stopBits) } : undefined,
      });
    }
  }

  return (
    <form onSubmit={submit} className="flex shrink-0 flex-wrap items-end gap-3 border-b border-line bg-surface px-4 py-3" aria-label="Connection">
      <Field label="Device URL">
        <TextInput className="w-64" value={url} onChange={(e) => setUrl(e.target.value)} disabled={locked} required spellCheck={false} placeholder="tcp://host:port" title={URL_FORMS} />
      </Field>
      {serial && (
        <>
          {serialPorts.length > 0 && (
            <Field label="Serial port">
              <select className={SELECT} value={target.slice('rtu://'.length)} onChange={(e) => setUrl(`rtu://${e.target.value}`)} disabled={locked}>
                {!serialPorts.some((port) => `rtu://${port.path}` === target) && <option value={target.slice('rtu://'.length)}>choose…</option>}
                {serialPorts.map((port) => (
                  <option key={port.path} value={port.path}>
                    {port.path}
                  </option>
                ))}
              </select>
            </Field>
          )}
          <Field label="Baud">
            <TextInput className="w-24" type="number" min={50} step={1} value={baud} onChange={(e) => setBaud(e.target.value)} disabled={locked} />
          </Field>
          <Field label="Data bits">
            <select className={SELECT} value={dataBits} onChange={(e) => setDataBits(e.target.value)} disabled={locked}>
              {['5', '6', '7', '8'].map((bits) => (
                <option key={bits}>{bits}</option>
              ))}
            </select>
          </Field>
          <Field label="Parity">
            <select className={SELECT} value={parity} onChange={(e) => setParity(e.target.value as typeof parity)} disabled={locked}>
              {['none', 'even', 'odd'].map((value) => (
                <option key={value}>{value}</option>
              ))}
            </select>
          </Field>
          <Field label="Stop bits">
            <select className={SELECT} value={stopBits} onChange={(e) => setStopBits(e.target.value)} disabled={locked}>
              {['1', '2'].map((bits) => (
                <option key={bits}>{bits}</option>
              ))}
            </select>
          </Field>
        </>
      )}
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
            <select className={SELECT} value={regType} onChange={(e) => setRegType(e.target.value as RegType)} disabled={locked}>
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
        <Button type="submit" variant="primary" icon={<ConnectIcon />} disabled={connecting || offline || baseAddresses === null || unsupported !== null}>
          {connecting ? 'Connecting…' : 'Connect'}
        </Button>
      )}
      {!connected && (
        <div className="basis-full text-[12px] text-muted">
          {unsupported !== null && (
            <p className="mb-1 flex items-center gap-1.5 text-[13px] text-wire" role="alert">
              <WarningIcon size={16} />
              {unsupported}
            </p>
          )}
          {serial && (
            <p className="mb-1">
              Serial paths are on the machine the engine runs on{engineIsRemote ? ' (the gateway, not this computer); the daemon only opens the devices its operator allowed' : ''}.
            </p>
          )}
          <p>
            URL forms: <span className="font-mono">{URL_FORMS}</span>
          </p>
        </div>
      )}
    </form>
  );
}
