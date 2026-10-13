import { describe, expect, it } from 'vitest';
import { group, model, point } from '../../test/fixtures';
import { flattenModels } from './rows';

const mppt = model(
  2,
  group('mppt', 0, [point('N', { label: 'Number of Modules' })], [
    group('module', 1, [point('DCA', { label: 'DC Current' }), point('DCV', { description: 'Voltage of the input' })]),
    group('module', 2, [point('DCA', { label: 'DC Current' }), point('DCV')]),
  ]),
);
const curves = model(
  4,
  group('DERVoltVar', 0, [point('Ena')], [
    group('Crv', 1, [point('ActPt')], [group('Pt', 1, [point('V')]), group('Pt', 2, [point('V')]), group('Pt', 3, [point('V')])]),
  ]),
);
const vendor = model(5, null, { raw_registers: [1, 2, 3] });

const describeRows = (rows: ReturnType<typeof flattenModels>) =>
  rows.map((row) => (row.kind === 'point' ? `  ${row.point.name}` : row.kind === 'group' ? row.path : `${row.kind} ${row.model.index}`));

describe('flattenModels', () => {
  it('puts the points of repeating groups under one heading per instance', () => {
    expect(describeRows(flattenModels([mppt], '', false))).toEqual([
      'mppt', '  N',
      'module [1]', '  DCA', '  DCV',
      'module [2]', '  DCA', '  DCV',
    ]);
  });

  it('names nested instances by their path', () => {
    expect(describeRows(flattenModels([curves], '', false))).toEqual([
      'DERVoltVar', '  Ena',
      'Crv [1]', '  ActPt',
      'Crv [1] / Pt [1]', '  V',
      'Crv [1] / Pt [2]', '  V',
      'Crv [1] / Pt [3]', '  V',
    ]);
  });

  it('gives every row a distinct key', () => {
    const keys = flattenModels([mppt, curves, vendor], '', true).map((row) => row.key);
    expect(new Set(keys).size).toBe(keys.length);
  });

  it('adds a heading per model and a stand-in row for a model without a schema', () => {
    const rows = describeRows(flattenModels([mppt, vendor], '', true));
    expect(rows[0]).toBe('model 2');
    expect(rows.slice(-2)).toEqual(['model 5', 'undecoded 5']);
  });
});

describe('search filter', () => {
  it('matches the point name, ignoring case', () => {
    expect(describeRows(flattenModels([mppt], 'dca', false))).toEqual(['module [1]', '  DCA', 'module [2]', '  DCA']);
  });

  it('matches the label and the description', () => {
    expect(describeRows(flattenModels([mppt], 'number of', false))).toEqual(['mppt', '  N']);
    expect(describeRows(flattenModels([mppt], ' voltage of the INPUT ', false))).toEqual(['module [1]', '  DCV']);
  });

  it('drops models and groups without a match', () => {
    expect(flattenModels([mppt, curves, vendor], 'ActPt', true).map((row) => row.kind)).toEqual(['model', 'group', 'point']);
    expect(flattenModels([mppt, vendor], 'no such point', true)).toEqual([]);
  });
});
