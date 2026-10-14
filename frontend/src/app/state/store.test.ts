import { describe, expect, it } from 'vitest';
import { group, model, point } from '../../test/fixtures';
import type { ExplorerEvent, Health, ModelResult, RecordingInfo, Sample, SessionInfo, Snapshot } from '../service/types';
import { initialState, reduce, type ExplorerState } from './store';

const session: SessionInfo = {
  id: 's1',
  target: { url: 'tcp://10.0.0.1:502', unit_id: 1, reg_type: 'holding', base_address: 40000 },
  status: 'connected',
  opened_at: '2026-10-13T10:00:00Z',
  discovered_at: '2026-10-13T10:00:00Z',
  models: [0, 1].map((index) => ({ index, id: 100 + index, name: `Model ${index}`, start_address: 40002, length: 10, schema_known: true, decoding_supported: true, static_only: index === 0 })),
  warnings: [],
  attempts: [],
};

const snapshot = (models: ModelResult[]): Snapshot => ({
  schema: 'otfabric.sunspec-explorer.snapshot/1',
  app: { name: 'sunspec-explorer', version: 'test' },
  libs: { go_sunspec: 'test', registry_models: 1 },
  session_id: 's1',
  captured_at: '2026-10-13T10:00:01Z',
  read_duration_ms: 1,
  metadata: 'full',
  target: session.target,
  device: null,
  models,
  warnings: [],
});

const sampleEvent = (seq: number, models: ModelResult[], sessionId = 's1'): ExplorerEvent => {
  const sample: Sample = { session_id: sessionId, seq, captured_at: '2026-10-13T10:00:05Z', read_duration_ms: 1, overruns: 0, metadata: 'compact', models };
  return { seq, kind: 'sample', session_id: sessionId, at: sample.captured_at, sample };
};

function connected(): ExplorerState {
  let state = reduce(initialState, { type: 'discovered', session });
  state = reduce(state, { type: 'fullRequested' });
  return reduce(state, { type: 'snapshot', snapshot: snapshot([model(0, group('common', 0, [point('Mn', { label: 'Manufacturer' })])), model(1, group('inverter', 0, [point('W', { label: 'Watts' })]))]) });
}

describe('explorer reducer', () => {
  it('asks for a full snapshot of every model after discovery and leaves static models out of the scope', () => {
    const state = reduce(initialState, { type: 'discovered', session });
    expect(state.wantFull).toEqual([0, 1]);
    expect(state.scope).toEqual([1]);
  });

  it('merges samples into the full results and counts cycles', () => {
    const state = reduce(connected(), { type: 'event', event: sampleEvent(3, [model(1, group('inverter', 0, [point('W', { raw: 99 })]))]) });
    expect(state.results[1]?.group?.points[0]).toMatchObject({ label: 'Watts', raw: 99 });
    expect(state.polling).toMatchObject({ cycles: 3, failures: 0, last_sample_at: '2026-10-13T10:00:05Z' });
    expect(state.wantFull).toEqual([]);
  });

  it('keeps the last good values when a model fails and counts the failure', () => {
    const state = reduce(connected(), { type: 'event', event: sampleEvent(1, [model(1, null, { status: 'error', error: 'timeout' })]) });
    expect(state.results[1]).toMatchObject({ status: 'error', error: 'timeout', stale: true });
    expect(state.results[1]?.group?.points[0]?.label).toBe('Watts');
    expect(state.polling.failures).toBe(1);
  });

  it('requests full metadata once when a sample has a new shape', () => {
    const reshaped = [model(1, group('inverter', 0, [point('W'), point('VA')]))];
    let state = reduce(connected(), { type: 'event', event: sampleEvent(1, reshaped) });
    expect(state.wantFull).toEqual([1]);
    state = reduce(state, { type: 'fullRequested' });
    state = reduce(state, { type: 'event', event: sampleEvent(2, [model(1, group('inverter', 0, [point('W'), point('VA'), point('VAr')]))]) });
    expect(state.wantFull).toEqual([]);
    expect(state.fullInFlight).toEqual([1]);
  });

  it('ignores samples of another session', () => {
    const before = connected();
    const after = reduce(before, { type: 'event', event: sampleEvent(1, [model(1, null)], 'other') });
    expect(after.results).toBe(before.results);
    expect(after.polling).toBe(before.polling);
  });

  it('starts over from a changed discovery', () => {
    const changed = { ...session, discovered_at: '2026-10-13T11:00:00Z', models: session.models.slice(0, 1) };
    const state = reduce(connected(), { type: 'event', event: { seq: 9, kind: 'discovery_changed', session_id: 's1', at: changed.discovered_at, session: changed } });
    expect(state.session).toBe(changed);
    expect(state.results).toEqual({});
    expect(state.wantFull).toEqual([0]);
  });

  it('fetches the session state after a resync', () => {
    expect(reduce(connected(), { type: 'event', event: { seq: 0, kind: 'resync', at: '2026-10-13T10:00:00Z' } }).wantState).toBe(true);
  });
});

describe('link to the engine', () => {
  const resync: ExplorerEvent = { seq: 40, kind: 'resync', at: '2026-10-13T10:05:00Z' };
  const afterEvents = () => reduce(connected(), { type: 'event', event: sampleEvent(1, [model(1, group('inverter', 0, [point('W', { raw: 5 })]))]) });

  it('keeps the session and its values while the link is down', () => {
    const before = afterEvents();
    const down = reduce(before, { type: 'link', status: 'down' });
    expect(down).toMatchObject({ link: 'down', restored: null });
    expect(down.session).toBe(before.session);
    expect(down.results).toBe(before.results);
  });

  it('says that events were replayed when the stream resumes', () => {
    const up = reduce(reduce(afterEvents(), { type: 'link', status: 'down' }), { type: 'link', status: 'up' });
    expect(up).toMatchObject({ link: 'up', restored: 'replayed', wantState: false });
  });

  it('says that events were missed when a resync follows, and reads the state again', () => {
    const up = reduce(reduce(afterEvents(), { type: 'link', status: 'down' }), { type: 'link', status: 'up' });
    expect(reduce(up, { type: 'event', event: resync })).toMatchObject({ restored: 'gap', wantState: true });
  });

  it('reads the state again when there was no event to resume from', () => {
    const up = reduce(reduce(connected(), { type: 'link', status: 'down' }), { type: 'link', status: 'up' });
    expect(up.wantState).toBe(true);
  });

  it('retries a state fetch that failed while the link was down', () => {
    let state = reduce(afterEvents(), { type: 'event', event: resync });
    state = reduce(state, { type: 'link', status: 'down' });
    state = reduce(state, { type: 'stateFailed' });
    expect(state.wantState).toBe(false);
    expect(reduce(state, { type: 'link', status: 'up' }).wantState).toBe(true);
  });

  it('does not announce a restore when the link was never lost', () => {
    expect(reduce(initialState, { type: 'link', status: 'up' }).restored).toBeNull();
  });

  it('keeps a refused token across a disconnect', () => {
    const refused = reduce(connected(), { type: 'link', status: 'unauthorized' });
    expect(reduce(refused, { type: 'disconnected' }).link).toBe('unauthorized');
  });
});

describe('attaching to a session the engine already has', () => {
  it('takes over the session, its polling and its last sample, and asks for metadata', () => {
    const polling = { active: true, interval_s: 2, model_indexes: [1], cycles: 7, failures: 1, overruns: 0 };
    const last = sampleEvent(7, [model(1, group('inverter', 0, [point('W', { raw: 5 })]))]).sample as Sample;
    const state = reduce(initialState, { type: 'attached', state: { session, polling, last_sample: last, recording: null } });
    expect(state.session).toBe(session);
    expect(state.polling).toBe(polling);
    expect(state.scope).toEqual([1]);
    expect(state.results[1]?.group?.points[0]?.raw).toBe(5);
    expect(state.wantFull).toEqual([0, 1]);
  });
});


describe('recording', () => {
  const recording = (extra: Partial<RecordingInfo> = {}): RecordingInfo => ({
    id: 'r1',
    session_id: 's1',
    name: 'night-run',
    state: 'running',
    started_at: '2026-10-13T10:00:00Z',
    samples: 0,
    failures: 0,
    size_bytes: 120,
    files: 1,
    location: '/tmp/sunspec-explorer-501',
    temporary: true,
    ...extra,
  });
  const status = (info: RecordingInfo): ExplorerEvent => ({ seq: 20, kind: 'recording_status', session_id: info.session_id, at: info.started_at, recording: info });
  const sample = (info: RecordingInfo): ExplorerEvent => ({
    ...sampleEvent(4, [model(1, group('inverter', 0, [point('W', { raw: 5 })]))]),
    recording: info,
  });

  it('follows recording_status events of its session', () => {
    const started = reduce(connected(), { type: 'event', event: status(recording()) });
    expect(started.recording).toMatchObject({ id: 'r1', state: 'running' });
    const stopped = reduce(started, { type: 'event', event: status(recording({ state: 'stopped', stop_reason: 'limit', stopped_at: '2026-10-13T10:10:00Z' })) });
    expect(stopped.recording).toMatchObject({ state: 'stopped', stop_reason: 'limit' });
  });

  it('takes the counters that sample events carry', () => {
    const started = reduce(connected(), { type: 'recording', recording: recording() });
    const next = reduce(started, { type: 'event', event: sample(recording({ samples: 4, failures: 1, size_bytes: 9000 })) });
    expect(next.recording).toMatchObject({ samples: 4, failures: 1, size_bytes: 9000 });
    expect(next.polling.cycles).toBe(4);
  });

  it('ignores the recording of another session, except in the list', () => {
    const listed = reduce(connected(), { type: 'recordings', recordings: [] });
    const next = reduce(listed, { type: 'event', event: status(recording({ id: 'r9', session_id: 'other' })) });
    expect(next.recording).toBeNull();
    expect(next.recordings?.map((entry) => entry.id)).toEqual(['r9']);
  });

  it('keeps the list current: new recordings in front, known ones replaced, deleted ones removed', () => {
    let state = reduce(connected(), { type: 'recordings', recordings: [recording({ id: 'old', state: 'interrupted' })] });
    state = reduce(state, { type: 'event', event: status(recording()) });
    state = reduce(state, { type: 'event', event: status(recording({ state: 'stopped', stop_reason: 'user' })) });
    expect(state.recordings?.map((entry) => [entry.id, entry.state])).toEqual([['r1', 'stopped'], ['old', 'interrupted']]);
    state = reduce(state, { type: 'recordingDeleted', id: 'r1' });
    expect(state.recordings?.map((entry) => entry.id)).toEqual(['old']);
    expect(state.recording).toBeNull();
  });

  it('still learns how the recording ended after the session was closed', () => {
    let state = reduce(connected(), { type: 'recording', recording: recording() });
    state = reduce(state, { type: 'disconnected' });
    expect(state.recording?.state).toBe('running');
    state = reduce(state, { type: 'event', event: status(recording({ state: 'stopped', stop_reason: 'session_closed' })) });
    expect(state.recording).toMatchObject({ state: 'stopped', stop_reason: 'session_closed' });
  });

  it('adopts the running recording when attaching and drops one that ended unseen', () => {
    const polling = { active: true, interval_s: 2, model_indexes: [1], cycles: 7, failures: 0, overruns: 0 };
    const attached = reduce(initialState, { type: 'attached', state: { session, polling, last_sample: null, recording: recording({ samples: 7 }) } });
    expect(attached.recording?.samples).toBe(7);
    const later = reduce(attached, { type: 'state', state: { session, polling, last_sample: null, recording: null } });
    expect(later.recording).toBeNull();
  });
});

describe('engine health', () => {
  const health = (extra: Partial<Health> = {}): Health => ({
    status: 'ok',
    version: '0.0.2',
    api_version: '1.1',
    uptime_s: 1,
    go_sunspec: 'v0.5.0',
    registry_models: 100,
    time: '2026-10-14T10:00:00Z',
    features: ['recording'],
    ...extra,
  });

  it('remembers the first versions and reports a later change', () => {
    const first = reduce(initialState, { type: 'health', health: health(), skewMs: 12 });
    expect(first).toMatchObject({ baseline: { version: '0.0.2', api_version: '1.1' }, updatedTo: null, skewMs: 12 });
    expect(reduce(first, { type: 'health', health: health({ uptime_s: 99 }), skewMs: -8000 })).toMatchObject({ updatedTo: null, skewMs: -8000 });
    expect(reduce(first, { type: 'health', health: health({ version: '0.0.3' }), skewMs: 0 }).updatedTo).toBe('0.0.3');
    expect(reduce(first, { type: 'health', health: health({ api_version: '1.2' }), skewMs: 0 }).updatedTo).toBe('0.0.2');
  });

  it('keeps what is known about the engine across a disconnect', () => {
    let state = reduce(connected(), { type: 'health', health: health(), skewMs: 9000 });
    state = reduce(state, { type: 'serialPorts', ports: [{ path: '/dev/ttyUSB0' }] });
    state = reduce(reduce(state, { type: 'dismissSkew' }), { type: 'disconnected' });
    expect(state).toMatchObject({ skewMs: 9000, skewDismissed: true, serialPorts: [{ path: '/dev/ttyUSB0' }], baseline: { version: '0.0.2' } });
  });
});
