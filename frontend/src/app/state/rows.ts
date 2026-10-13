import type { GroupView, ModelResult, PointView } from '../service/types';

export interface ModelRow {
  kind: 'model';
  key: string;
  model: ModelResult;
}

export interface GroupRow {
  kind: 'group';
  key: string;
  /** Instance path below the model, e.g. "module [2]" or "Crv [1] / Pt [3]". */
  path: string;
  label?: string;
}

export interface PointRow {
  kind: 'point';
  key: string;
  model: ModelResult;
  path: string;
  point: PointView;
}

/** Stands in for the registers of a model that has no schema. */
export interface UndecodedRow {
  kind: 'undecoded';
  key: string;
  model: ModelResult;
}

export type Row = ModelRow | GroupRow | PointRow | UndecodedRow;

function matches(point: PointView, needle: string): boolean {
  return [point.name, point.label, point.description].some((text) => text?.toLowerCase().includes(needle));
}

function groupPath(parent: string, group: GroupView): string {
  // The top-level group is the model itself (index 0); instances are 1-based.
  const own = group.index === 0 ? group.name : `${group.name} [${group.index}]`;
  return parent === '' ? own : `${parent} / ${own}`;
}

function flattenGroup(model: ModelResult, group: GroupView, parent: string, needle: string, out: Row[]): void {
  const path = groupPath(parent, group);
  const points = needle === '' ? group.points : group.points.filter((point) => matches(point, needle));
  if (points.length > 0) {
    out.push({ kind: 'group', key: `${model.index}/${path}`, path, label: group.label });
    for (const point of points) {
      out.push({ kind: 'point', key: `${model.index}/${path}/${point.name}`, model, path, point });
    }
  }
  // Nested instances are named from their parent instance, not from the model.
  const childParent = group.index === 0 ? '' : path;
  for (const child of group.groups) {
    flattenGroup(model, child, childParent, needle, out);
  }
}

/**
 * Turns decoded models into the flat row list the virtualized table renders:
 * an optional heading per model, a heading per group instance, then its
 * points. `query` filters points on name, label and description; headings
 * without a matching point are dropped.
 */
export function flattenModels(models: ModelResult[], query: string, withModelRows: boolean): Row[] {
  const needle = query.trim().toLowerCase();
  const out: Row[] = [];
  for (const model of models) {
    const rows: Row[] = [];
    if (model.group) {
      flattenGroup(model, model.group, '', needle, rows);
    } else if (needle === '') {
      rows.push({ kind: 'undecoded', key: `${model.index}/undecoded`, model });
    }
    if (rows.length > 0 && withModelRows) {
      out.push({ kind: 'model', key: `${model.index}`, model });
    }
    out.push(...rows);
  }
  return out;
}
