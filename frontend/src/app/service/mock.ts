// An in-memory fake device for development (`?mock`) and tests. It is the only
// place in the frontend that may use a timer for polling: in the real adapters
// polling runs in the Go engine.

import type { Platform } from '../platform/platform';
import { ServiceError, type Service } from './service';
import {
  API_VERSION,
  SNAPSHOT_SCHEMA,
  type ExplorerEvent,
  type ExportFile,
  type ExportFormat,
  type GroupView,
  type Health,
  type MetadataLevel,
  type ModelInfo,
  type ModelResult,
  type PointView,
  type PollConfig,
  type PollState,
  type ProbeAttempt,
  type RecordingConfig,
  type RecordingFormat,
  type RecordingInfo,
  type RecordingStopReason,
  type Sample,
  type SerialPort,
  type SessionInfo,
  type SessionState,
  type Snapshot,
  type SnapshotRequest,
  type TargetConfig,
} from './types';

const NOT_IMPLEMENTED = Symbol('not implemented');
type MockValue = number | string | typeof NOT_IMPLEMENTED;

interface PointDef {
  name: string;
  type: string;
  label: string;
  description?: string;
  units?: string;
  size?: number;
  sf?: string;
  static?: boolean;
  mandatory?: boolean;
  symbols?: (raw: number) => string[];
  value: (tick: number, instance: number) => MockValue;
}

interface GroupDef {
  name: string;
  label: string;
  points: PointDef[];
  /** Repeating child groups: `count` instances of each definition. */
  groups?: { def: GroupDef; count: number }[];
}

interface ModelDef {
  id: number;
  name: string;
  /** Absent for a vendor model the registry does not know. */
  group?: GroupDef;
  rawRegisters?: number[];
}

/** The value a device reports for a point it does not implement. */
const SENTINEL: Record<string, number | string> = {
  int16: -32768,
  uint16: 65535,
  enum16: 65535,
  sunssf: -32768,
  uint32: 4294967295,
  acc32: 0,
  bitfield32: 4294967295,
  string: '',
};

const fixed = (value: MockValue) => () => value;
const wave = (base: number, amplitude: number) => (tick: number, instance: number) =>
  Math.round(base + amplitude * Math.sin((tick + instance * 2) / 3));
const sf = (name: string, exponent: number): PointDef => ({
  name,
  type: 'sunssf',
  label: `${name.replace(/_SF$/, '')} scale factor`,
  value: fixed(exponent),
});
const enumOf = (names: Record<number, string>) => (raw: number) => {
  const name = names[raw];
  return name ? [name] : [];
};
const flagsOf = (names: string[]) => (raw: number) =>
  names.filter((_, bit) => (raw & (1 << bit)) !== 0);

const INVERTER_STATES = { 1: 'OFF', 2: 'SLEEPING', 3: 'STARTING', 4: 'MPPT', 5: 'THROTTLED', 7: 'FAULT' };
const INVERTER_EVENTS = [
  'GROUND_FAULT',
  'DC_OVER_VOLT',
  'AC_DISCONNECT',
  'DC_DISCONNECT',
  'GRID_DISCONNECT',
  'CABINET_OPEN',
  'MANUAL_SHUTDOWN',
  'OVER_TEMP',
];

const COMMON: GroupDef = {
  name: 'common',
  label: 'Common',
  points: [
    { name: 'Mn', type: 'string', size: 16, label: 'Manufacturer', description: 'Well known value registered with SunSpec for compliance', static: true, mandatory: true, value: fixed('OT Fabric') },
    { name: 'Md', type: 'string', size: 16, label: 'Model', description: 'Manufacturer specific value (32 chars)', static: true, mandatory: true, value: fixed('Mock Inverter 10k') },
    { name: 'Opt', type: 'string', size: 8, label: 'Options', description: 'Manufacturer specific value (16 chars)', static: true, value: fixed(NOT_IMPLEMENTED) },
    { name: 'Vr', type: 'string', size: 8, label: 'Version', description: 'Manufacturer specific value (16 chars)', static: true, value: fixed('1.4.2') },
    { name: 'SN', type: 'string', size: 16, label: 'Serial Number', description: 'Manufacturer specific value (32 chars)', static: true, mandatory: true, value: fixed('MOCK-000042') },
    { name: 'DA', type: 'uint16', label: 'Device Address', description: 'Modbus device address', static: true, value: fixed(1) },
    { name: 'Pad', type: 'pad', label: 'Pad', description: 'Force even alignment', static: true, value: fixed(NOT_IMPLEMENTED) },
  ],
};

const INVERTER: GroupDef = {
  name: 'inverter',
  label: 'Inverter (Three Phase)',
  points: [
    { name: 'A', type: 'uint16', units: 'A', sf: 'A_SF', label: 'Amps', description: 'AC Current', mandatory: true, value: wave(1830, 40) },
    { name: 'AphA', type: 'uint16', units: 'A', sf: 'A_SF', label: 'Amps PhaseA', description: 'Phase A Current', mandatory: true, value: wave(610, 15) },
    { name: 'AphB', type: 'uint16', units: 'A', sf: 'A_SF', label: 'Amps PhaseB', description: 'Phase B Current', mandatory: true, value: wave(611, 14) },
    { name: 'AphC', type: 'uint16', units: 'A', sf: 'A_SF', label: 'Amps PhaseC', description: 'Phase C Current', mandatory: true, value: wave(609, 16) },
    sf('A_SF', -2),
    { name: 'PPVphAB', type: 'uint16', units: 'V', sf: 'V_SF', label: 'Phase Voltage AB', description: 'Phase Voltage AB', value: wave(3990, 8) },
    { name: 'PPVphBC', type: 'uint16', units: 'V', sf: 'V_SF', label: 'Phase Voltage BC', description: 'Phase Voltage BC', value: wave(3992, 7) },
    { name: 'PPVphCA', type: 'uint16', units: 'V', sf: 'V_SF', label: 'Phase Voltage CA', description: 'Phase Voltage CA', value: wave(3989, 9) },
    { name: 'PhVphA', type: 'uint16', units: 'V', sf: 'V_SF', label: 'Phase Voltage AN', description: 'Phase Voltage AN', mandatory: true, value: wave(2304, 5) },
    { name: 'PhVphB', type: 'uint16', units: 'V', sf: 'V_SF', label: 'Phase Voltage BN', description: 'Phase Voltage BN', mandatory: true, value: wave(2306, 4) },
    { name: 'PhVphC', type: 'uint16', units: 'V', sf: 'V_SF', label: 'Phase Voltage CN', description: 'Phase Voltage CN', mandatory: true, value: wave(2301, 6) },
    sf('V_SF', -1),
    { name: 'W', type: 'int16', units: 'W', sf: 'W_SF', label: 'Watts', description: 'AC Power', mandatory: true, value: wave(420, 12) },
    sf('W_SF', 1),
    { name: 'Hz', type: 'uint16', units: 'Hz', sf: 'Hz_SF', label: 'Hz', description: 'Line Frequency', mandatory: true, value: wave(5000, 2) },
    sf('Hz_SF', -2),
    { name: 'VA', type: 'int16', units: 'VA', sf: 'VA_SF', label: 'VA', description: 'AC Apparent Power', value: wave(422, 12) },
    sf('VA_SF', 1),
    { name: 'VAr', type: 'int16', units: 'var', sf: 'VAr_SF', label: 'VAr', description: 'AC Reactive Power', value: wave(-12, 4) },
    sf('VAr_SF', 1),
    { name: 'PF', type: 'int16', units: 'Pct', sf: 'PF_SF', label: 'PF', description: 'AC Power Factor', value: fixed(995) },
    sf('PF_SF', -1),
    { name: 'WH', type: 'acc32', size: 2, units: 'Wh', sf: 'WH_SF', label: 'WattHours', description: 'AC Energy', mandatory: true, value: (tick) => 18250340 + tick * 6 },
    sf('WH_SF', 0),
    { name: 'DCA', type: 'uint16', units: 'A', sf: 'DCA_SF', label: 'DC Amps', description: 'DC Current', value: wave(712, 20) },
    sf('DCA_SF', -2),
    { name: 'DCV', type: 'uint16', units: 'V', sf: 'DCV_SF', label: 'DC Voltage', description: 'DC Voltage', value: wave(6020, 30) },
    sf('DCV_SF', -1),
    { name: 'DCW', type: 'int16', units: 'W', sf: 'DCW_SF', label: 'DC Watts', description: 'DC Power', value: wave(428, 12) },
    sf('DCW_SF', 1),
    { name: 'TmpCab', type: 'int16', units: 'C', sf: 'Tmp_SF', label: 'Cabinet Temperature', description: 'Cabinet Temperature', mandatory: true, value: wave(412, 6) },
    { name: 'TmpSnk', type: 'int16', units: 'C', sf: 'Tmp_SF', label: 'Heat Sink Temperature', description: 'Heat Sink Temperature', value: fixed(NOT_IMPLEMENTED) },
    { name: 'TmpTrns', type: 'int16', units: 'C', sf: 'Tmp_SF', label: 'Transformer Temperature', description: 'Transformer Temperature', value: fixed(NOT_IMPLEMENTED) },
    { name: 'TmpOt', type: 'int16', units: 'C', sf: 'Tmp_SF', label: 'Other Temperature', description: 'Other Temperature', value: fixed(NOT_IMPLEMENTED) },
    sf('Tmp_SF', -1),
    { name: 'St', type: 'enum16', label: 'Operating State', description: 'Enumerated value. Operating state', mandatory: true, symbols: enumOf(INVERTER_STATES), value: (tick) => (tick % 12 === 11 ? 5 : 4) },
    { name: 'StVnd', type: 'enum16', label: 'Vendor Operating State', description: 'Vendor specific operating state code', value: fixed(17) },
    { name: 'Evt1', type: 'bitfield32', size: 2, label: 'Event1', description: 'Bitmask value. Event fields', mandatory: true, symbols: flagsOf(INVERTER_EVENTS), value: (tick) => (tick % 4 === 3 ? 0b10100000 : 0b00100000) },
    { name: 'Evt2', type: 'bitfield32', size: 2, label: 'Event Bitfield 2', description: 'Reserved for future use', mandatory: true, value: fixed(0) },
    { name: 'EvtVnd1', type: 'bitfield32', size: 2, label: 'Vendor Event Bitfield 1', description: 'Vendor defined events', value: fixed(0) },
    { name: 'EvtVnd2', type: 'bitfield32', size: 2, label: 'Vendor Event Bitfield 2', description: 'Vendor defined events', value: fixed(NOT_IMPLEMENTED) },
    { name: 'EvtVnd3', type: 'bitfield32', size: 2, label: 'Vendor Event Bitfield 3', description: 'Vendor defined events', value: fixed(NOT_IMPLEMENTED) },
    { name: 'EvtVnd4', type: 'bitfield32', size: 2, label: 'Vendor Event Bitfield 4', description: 'Vendor defined events', value: fixed(NOT_IMPLEMENTED) },
  ],
};

const MPPT_MODULE: GroupDef = {
  name: 'module',
  label: 'Module',
  points: [
    { name: 'ID', type: 'uint16', label: 'Input ID', description: 'Internal input ID', static: true, value: (_tick, instance) => instance },
    { name: 'IDStr', type: 'string', size: 8, label: 'Input ID String', description: 'Input ID string', static: true, value: (_tick, instance) => `String ${instance}` },
    { name: 'DCA', type: 'uint16', units: 'A', sf: 'DCA_SF', label: 'DC Current', description: 'DC current of the input', mandatory: true, value: wave(356, 10) },
    { name: 'DCV', type: 'uint16', units: 'V', sf: 'DCV_SF', label: 'DC Voltage', description: 'DC voltage of the input', mandatory: true, value: wave(6020, 30) },
    { name: 'DCW', type: 'uint16', units: 'W', sf: 'DCW_SF', label: 'DC Power', description: 'DC power of the input', mandatory: true, value: wave(214, 6) },
    { name: 'DCWH', type: 'acc32', size: 2, units: 'Wh', sf: 'DCWH_SF', label: 'Lifetime Energy', description: 'Lifetime energy of the input', value: (tick, instance) => 9100000 + instance * 1500 + tick * 3 },
    { name: 'Tms', type: 'uint32', size: 2, units: 'Secs', label: 'Timestamp', description: 'Timestamp of the input reading', value: (tick) => 847000000 + tick },
    { name: 'Tmp', type: 'int16', units: 'C', label: 'Temperature', description: 'Temperature of the input', value: fixed(NOT_IMPLEMENTED) },
    { name: 'DCSt', type: 'enum16', label: 'Operating State', description: 'Operating state of the input', symbols: enumOf(INVERTER_STATES), value: fixed(4) },
    { name: 'DCEvt', type: 'bitfield32', size: 2, label: 'Module Events', description: 'Module event flags', symbols: flagsOf(INVERTER_EVENTS), value: fixed(0) },
  ],
};

const MPPT: GroupDef = {
  name: 'mppt',
  label: 'Multiple MPPT Inverter Extension',
  points: [
    sf('DCA_SF', -2),
    sf('DCV_SF', -1),
    sf('DCW_SF', 1),
    sf('DCWH_SF', 0),
    { name: 'Evt', type: 'bitfield32', size: 2, label: 'Global Events', description: 'Global event flags', symbols: flagsOf(INVERTER_EVENTS), value: fixed(0) },
    { name: 'N', type: 'uint16', label: 'Number of Modules', description: 'Number of MPPT modules', static: true, value: fixed(2) },
    { name: 'TmsPer', type: 'uint16', units: 'Secs', label: 'Timestamp Period', description: 'Timestamp period', value: fixed(NOT_IMPLEMENTED) },
  ],
  groups: [{ def: MPPT_MODULE, count: 2 }],
};

const VENDOR_REGISTERS = [0x4f54, 0x4642, 0x0001, 0x0102, 0xffff, 0x8000, 0x1a2b, 0x0000, 0x00ff, 0x7fff, 0x0042, 0xbeef];

const DEVICE: ModelDef[] = [
  { id: 1, name: 'Common', group: COMMON },
  { id: 103, name: 'Inverter (Three Phase)', group: INVERTER },
  { id: 160, name: 'Multiple MPPT Inverter Extension', group: MPPT },
  { id: 64900, name: 'Vendor model 64900', rawRegisters: VENDOR_REGISTERS },
];

const BASE_ADDRESS = 40000;
const NO_SCHEMA_WARNING = 'model 64900: no schema in the registry, registers are not decoded';
const READ_FAILURE = 'modbus: exception 4 (server device failure)';

function groupLength(def: GroupDef): number {
  const own = def.points.reduce((sum, point) => sum + (point.size ?? 1), 0);
  const nested = (def.groups ?? []).reduce((sum, child) => sum + child.count * groupLength(child.def), 0);
  return own + nested;
}

function collectScaleFactors(def: GroupDef, tick: number, into: Map<string, number>): void {
  for (const point of def.points) {
    if (point.type === 'sunssf') {
      into.set(point.name, point.value(tick, 0) as number);
    }
  }
  for (const child of def.groups ?? []) {
    collectScaleFactors(child.def, tick, into);
  }
}

interface BuildContext {
  tick: number;
  metadata: MetadataLevel;
  start: number;
  /** Register offset of the next point, counted from the model's ID register. */
  offset: number;
  scaleFactors: Map<string, number>;
}

function buildPoint(def: PointDef, instance: number, ctx: BuildContext): PointView {
  const value = def.value(ctx.tick, instance);
  const implemented = value !== NOT_IMPLEMENTED;
  const raw = implemented ? value : (SENTINEL[def.type] ?? 65535);
  const size = def.size ?? 1;
  const point: PointView = {
    name: def.name,
    type: def.type,
    implemented,
    raw,
    scaled: null,
    address: ctx.start + ctx.offset,
    offset: ctx.offset,
    size,
  };
  ctx.offset += size;
  if (def.units) {
    point.units = def.units;
  }
  if (def.sf) {
    const exponent = ctx.scaleFactors.get(def.sf);
    point.sf_name = def.sf;
    if (exponent !== undefined) {
      point.sf_raw = exponent;
      if (implemented && typeof raw === 'number') {
        point.scaled = Number((raw * 10 ** exponent).toFixed(Math.max(0, -exponent)));
      }
    }
  }
  if (implemented && def.symbols && typeof raw === 'number') {
    point.symbols = def.symbols(raw);
  }
  if (ctx.metadata === 'full') {
    point.label = def.label;
    point.description = def.description;
    point.access = 'R';
    point.mandatory = def.mandatory ?? false;
    point.static = def.static ?? false;
  }
  return point;
}

function buildGroup(def: GroupDef, index: number, ctx: BuildContext): GroupView {
  const group: GroupView = {
    name: def.name,
    index,
    points: def.points.map((point) => buildPoint(point, index, ctx)),
    groups: [],
  };
  if (ctx.metadata === 'full') {
    group.label = def.label;
  }
  for (const child of def.groups ?? []) {
    for (let i = 1; i <= child.count; i++) {
      group.groups.push(buildGroup(child.def, i, ctx));
    }
  }
  return group;
}

function isStaticOnly(def: GroupDef): boolean {
  return (
    def.points.every((point) => point.static === true) &&
    (def.groups ?? []).every((child) => isStaticOnly(child.def))
  );
}

function describeDevice(): ModelInfo[] {
  let address = BASE_ADDRESS + 2; // after the "SunS" marker
  return DEVICE.map((def, index) => {
    const length = def.group ? groupLength(def.group) : (def.rawRegisters?.length ?? 0);
    const info: ModelInfo = {
      index,
      id: def.id,
      name: def.name,
      start_address: address,
      length,
      schema_known: def.group !== undefined,
      decoding_supported: def.group !== undefined,
      static_only: def.group !== undefined && isStaticOnly(def.group),
    };
    address += 2 + length;
    return info;
  });
}

interface MockRecording {
  info: RecordingInfo;
  lines: string[];
}

const RECORDING_NAME = /^[a-zA-Z0-9._-]{1,64}$/;
const RECORDING_LOCATION = '/tmp/sunspec-explorer-mock';

interface MockSession {
  info: SessionInfo;
  /** The running recording of the session. */
  recording: MockRecording | null;
  polling: PollState;
  timer: ReturnType<typeof setInterval> | null;
  lastSample: Sample | null;
  lastOkAt: Map<number, number>;
}

const idlePolling = (): PollState => ({
  active: false,
  interval_s: 5,
  model_indexes: [],
  cycles: 0,
  failures: 0,
  overruns: 0,
});

/** The host of a network target, the device of a serial one; null for a URL the engine would refuse. */
function hostOf(url: string): string | null {
  return /^(?:tcp|rtuovertcp|tcp\+tls):\/\/([^:/]+)(?::\d+)?$/.exec(url)?.[1] ?? /^rtu:\/\/(.+)$/.exec(url)?.[1] ?? null;
}

const CSV_HEADER = 'captured_at,seq,model_index,model_id,group_path,point,value,scaled,unit,implemented,status';
const csvCell = (value: unknown) => (typeof value === 'string' && /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : String(value ?? ''));

/** Long-format CSV rows of one read: one row per point. */
function csvRows(capturedAt: string, seq: number, models: ModelResult[]): string[] {
  const rows: string[] = [];
  const walk = (model: ModelResult, group: GroupView, parent: string) => {
    const path = `${parent}${group.name}${group.index > 0 ? `[${group.index}]` : ''}`;
    for (const point of group.points) {
      const cells = [capturedAt, seq, model.index, model.id, path, point.name, point.implemented ? point.raw : '', point.scaled, point.units, point.implemented, model.status];
      rows.push(cells.map(csvCell).join(','));
    }
    group.groups.forEach((child) => walk(model, child, `${path}/`));
  };
  models.forEach((model) => model.group && walk(model, model.group, ''));
  return rows;
}

/** The points of a group as a device file lists them: by name, null when not implemented. */
function deviceFileGroup(group: GroupView): { points: Record<string, unknown>; groups?: Record<string, unknown[]> } {
  const points = Object.fromEntries(group.points.map((point) => [point.name, point.implemented ? (point.scaled ?? point.raw) : null]));
  if (group.groups.length === 0) {
    return { points };
  }
  const groups: Record<string, unknown[]> = {};
  for (const child of group.groups) {
    (groups[child.name] ??= []).push(deviceFileGroup(child));
  }
  return { points, groups };
}

export class MockService implements Service {
  private readonly platform: Pick<Platform, 'saveBlob'> | undefined;
  private readonly started = Date.now();
  private readonly recordings_: MockRecording[] = [];
  private readonly sessions_ = new Map<string, MockSession>();
  private readonly listeners = new Set<(event: ExplorerEvent) => void>();
  private readonly history: ExplorerEvent[] = [];
  private readonly failing = new Set<number>();
  private nextSession = 1;
  private eventSeq = 0;
  private tick = 0;

  /** Without a platform, saving a copy of a recording does nothing and reports success. */
  constructor(platform?: Pick<Platform, 'saveBlob'>) {
    this.platform = platform;
  }

  /** Makes every read of the model at this chain position fail, or recover. */
  setModelFailing(index: number, failing: boolean): void {
    if (failing) {
      this.failing.add(index);
    } else {
      this.failing.delete(index);
    }
  }

  async health(): Promise<Health> {
    return {
      status: 'ok',
      version: '0.1.0-mock',
      api_version: API_VERSION,
      uptime_s: Math.floor((Date.now() - this.started) / 1000),
      go_sunspec: 'mock',
      registry_models: DEVICE.length - 1,
      time: new Date().toISOString(),
      features: ['recording', 'serial', 'tls'],
    };
  }

  async openSession(cfg: TargetConfig): Promise<SessionInfo> {
    const host = hostOf(cfg.url);
    if (host === null) {
      throw new ServiceError('invalid_argument', `invalid target URL "${cfg.url}": want tcp://host:port, rtuovertcp://host:port, tcp+tls://host:port or rtu://<device>`);
    }
    const regType = cfg.reg_type ?? 'holding';
    if (host === 'fail.test') {
      const bases = cfg.base_addresses?.length ? cfg.base_addresses : [0, 40000, 50000];
      const attempts: ProbeAttempt[] = bases.map((base_address) => ({
        base_address,
        reg_type: regType,
        matched: false,
        error: 'modbus: exception 2 (illegal data address)',
      }));
      throw new ServiceError('not_sunspec', `no SunSpec marker found on ${cfg.url} (unit ${cfg.unit_id})`, attempts);
    }
    const now = new Date().toISOString();
    const info: SessionInfo = {
      id: `mock-${this.nextSession++}`,
      target: { url: cfg.url, unit_id: cfg.unit_id || 1, reg_type: regType, base_address: BASE_ADDRESS },
      status: 'connected',
      opened_at: now,
      discovered_at: now,
      device: { manufacturer: 'OT Fabric', model: 'Mock Inverter 10k', version: '1.4.2', serial: 'MOCK-000042' },
      models: describeDevice(),
      warnings: [NO_SCHEMA_WARNING],
      attempts: [
        { base_address: 0, reg_type: regType, matched: false, error: 'modbus: exception 2 (illegal data address)' },
        { base_address: BASE_ADDRESS, reg_type: regType, matched: true },
      ],
    };
    this.sessions_.set(info.id, { info, recording: null, polling: idlePolling(), timer: null, lastSample: null, lastOkAt: new Map() });
    return info;
  }

  async closeSession(id: string): Promise<void> {
    const session = this.session(id);
    this.stopTimer(session);
    this.endRecording(session, 'session_closed');
    this.sessions_.delete(id);
  }

  async sessions(): Promise<SessionInfo[]> {
    return [...this.sessions_.values()].map((session) => session.info);
  }

  async rediscover(id: string): Promise<SessionInfo> {
    const session = this.session(id);
    session.info = { ...session.info, discovered_at: new Date().toISOString(), models: describeDevice() };
    return session.info;
  }

  async snapshot(id: string, req: SnapshotRequest): Promise<Snapshot> {
    const session = this.session(id);
    const indexes = req.model_indexes?.length ? req.model_indexes : session.info.models.map((model) => model.index);
    const metadata = req.metadata ?? 'full';
    this.tick++;
    const models = this.readModels(session, indexes, metadata);
    return {
      schema: SNAPSHOT_SCHEMA,
      app: { name: 'sunspec-explorer', version: '0.1.0-mock' },
      libs: { go_sunspec: 'mock', registry_models: DEVICE.length - 1 },
      session_id: id,
      captured_at: new Date().toISOString(),
      read_duration_ms: models.reduce((sum, model) => sum + model.read_duration_ms, 0),
      metadata,
      target: session.info.target,
      device: session.info.device ?? null,
      models,
      warnings: session.info.warnings,
    };
  }

  async startPolling(id: string, cfg: PollConfig): Promise<void> {
    const session = this.session(id);
    const interval = cfg.interval_s || 5;
    if (!Number.isInteger(interval) || interval < 1 || interval > 3600) {
      throw new ServiceError('invalid_argument', 'poll interval must be 1..3600 whole seconds');
    }
    this.stopTimer(session);
    const indexes = cfg.model_indexes?.length
      ? cfg.model_indexes
      : session.info.models.filter((model) => !model.static_only).map((model) => model.index);
    session.polling = { ...idlePolling(), active: true, interval_s: interval, model_indexes: indexes, started_at: new Date().toISOString() };
    session.timer = setInterval(() => this.pollOnce(session), interval * 1000);
  }

  async stopPolling(id: string): Promise<void> {
    const session = this.session(id);
    this.stopTimer(session);
    session.polling = { ...session.polling, active: false };
  }

  async state(id: string): Promise<SessionState> {
    const session = this.session(id);
    return { session: session.info, polling: session.polling, last_sample: session.lastSample, recording: session.recording?.info ?? null };
  }

  subscribe(sinceSeq: number, onEvent: (event: ExplorerEvent) => void): () => void {
    if (sinceSeq > 0) {
      this.history.filter((event) => event.seq > sinceSeq).forEach(onEvent);
    }
    this.listeners.add(onEvent);
    return () => this.listeners.delete(onEvent);
  }

  async startRecording(id: string, cfg: RecordingConfig): Promise<RecordingInfo> {
    const session = this.session(id);
    const name = cfg.name || `recording-${this.recordings_.length + 1}`;
    if (!RECORDING_NAME.test(name)) {
      throw new ServiceError('invalid_argument', `invalid recording name "${name}": use 1 to 64 letters, digits, ".", "_" or "-"`);
    }
    if (this.recordings_.some((recording) => recording.info.name === name)) {
      throw new ServiceError('invalid_argument', `a recording named "${name}" already exists`);
    }
    if (session.recording) {
      throw new ServiceError('invalid_argument', 'the session is already recording');
    }
    const header = JSON.stringify({ type: 'header', schema: 'otfabric.sunspec-explorer.recording/1', started_at: new Date().toISOString(), target: session.info.target });
    const info: RecordingInfo = {
      id: `rec-${this.recordings_.length + 1}`,
      session_id: id,
      name,
      state: 'running',
      started_at: new Date().toISOString(),
      samples: 0,
      failures: 0,
      size_bytes: header.length + 1,
      files: 1,
      location: RECORDING_LOCATION,
      temporary: true,
    };
    session.recording = { info, lines: [header] };
    this.recordings_.unshift(session.recording);
    this.emit({ kind: 'recording_status', session_id: id, recording: info });
    return info;
  }

  async stopRecording(recId: string): Promise<RecordingInfo> {
    const recording = this.recording(recId);
    const session = this.sessions_.get(recording.info.session_id);
    if (session?.recording === recording) {
      this.endRecording(session, 'user');
    }
    return recording.info;
  }

  async recordings(): Promise<RecordingInfo[]> {
    return this.recordings_.map((recording) => recording.info);
  }

  async deleteRecording(recId: string): Promise<void> {
    const recording = this.recording(recId);
    if (recording.info.state === 'running') {
      throw new ServiceError('invalid_argument', 'a running recording cannot be deleted: stop it first');
    }
    this.recordings_.splice(this.recordings_.indexOf(recording), 1);
  }

  async saveRecording(recId: string, format: RecordingFormat = 'jsonl'): Promise<boolean> {
    const recording = this.recording(recId);
    const lines =
      format === 'jsonl'
        ? recording.lines
        : [CSV_HEADER, ...recording.lines.map((line) => JSON.parse(line) as Sample & { type: string }).flatMap((line) => (line.type === 'sample' ? csvRows(line.captured_at, line.seq, line.models) : []))];
    const blob = new Blob([lines.join('\n') + '\n'], { type: format === 'csv' ? 'text/csv' : 'application/x-ndjson' });
    return this.platform ? this.platform.saveBlob(`${recording.info.name}.${format}`, blob) : true;
  }

  async exportSession(id: string, format: ExportFormat): Promise<ExportFile> {
    const snapshot = await this.snapshot(id, { metadata: 'full' });
    const stamp = snapshot.captured_at.slice(0, 19).replace(/[-:]/g, '');
    if (format === 'csv') {
      const text = [CSV_HEADER, ...csvRows(snapshot.captured_at, 0, snapshot.models)].join('\n') + '\n';
      return { name: `sunspec-points-${stamp}Z.csv`, mime: 'text/csv', text };
    }
    const device = {
      unit_id: snapshot.target.unit_id,
      base_address: snapshot.target.base_address,
      models: snapshot.models.map((model) => (model.group ? { id: model.id, name: model.name, ...deviceFileGroup(model.group) } : { id: model.id, registers: model.raw_registers })),
    };
    return { name: `sunspec-device-${stamp}Z.json`, mime: 'application/json', text: JSON.stringify(device, null, 2) + '\n' };
  }

  async serialPorts(): Promise<SerialPort[]> {
    return [{ path: '/dev/ttyUSB0' }, { path: '/dev/ttyUSB1' }];
  }

  private session(id: string): MockSession {
    const session = this.sessions_.get(id);
    if (!session) {
      throw new ServiceError('not_found', `no session ${id}`);
    }
    return session;
  }

  private recording(recId: string): MockRecording {
    const recording = this.recordings_.find((candidate) => candidate.info.id === recId);
    if (!recording) {
      throw new ServiceError('not_found', `no recording ${recId}`);
    }
    return recording;
  }

  private append(recording: MockRecording, line: object, counters: Partial<RecordingInfo>): void {
    const text = JSON.stringify(line);
    recording.lines.push(text);
    recording.info = { ...recording.info, ...counters, size_bytes: recording.info.size_bytes + text.length + 1 };
  }

  private endRecording(session: MockSession, reason: RecordingStopReason): void {
    const recording = session.recording;
    if (!recording) {
      return;
    }
    const stoppedAt = new Date().toISOString();
    this.append(recording, { type: 'footer', stopped_at: stoppedAt, samples: recording.info.samples, reason }, { state: 'stopped', stopped_at: stoppedAt, stop_reason: reason });
    session.recording = null;
    this.emit({ kind: 'recording_status', session_id: session.info.id, recording: recording.info });
  }

  private stopTimer(session: MockSession): void {
    if (session.timer !== null) {
      clearInterval(session.timer);
      session.timer = null;
    }
  }

  private pollOnce(session: MockSession): void {
    this.tick++;
    const polling = session.polling;
    const models = this.readModels(session, polling.model_indexes, 'compact');
    const capturedAt = new Date().toISOString();
    const sample: Sample = {
      session_id: session.info.id,
      seq: polling.cycles + 1,
      captured_at: capturedAt,
      read_duration_ms: models.reduce((sum, model) => sum + model.read_duration_ms, 0),
      overruns: polling.overruns,
      metadata: 'compact',
      models,
    };
    session.polling = {
      ...polling,
      cycles: sample.seq,
      failures: polling.failures + (models.some((model) => model.status !== 'ok') ? 1 : 0),
      last_sample_at: capturedAt,
    };
    session.lastSample = sample;
    const recording = session.recording;
    if (recording) {
      const failed = models.some((model) => model.status !== 'ok');
      this.append(recording, { type: 'sample', ...sample }, { samples: recording.info.samples + 1, failures: recording.info.failures + (failed ? 1 : 0) });
    }
    this.emit({ kind: 'sample', session_id: session.info.id, sample, recording: recording?.info });
  }

  private readModels(session: MockSession, indexes: number[], metadata: MetadataLevel): ModelResult[] {
    const now = Date.now();
    const staleAfterMs = 2 * session.polling.interval_s * 1000;
    return indexes.flatMap((index) => {
      const info = session.info.models[index];
      const def = DEVICE[index];
      if (!info || !def) {
        return [];
      }
      const lastOk = session.lastOkAt.get(index);
      const result: ModelResult = {
        index,
        id: info.id,
        name: info.name,
        start_address: info.start_address,
        length: info.length,
        schema_known: info.schema_known,
        status: 'ok',
        error: null,
        read_duration_ms: 8 + info.length,
        warnings: [],
        stale: false,
        group: null,
        raw_registers: null,
      };
      if (this.failing.has(index)) {
        result.status = 'error';
        result.error = READ_FAILURE;
        result.stale = lastOk === undefined || now - lastOk > staleAfterMs;
        if (lastOk !== undefined) {
          result.last_ok_at = new Date(lastOk).toISOString();
        }
        return [result];
      }
      session.lastOkAt.set(index, now);
      result.last_ok_at = new Date(now).toISOString();
      if (def.group) {
        const scaleFactors = new Map<string, number>();
        collectScaleFactors(def.group, this.tick, scaleFactors);
        result.group = buildGroup(def.group, 0, { tick: this.tick, metadata, start: info.start_address, offset: 2, scaleFactors });
      } else {
        result.raw_registers = def.rawRegisters ?? [];
        result.warnings = [NO_SCHEMA_WARNING];
      }
      return [result];
    });
  }

  private emit(event: Omit<ExplorerEvent, 'seq' | 'at'>): void {
    const full: ExplorerEvent = { ...event, seq: ++this.eventSeq, at: new Date().toISOString() };
    this.history.push(full);
    if (this.history.length > 64) {
      this.history.shift();
    }
    this.listeners.forEach((listener) => listener(full));
  }
}
