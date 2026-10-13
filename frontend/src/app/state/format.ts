import type { PointView, RawValue, Timestamp } from '../service/types';

export const hex16 = (value: number): string => `0x${value.toString(16).toUpperCase().padStart(4, '0')}`;

export function formatRaw(raw: RawValue): string {
  if (raw === null) {
    return '—';
  }
  if (Array.isArray(raw)) {
    return raw.map(hex16).join(' ');
  }
  return String(raw);
}

/** The scaled value when the point has one, with as many decimals as the scale factor implies. */
export function formatValue(point: PointView): string {
  if (point.scaled !== null) {
    return point.scaled.toFixed(Math.max(0, -(point.sf_raw ?? 0)));
  }
  return formatRaw(point.raw);
}

export function formatBytes(bytes: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB'];
  const exponent = Math.min(units.length - 1, Math.floor(Math.log2(Math.max(bytes, 1)) / 10));
  const value = bytes / 1024 ** exponent;
  return `${exponent === 0 ? value : value.toFixed(1)} ${units[exponent]}`;
}

/** The time between two timestamps as h:mm:ss, or m:ss below an hour. */
export function formatElapsed(from: Timestamp, to: Timestamp): string {
  const seconds = Math.max(0, Math.round((Date.parse(to) - Date.parse(from)) / 1000));
  const pad = (n: number) => String(n).padStart(2, '0');
  const hours = Math.floor(seconds / 3600);
  const rest = `${pad(Math.floor((seconds % 3600) / 60))}:${pad(seconds % 60)}`;
  return hours > 0 ? `${hours}:${rest}` : rest.replace(/^0/, '');
}

export function formatLocalTime(at: Timestamp): string {
  return new Date(at).toLocaleTimeString();
}
