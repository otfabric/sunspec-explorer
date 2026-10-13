import type { GroupView, ModelResult, PointView } from '../app/service/types';

export function point(name: string, extra: Partial<PointView> = {}): PointView {
  return { name, type: 'uint16', implemented: true, raw: 1, scaled: null, address: 40000, offset: 2, size: 1, ...extra };
}

export function group(name: string, index: number, points: PointView[], groups: GroupView[] = []): GroupView {
  return { name, index, points, groups };
}

export function model(index: number, root: GroupView | null, extra: Partial<ModelResult> = {}): ModelResult {
  return {
    index,
    id: 100 + index,
    name: `Model ${index}`,
    start_address: 40000,
    length: 10,
    schema_known: root !== null,
    status: 'ok',
    error: null,
    read_duration_ms: 5,
    warnings: [],
    stale: false,
    group: root,
    raw_registers: null,
    ...extra,
  };
}
