import { describe, expect, it } from 'vitest';
import { estimateSkewMs, isCompatible } from './engine';

const at = (iso: string) => Date.parse(iso);

describe('estimateSkewMs', () => {
  it('is zero when the engine time is the midpoint of the request', () => {
    expect(estimateSkewMs('2026-10-14T10:00:00.100Z', at('2026-10-14T10:00:00.000Z'), at('2026-10-14T10:00:00.200Z'))).toBe(0);
  });

  it('is positive when the engine clock is ahead and negative when it is behind', () => {
    const sent = at('2026-10-14T10:00:00Z');
    expect(estimateSkewMs('2026-10-14T10:00:12Z', sent, sent + 400)).toBe(11_800);
    expect(estimateSkewMs('2026-10-14T09:59:30Z', sent, sent + 400)).toBe(-30_200);
  });

  it('reads the nanosecond timestamps of the engine', () => {
    const sent = at('2026-10-14T10:00:00Z');
    expect(estimateSkewMs('2026-10-14T10:00:07.250123456Z', sent, sent)).toBe(7250);
  });
});

describe('isCompatible', () => {
  it('accepts every 1.x and nothing else', () => {
    expect(isCompatible('1.0')).toBe(true);
    expect(isCompatible('1.7')).toBe(true);
    expect(isCompatible('2.0')).toBe(false);
    expect(isCompatible('0.9')).toBe(false);
    expect(isCompatible('')).toBe(false);
  });
});
