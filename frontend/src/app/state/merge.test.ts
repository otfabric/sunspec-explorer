import { describe, expect, it } from 'vitest';
import { group, model, point } from '../../test/fixtures';
import { mergeModel } from './merge';

const meta = { label: 'Watts', description: 'AC Power', access: 'R', mandatory: true, static: false };
const full = model(
  1,
  group('inverter', 0, [point('W', { ...meta, raw: 10, units: 'W' })], [
    { ...group('module', 1, [point('DCW', { label: 'DC Power', raw: 1 })]), label: 'Module' },
    { ...group('module', 2, [point('DCW', { label: 'DC Power', raw: 2 })]), label: 'Module' },
  ]),
);

describe('mergeModel', () => {
  it('takes values from the sample and metadata from the full result', () => {
    const compact = model(
      1,
      group('inverter', 0, [point('W', { raw: 42, scaled: 420, units: 'W' })], [
        group('module', 1, [point('DCW', { raw: 7 })]),
        group('module', 2, [point('DCW', { raw: 8 })]),
      ]),
      { read_duration_ms: 9 },
    );
    const merged = mergeModel(full, compact);
    expect(merged.read_duration_ms).toBe(9);
    expect(merged.group?.points[0]).toMatchObject({ ...meta, raw: 42, scaled: 420 });
    expect(merged.group?.groups[1]).toMatchObject({ label: 'Module', index: 2 });
    expect(merged.group?.groups[1]?.points[0]).toMatchObject({ label: 'DC Power', raw: 8 });
  });

  it('returns the sample itself when a point is renamed, added or removed', () => {
    const renamed = model(1, group('inverter', 0, [point('VA')], full.group?.groups));
    const fewerInstances = model(1, group('inverter', 0, [point('W')], [group('module', 1, [point('DCW')])]));
    expect(mergeModel(full, renamed)).toBe(renamed);
    expect(mergeModel(full, fewerInstances)).toBe(fewerInstances);
  });

  it('returns the sample itself when the previous result had no decoded group', () => {
    const compact = model(1, group('inverter', 0, [point('W')]));
    expect(mergeModel(model(1, null), compact)).toBe(compact);
  });

  it('keeps the last good values of a failed read, marked with the error and stale', () => {
    const failed = model(1, null, { status: 'error', error: 'timeout', stale: false });
    const merged = mergeModel(full, failed);
    expect(merged).toMatchObject({ status: 'error', error: 'timeout', stale: true });
    expect(merged.group).toBe(full.group);
  });

  it('does the same for a model the device no longer reports', () => {
    const merged = mergeModel(full, model(1, null, { status: 'changed', error: 'model changed' }));
    expect(merged).toMatchObject({ status: 'changed', stale: true, group: full.group });
  });

  it('carries the registers of a model without a schema', () => {
    const before = model(3, null, { raw_registers: [1, 2] });
    const after = model(3, null, { raw_registers: [3, 4] });
    expect(mergeModel(before, after).raw_registers).toEqual([3, 4]);
    expect(mergeModel(before, model(3, null, { status: 'error', error: 'x' })).raw_registers).toEqual([1, 2]);
  });
});
