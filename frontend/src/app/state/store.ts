import type { LinkStatus, ServiceError } from '../service/service';
import type {
  ExplorerEvent,
  Health,
  ModelResult,
  PollState,
  RecordingInfo,
  Sample,
  SessionInfo,
  SessionState,
  Snapshot,
} from '../service/types';
import { mergeModel } from './merge';

export type Selection = number | 'all';

export interface ExplorerState {
  health: Health | null;
  session: SessionInfo | null;
  connecting: boolean;
  connectError: ServiceError | null;
  /** Latest result per chain position, with metadata whenever it is known. */
  results: Record<number, ModelResult>;
  polling: PollState;
  reading: boolean;
  /** The last problem worth telling the user about. */
  notice: string | null;
  selected: Selection;
  /** Chain positions included in a single read and in polling. */
  scope: number[];
  /** Models that need a snapshot with full metadata, and those being fetched. */
  wantFull: number[];
  fullInFlight: number[];
  /** Set when events were missed and the session state must be fetched again. */
  wantState: boolean;
  /** A fetch of the session state failed; it is tried again when the link is back. */
  stateMissed: boolean;
  /** The link to the engine; only the web UI can lose it. */
  link: LinkStatus;
  /**
   * How a lost link came back: 'replayed' when the stream resumed where it
   * broke off, 'gap' when the engine said that events were missed.
   */
  restored: 'replayed' | 'gap' | null;
  /** False until the first engine event: until then there is nothing to resume from. */
  seenEvents: boolean;
  /**
   * The recording of this session: the running one, or the one that stopped
   * last, kept until the user has dealt with the offer to save a copy. It
   * outlives the session, because closing a session stops its recording.
   */
  recording: RecordingInfo | null;
  /** Every recording the engine holds, newest first; null until first listed. */
  recordings: RecordingInfo[] | null;
}

export type Action =
  | { type: 'health'; health: Health }
  | { type: 'notice'; message: string | null }
  | { type: 'connecting' }
  | { type: 'connectFailed'; error: ServiceError }
  | { type: 'discovered'; session: SessionInfo }
  | { type: 'disconnected' }
  | { type: 'reading'; reading: boolean }
  | { type: 'snapshot'; snapshot: Snapshot }
  | { type: 'fullRequested' }
  | { type: 'fullFailed'; indexes: number[] }
  | { type: 'state'; state: SessionState }
  | { type: 'stateFailed' }
  | { type: 'attached'; state: SessionState }
  | { type: 'link'; status: LinkStatus }
  | { type: 'dismissRestored' }
  | { type: 'recording'; recording: RecordingInfo }
  | { type: 'recordings'; recordings: RecordingInfo[] }
  | { type: 'recordingDeleted'; id: string }
  | { type: 'dismissRecording' }
  | { type: 'event'; event: ExplorerEvent }
  | { type: 'select'; selected: Selection }
  | { type: 'toggleScope'; index: number };

const idlePolling: PollState = { active: false, interval_s: 5, model_indexes: [], cycles: 0, failures: 0, overruns: 0 };

export const initialState: ExplorerState = {
  health: null,
  session: null,
  connecting: false,
  connectError: null,
  results: {},
  polling: idlePolling,
  reading: false,
  notice: null,
  selected: 'all',
  scope: [],
  wantFull: [],
  fullInFlight: [],
  wantState: false,
  stateMissed: false,
  link: 'up',
  restored: null,
  seenEvents: false,
  recording: null,
  recordings: null,
};

/** Forgets the session, not what is known about the engine and the link to it. */
const withoutSession = (state: ExplorerState): ExplorerState => ({
  ...initialState,
  health: state.health,
  link: state.link,
  seenEvents: state.seenEvents,
  recording: state.recording,
  recordings: state.recordings,
});

const without = (list: number[], remove: number[]) => list.filter((index) => !remove.includes(index));

/** A new discovery result: everything read from the old chain is dropped. */
function withDiscovery(state: ExplorerState, session: SessionInfo): ExplorerState {
  const indexes = session.models.map((model) => model.index);
  return {
    ...state,
    session,
    connecting: false,
    connectError: null,
    results: {},
    selected: state.selected !== 'all' && indexes.includes(state.selected) ? state.selected : 'all',
    // Static-only models (e.g. Common) are read once, not in every poll cycle.
    scope: session.models.filter((model) => !model.static_only).map((model) => model.index),
    wantFull: indexes,
    fullInFlight: [],
  };
}

function withSnapshot(state: ExplorerState, snapshot: Snapshot): ExplorerState {
  if (snapshot.session_id !== state.session?.id) {
    return state;
  }
  const results = { ...state.results };
  for (const model of snapshot.models) {
    const previous = results[model.index];
    results[model.index] = previous && model.status !== 'ok' ? mergeModel(previous, model) : model;
  }
  const answered = snapshot.models.map((model) => model.index);
  return { ...state, results, reading: false, fullInFlight: without(state.fullInFlight, answered) };
}

function withSample(state: ExplorerState, sample: Sample): ExplorerState {
  if (sample.session_id !== state.session?.id) {
    return state;
  }
  const results = { ...state.results };
  const wantFull = [...state.wantFull];
  for (const model of sample.models) {
    const previous = results[model.index];
    const merged = previous ? mergeModel(previous, model) : model;
    results[model.index] = merged;
    // The sample came back unmerged: its metadata has to be fetched.
    const needsMetadata = merged === model && model.group !== null;
    if (needsMetadata && !wantFull.includes(model.index) && !state.fullInFlight.includes(model.index)) {
      wantFull.push(model.index);
    }
  }
  return { ...state, results, wantFull };
}

function withSampleEvent(state: ExplorerState, sample: Sample): ExplorerState {
  const next = withSample(state, sample);
  if (next === state) {
    return state;
  }
  const failed = sample.models.some((model) => model.status !== 'ok');
  return {
    ...next,
    polling: {
      ...state.polling,
      cycles: sample.seq,
      overruns: sample.overruns,
      failures: state.polling.failures + (failed ? 1 : 0),
      last_sample_at: sample.captured_at,
    },
  };
}

function withSessionState(state: ExplorerState, fetched: SessionState): ExplorerState {
  if (fetched.session.id !== state.session?.id) {
    return state;
  }
  const rediscovered = fetched.session.discovered_at !== state.session.discovered_at;
  const base = rediscovered ? withDiscovery(state, fetched.session) : { ...state, session: fetched.session };
  const next = fetched.last_sample ? withSample(base, fetched.last_sample) : base;
  return { ...withSessionRecording(next, fetched.recording), polling: fetched.polling, wantState: false, stateMissed: false };
}

/** A session that was already open on the engine, e.g. after a page reload. */
function withAttached(state: ExplorerState, fetched: SessionState): ExplorerState {
  const base = withDiscovery(state, fetched.session);
  const next = fetched.last_sample ? withSample(base, fetched.last_sample) : base;
  return {
    ...withSessionRecording(next, fetched.recording),
    polling: fetched.polling,
    scope: fetched.polling.active ? fetched.polling.model_indexes : next.scope,
  };
}

/** News about one recording, from a Service result or an event. */
function withRecording(state: ExplorerState, recording: RecordingInfo): ExplorerState {
  const mine = recording.session_id === state.session?.id || recording.id === state.recording?.id;
  const known = state.recordings?.some((candidate) => candidate.id === recording.id);
  return {
    ...state,
    recording: mine ? recording : state.recording,
    recordings:
      state.recordings &&
      (known ? state.recordings.map((candidate) => (candidate.id === recording.id ? recording : candidate)) : [recording, ...state.recordings]),
  };
}

/** What the engine says the session records right now (null: nothing). */
function withSessionRecording(state: ExplorerState, running: RecordingInfo | null): ExplorerState {
  if (running) {
    return withRecording(state, running);
  }
  // A recording believed to be running has ended while nobody was told how:
  // the list of recordings has its final state.
  return state.recording?.state === 'running' ? { ...state, recording: null } : state;
}

function withLink(state: ExplorerState, status: LinkStatus): ExplorerState {
  if (status !== 'up') {
    return { ...state, link: status, restored: null };
  }
  if (state.link !== 'down') {
    return { ...state, link: status };
  }
  return {
    ...state,
    link: status,
    restored: 'replayed',
    // Fetch what the session looks like now when the stream cannot tell:
    // nothing was received before the loss, so there is nothing to resume
    // from, or an earlier fetch failed.
    wantState: state.session !== null && (!state.seenEvents || state.stateMissed),
    stateMissed: false,
  };
}

function withEvent(current: ExplorerState, event: ExplorerEvent): ExplorerState {
  const seen = current.seenEvents ? current : { ...current, seenEvents: true };
  // Recording news is taken from any event that carries it (samples do, with
  // the current counters), also for a session this window has left.
  const state = event.recording ? withRecording(seen, event.recording) : seen;
  if (event.kind === 'resync') {
    return state.session ? { ...state, wantState: true, restored: 'gap' } : state;
  }
  if (event.session_id !== undefined && event.session_id !== state.session?.id) {
    return state;
  }
  switch (event.kind) {
    case 'sample':
      return event.sample ? withSampleEvent(state, event.sample) : state;
    case 'session_status':
      if (!event.session) {
        return state;
      }
      if (event.session.status === 'closed') {
        return { ...withoutSession(state), notice: event.message ?? 'The engine closed the session.' };
      }
      return { ...state, session: event.session };
    case 'discovery_changed':
      return event.session ? withDiscovery(state, event.session) : state;
    case 'error':
      return { ...state, notice: event.message ?? 'The engine reported an error.' };
    case 'recording_status':
      return state;
  }
}

export function reduce(state: ExplorerState, action: Action): ExplorerState {
  switch (action.type) {
    case 'health':
      return { ...state, health: action.health };
    case 'notice':
      return { ...state, notice: action.message, reading: false };
    case 'connecting':
      return { ...state, connecting: true, connectError: null, notice: null };
    case 'connectFailed':
      return { ...state, connecting: false, connectError: action.error };
    case 'discovered':
      return withDiscovery(state, action.session);
    case 'disconnected':
      return withoutSession(state);
    case 'reading':
      return { ...state, reading: action.reading };
    case 'snapshot':
      return withSnapshot(state, action.snapshot);
    case 'fullRequested':
      return { ...state, wantFull: [], fullInFlight: [...state.fullInFlight, ...state.wantFull] };
    case 'fullFailed':
      return { ...state, fullInFlight: without(state.fullInFlight, action.indexes) };
    case 'state':
      return withSessionState(state, action.state);
    case 'stateFailed':
      return { ...state, wantState: false, stateMissed: true };
    case 'attached':
      return withAttached(state, action.state);
    case 'link':
      return withLink(state, action.status);
    case 'dismissRestored':
      return { ...state, restored: null };
    case 'recording':
      return withRecording(state, action.recording);
    case 'recordings':
      return { ...state, recordings: action.recordings };
    case 'recordingDeleted':
      return {
        ...state,
        recording: state.recording?.id === action.id ? null : state.recording,
        recordings: state.recordings && state.recordings.filter((recording) => recording.id !== action.id),
      };
    case 'dismissRecording':
      return { ...state, recording: null };
    case 'event':
      return withEvent(state, action.event);
    case 'select':
      return { ...state, selected: action.selected };
    case 'toggleScope':
      return {
        ...state,
        scope: state.scope.includes(action.index)
          ? state.scope.filter((index) => index !== action.index)
          : [...state.scope, action.index].sort((a, b) => a - b),
      };
  }
}
