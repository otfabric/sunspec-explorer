import type { GroupView, ModelResult, PointView } from '../service/types';

/** True when two decoded trees hold the same groups and points in the same places. */
function sameShape(a: GroupView | null, b: GroupView | null): boolean {
  if (a === null || b === null) {
    return a === b;
  }
  return (
    a.name === b.name &&
    a.index === b.index &&
    a.points.length === b.points.length &&
    a.groups.length === b.groups.length &&
    a.points.every((point, i) => point.name === b.points[i]?.name) &&
    a.groups.every((group, i) => sameShape(group, b.groups[i] ?? null))
  );
}

function mergePoint(full: PointView, compact: PointView): PointView {
  return {
    ...compact,
    label: full.label,
    description: full.description,
    access: full.access,
    mandatory: full.mandatory,
    static: full.static,
    units: compact.units ?? full.units,
  };
}

function mergeGroup(full: GroupView, compact: GroupView): GroupView {
  return {
    ...compact,
    label: full.label,
    // sameShape has checked that both trees line up position by position.
    points: compact.points.map((point, i) => mergePoint(full.points[i] as PointView, point)),
    groups: compact.groups.map((group, i) => mergeGroup(full.groups[i] as GroupView, group)),
  };
}

/**
 * Combines the last result of a model that carries metadata with a newer one
 * that does not (a poll sample).
 *
 * - A failed read (status error or changed) keeps the last good values
 *   visible, with the new status and error, marked stale.
 * - A good read whose tree lines up with the previous one gets its labels,
 *   descriptions, access, mandatory and static flags from the previous one.
 * - A good read with a different tree is returned as is, the very same
 *   object: the caller sees `result === compact` and asks for a full snapshot.
 */
export function mergeModel(full: ModelResult, compact: ModelResult): ModelResult {
  if (compact.status !== 'ok') {
    return {
      ...compact,
      group: compact.group ?? full.group,
      raw_registers: compact.raw_registers ?? full.raw_registers,
      stale: true,
    };
  }
  if (!sameShape(full.group, compact.group)) {
    return compact;
  }
  if (full.group === null || compact.group === null) {
    return { ...compact };
  }
  return { ...compact, group: mergeGroup(full.group, compact.group) };
}
