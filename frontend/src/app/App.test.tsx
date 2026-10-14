import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { App } from './App';
import type { Platform, Theme, TLSSettings, TlsFileKind, TlsIdentity } from './platform/platform';
import { MockService } from './service/mock';
import type { LinkStatus } from './service/service';
import type { ExplorerEvent, Health } from './service/types';

const fakePlatform = (theme: Theme): Platform => ({
  mode: { kind: 'browser' },
  saveFile: vi.fn(async () => true),
  saveBlob: vi.fn(async () => true),
  openExternal: vi.fn(),
  persistTheme: vi.fn(),
  loadTheme: () => theme,
  copyText: vi.fn(async () => {}),
  reload: vi.fn(),
});

describe.each<Theme>(['dark', 'light'])('App against the mock device, %s theme', (theme) => {
  it('connects, lists the chain, labels unimplemented points and shows raw registers', async () => {
    const user = userEvent.setup();
    render(<App service={new MockService()} platform={fakePlatform(theme)} />);
    expect(document.documentElement.dataset.theme).toBe(theme);

    await user.click(screen.getByRole('button', { name: 'Connect' }));
    const models = within(await screen.findByRole('navigation', { name: 'Models' }));
    for (const id of [1, 103, 160, 64900]) {
      expect(models.getByText(String(id), { exact: true })).toBeInTheDocument();
    }
    expect(models.getByText('no schema')).toBeInTheDocument();
    expect(models.getByText('static')).toBeInTheDocument();

    await user.click(models.getByRole('button', { name: /103/ }));
    const heatSink = (await screen.findByText('TmpSnk')).closest('tr') as HTMLElement;
    expect(within(heatSink).getByText('not implemented')).toBeInTheDocument();
    expect(heatSink).not.toHaveTextContent('32768');
    expect(within(screen.getByText('St').closest('tr') as HTMLElement).getByText('MPPT')).toBeInTheDocument();

    await user.click(models.getByRole('button', { name: /160/ }));
    expect(await screen.findByText('module [2]')).toBeInTheDocument();

    await user.click(models.getByRole('button', { name: /64900/ }));
    const registers = within(await screen.findByRole('region', { name: 'Raw registers' }));
    expect(registers.getByText('undecoded — no schema')).toBeInTheDocument();
    expect(registers.getByText('0xBEEF')).toBeInTheDocument();
  });

  it('collapses the model list to a rail and expands it again, remembering the choice', async () => {
    localStorage.clear();
    const user = userEvent.setup();
    const { unmount } = render(<App service={new MockService()} platform={fakePlatform(theme)} />);
    await user.click(screen.getByRole('button', { name: 'Connect' }));

    // Expanded by default.
    const nav = await screen.findByRole('navigation', { name: 'Models' });
    expect(within(nav).getByRole('button', { name: 'Re-discover' })).toBeInTheDocument();

    await user.click(within(nav).getByRole('button', { name: 'Hide the model list' }));
    const rail = screen.getByRole('navigation', { name: 'Models' });
    expect(within(rail).queryByRole('button', { name: 'Re-discover' })).not.toBeInTheDocument();
    expect(within(rail).queryByText('no schema')).not.toBeInTheDocument();
    // The models stay selectable from the rail.
    await user.click(within(rail).getByRole('button', { name: /^Model 64900 / }));
    expect(await screen.findByText(/undecoded/i)).toBeInTheDocument();

    // The choice survives a reload.
    unmount();
    render(<App service={new MockService()} platform={fakePlatform(theme)} />);
    await user.click(screen.getByRole('button', { name: 'Connect' }));
    const again = await screen.findByRole('navigation', { name: 'Models' });
    await user.click(within(again).getByRole('button', { name: 'Show the model list' }));
    expect(within(screen.getByRole('navigation', { name: 'Models' })).getByRole('button', { name: 'Re-discover' })).toBeInTheDocument();
    localStorage.clear();
  });

  it('shows every probe attempt when the device does not answer as SunSpec', async () => {
    const user = userEvent.setup();
    render(<App service={new MockService()} platform={fakePlatform(theme)} />);
    const url = screen.getByLabelText('Device URL');
    await user.clear(url);
    await user.type(url, 'tcp://fail.test:502');
    await user.click(screen.getByRole('button', { name: 'Connect' }));

    const alert = within(await screen.findByRole('alert'));
    expect(alert.getByText(/wrong unit ID is the usual cause/)).toBeInTheDocument();
    expect(alert.getAllByRole('row')).toHaveLength(4);
  });

  it('filters points and exports after a warning about serial numbers', async () => {
    const user = userEvent.setup();
    const platform = fakePlatform(theme);
    render(<App service={new MockService()} platform={platform} />);
    await user.click(screen.getByRole('button', { name: 'Connect' }));

    await user.type(await screen.findByRole('searchbox'), 'heat sink');
    expect(await screen.findByText('1 point')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Export…' }));
    expect(platform.saveFile).not.toHaveBeenCalled();
    const dialog = within(screen.getByRole('dialog', { name: 'Export' }));
    expect(dialog.getByText(/serial number and network addresses/)).toBeInTheDocument();
    await user.click(dialog.getByRole('button', { name: 'Snapshot (JSON)' }));
    await vi.waitFor(() => expect(platform.saveFile).toHaveBeenCalledOnce());
    const [name, mime, text] = vi.mocked(platform.saveFile).mock.calls[0] as [string, string, string];
    expect(name).toMatch(/^sunspec-snapshot-\d{8}T\d{6}Z\.json$/);
    expect(mime).toBe('application/json');
    expect(JSON.parse(text)).toMatchObject({ schema: 'otfabric.sunspec-explorer.snapshot/1', metadata: 'full' });
    expect(JSON.parse(text).models[3].raw_registers).toHaveLength(12);
  });

  it('exports the points as CSV and the device as a sunspecctl device file', async () => {
    const user = userEvent.setup();
    const platform = fakePlatform(theme);
    render(<App service={new MockService()} platform={platform} />);
    await user.click(screen.getByRole('button', { name: 'Connect' }));

    await user.click(await screen.findByRole('button', { name: 'Export…' }));
    await user.click(screen.getByRole('button', { name: 'Points (CSV)' }));
    await vi.waitFor(() => expect(platform.saveFile).toHaveBeenCalledTimes(1));
    const [csvName, csvMime, csv] = vi.mocked(platform.saveFile).mock.calls[0] as [string, string, string];
    expect(csvName).toMatch(/\.csv$/);
    expect(csvMime).toBe('text/csv');
    expect(csv.split('\n')[0]).toBe('captured_at,seq,model_index,model_id,group_path,point,value,scaled,unit,implemented,status');
    expect(csv).toMatch(/,2,160,mppt\/module\[2\],DCV,\d+,[\d.]+,V,true,ok\n/);

    await user.click(screen.getByRole('button', { name: 'Export…' }));
    await user.click(screen.getByRole('button', { name: 'Device file (sunspecctl)' }));
    await vi.waitFor(() => expect(platform.saveFile).toHaveBeenCalledTimes(2));
    const device = JSON.parse((vi.mocked(platform.saveFile).mock.calls[1] as [string, string, string])[2]);
    expect(device).toMatchObject({ unit_id: 1, base_address: 40000 });
    expect(device.models.map((model: { id: number }) => model.id)).toEqual([1, 103, 160, 64900]);
    expect(device.models[3].registers).toHaveLength(12);
  });

  it('picks up a session that is already open and polling on the engine', async () => {
    const service = new MockService();
    const open = await service.openSession({ url: 'tcp://10.1.2.3:502', unit_id: 1 });
    await service.startPolling(open.id, { interval_s: 30 });
    render(<App service={service} platform={fakePlatform(theme)} />);

    expect(await screen.findByRole('button', { name: 'Disconnect' })).toBeInTheDocument();
    expect(screen.getByText(/connected · tcp:\/\/10\.1\.2\.3:502/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Stop polling' })).toBeInTheDocument();
    expect(await screen.findByText('TmpSnk')).toBeInTheDocument();
    await service.closeSession(open.id);
  });

  it('records: start, counters advance, stop, offer to save, list, delete', async () => {
    const user = userEvent.setup();
    const platform = fakePlatform(theme);
    render(<App service={new MockService(platform)} platform={platform} />);
    await user.click(screen.getByRole('button', { name: 'Connect' }));

    await user.click(await screen.findByRole('button', { name: 'Record' }));
    const naming = within(screen.getByRole('dialog', { name: 'Start recording' }));
    expect(naming.getByText(/waits until you start it/)).toBeInTheDocument();
    await user.type(naming.getByLabelText('Name (optional)'), 'bad name');
    expect(naming.getByRole('button', { name: 'Start recording' })).toBeDisabled();
    await user.clear(naming.getByLabelText('Name (optional)'));
    await user.type(naming.getByLabelText('Name (optional)'), 'bench-test');
    await user.click(naming.getByRole('button', { name: 'Start recording' }));

    const bar = within(await screen.findByRole('region', { name: 'Recording' }));
    expect(bar.getByText('Recording')).toBeInTheDocument();
    expect(bar.getByText('bench-test')).toBeInTheDocument();
    expect(bar.getByText('recording waits for polling')).toBeInTheDocument();
    expect(bar.getByText('/tmp/sunspec-explorer-mock')).toBeInTheDocument();
    expect(bar.getByText(/Temporary storage/)).toBeInTheDocument();
    expect(bar.getByText(/0 samples/)).toBeInTheDocument();

    const interval = screen.getByLabelText('Poll interval in whole seconds');
    await user.clear(interval);
    await user.type(interval, '1');
    await user.click(screen.getByRole('button', { name: 'Start polling' }));
    expect(await bar.findByText(/2 samples · 0 failures/, {}, { timeout: 4000 })).toBeInTheDocument();
    expect(bar.queryByText('recording waits for polling')).not.toBeInTheDocument();
    expect(screen.getByText(/recording bench-test to temporary storage/)).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Stop recording' }));
    expect(await screen.findByText('Recording bench-test stopped: stopped by you')).toBeInTheDocument();
    expect(screen.getByText(/will be lost when the system clears its temp/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Save a copy of bench-test as JSON Lines' }));
    await vi.waitFor(() => expect(platform.saveBlob).toHaveBeenCalledOnce());
    const [name, blob] = vi.mocked(platform.saveBlob).mock.calls[0] as [string, Blob];
    expect(name).toBe('bench-test.jsonl');
    expect((await blob.text()).trim().split('\n').map((line) => JSON.parse(line).type)).toEqual(expect.arrayContaining(['header', 'sample', 'footer']));

    await user.click(screen.getByRole('button', { name: 'Recordings' }));
    const list = within(screen.getByRole('dialog', { name: 'Recordings' }));
    const row = within(await list.findByRole('row', { name: /bench-test/ }));
    await user.click(row.getByRole('button', { name: 'Save a copy of bench-test as CSV' }));
    await vi.waitFor(() => expect(platform.saveBlob).toHaveBeenCalledTimes(2));
    const [csvName, csv] = vi.mocked(platform.saveBlob).mock.calls[1] as [string, Blob];
    expect(csvName).toBe('bench-test.csv');
    expect(csv.type).toBe('text/csv');
    expect((await csv.text()).split('\n')[0]).toMatch(/^captured_at,seq,model_index/);
    expect(row.getByText('stopped')).toBeInTheDocument();
    await user.click(row.getByRole('button', { name: 'Delete bench-test' }));
    await user.click(within(screen.getByRole('dialog', { name: 'Delete recording' })).getByRole('button', { name: 'Delete' }));
    expect(await list.findByText('There are no recordings.')).toBeInTheDocument();
    expect(screen.queryByText(/Recording bench-test stopped/)).not.toBeInTheDocument();
  }, 15_000);
});

/** A mock whose health answer and link the test controls. */
class EngineDouble extends MockService {
  answer: Partial<Health> = {};
  link: (status: LinkStatus) => void = () => {};

  override async health(): Promise<Health> {
    return { ...(await super.health()), ...this.answer };
  }

  override subscribe(sinceSeq: number, onEvent: (event: ExplorerEvent) => void, onLink?: (status: LinkStatus) => void): () => void {
    this.link = onLink ?? this.link;
    return super.subscribe(sinceSeq, onEvent);
  }
}

describe('engine checks', () => {
  it('blocks the page when the engine speaks another API major version', async () => {
    const user = userEvent.setup();
    const platform = fakePlatform('dark');
    const service = new EngineDouble();
    service.answer = { api_version: '2.3' };
    render(<App service={service} platform={platform} />);

    expect(await screen.findByRole('heading', { name: 'This page does not match the daemon' })).toBeInTheDocument();
    expect(screen.getByText(/speaks API 2\.3; this page expects 1\.x/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'Reload' }));
    expect(platform.reload).toHaveBeenCalledOnce();
  });

  it('offers a reload when the engine was updated while the page stayed open', async () => {
    const user = userEvent.setup();
    const platform = fakePlatform('dark');
    const service = new EngineDouble();
    service.answer = { version: '0.0.2' };
    render(<App service={service} platform={platform} />);
    expect(await screen.findByText('engine 0.0.2')).toBeInTheDocument();
    expect(screen.queryByText(/was updated/)).not.toBeInTheDocument();

    service.answer = { version: '0.0.3' };
    act(() => service.link('down'));
    expect(screen.getByText(/Connection to the daemon lost/)).toBeInTheDocument();
    act(() => service.link('up'));

    expect(await screen.findByText(/The daemon was updated to 0\.0\.3\. Reload to get the matching page\./)).toBeInTheDocument();
    expect(screen.getByText(/open the URL with the token again/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeEnabled();
    await user.click(screen.getByRole('button', { name: 'Reload' }));
    expect(platform.reload).toHaveBeenCalledOnce();
  });

  it('warns about a clock that is off by more than five seconds, until dismissed', async () => {
    const user = userEvent.setup();
    const service = new EngineDouble();
    service.answer = { time: new Date(Date.now() - 42_000).toISOString() };
    render(<App service={service} platform={fakePlatform('dark')} />);

    const warning = await screen.findByText(/The daemon's clock is 42 s behind this computer\. Sample times are the daemon's\./);
    await user.click(within(warning.parentElement as HTMLElement).getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByText(/clock is/)).not.toBeInTheDocument();
  });

  it('says nothing about a clock that is close enough', async () => {
    const service = new EngineDouble();
    service.answer = { time: new Date(Date.now() + 3_000).toISOString() };
    render(<App service={service} platform={fakePlatform('dark')} />);
    expect(await screen.findByText(/^engine /)).toBeInTheDocument();
    expect(screen.queryByText(/clock is/)).not.toBeInTheDocument();
  });
});

describe('target kinds', () => {
  async function typeUrl(user: ReturnType<typeof userEvent.setup>, url: string) {
    const field = screen.getByLabelText('Device URL');
    await user.clear(field);
    await user.type(field, url);
  }

  it('offers the serial settings and the allowed ports for an rtu:// target', async () => {
    const user = userEvent.setup();
    const service = new MockService();
    const open = vi.spyOn(service, 'openSession');
    render(<App service={service} platform={fakePlatform('dark')} />);
    await screen.findByText(/^engine /);
    expect(screen.queryByLabelText('Baud')).not.toBeInTheDocument();

    await typeUrl(user, 'rtu://');
    expect(screen.getByText(/Serial paths are on the machine the engine runs on \(the gateway/)).toBeInTheDocument();
    await user.selectOptions(await screen.findByLabelText('Serial port'), '/dev/ttyUSB1');
    expect(screen.getByLabelText('Device URL')).toHaveValue('rtu:///dev/ttyUSB1');
    await user.clear(screen.getByLabelText('Baud'));
    await user.type(screen.getByLabelText('Baud'), '19200');
    await user.selectOptions(screen.getByLabelText('Parity'), 'even');
    await user.click(screen.getByRole('button', { name: 'Connect' }));

    expect(open).toHaveBeenCalledWith(
      expect.objectContaining({ url: 'rtu:///dev/ttyUSB1', serial: { baud: 19200, data_bits: 8, parity: 'even', stop_bits: 1 } }),
    );
    expect(await screen.findByRole('button', { name: 'Disconnect' })).toBeInTheDocument();
  });

  it('sends no serial settings for a network target', async () => {
    const user = userEvent.setup();
    const service = new MockService();
    const open = vi.spyOn(service, 'openSession');
    render(<App service={service} platform={fakePlatform('dark')} />);
    await typeUrl(user, 'tcp+tls://10.0.0.5:802');
    await user.click(screen.getByRole('button', { name: 'Connect' }));
    expect(open.mock.calls[0]?.[0]).toMatchObject({ url: 'tcp+tls://10.0.0.5:802', serial: undefined });
  });

  it('explains why a serial or TLS target cannot work instead of letting the connect fail', async () => {
    const user = userEvent.setup();
    const service = new EngineDouble();
    service.answer = { features: ['recording'] };
    render(<App service={service} platform={fakePlatform('dark')} />);
    await screen.findByText(/^engine /);

    await typeUrl(user, 'rtu:///dev/ttyUSB0');
    expect(screen.getByRole('alert')).toHaveTextContent('the daemon was not started with --allow-serial');
    expect(screen.getByRole('button', { name: 'Connect' })).toBeDisabled();
    expect(screen.queryByLabelText('Serial port')).not.toBeInTheDocument();

    await typeUrl(user, 'tcp+tls://10.0.0.5:802');
    expect(screen.getByRole('alert')).toHaveTextContent('--tls-cert, --tls-key and --tls-ca');
    expect(screen.getByRole('button', { name: 'Connect' })).toBeDisabled();

    await typeUrl(user, 'tcp://10.0.0.5:502');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeEnabled();
  });
});

describe('poll scope', () => {
  it('polls a static-only model too once its box is ticked', async () => {
    const user = userEvent.setup();
    const service = new MockService();
    const start = vi.spyOn(service, 'startPolling');
    render(<App service={service} platform={fakePlatform('dark')} />);
    await user.click(screen.getByRole('button', { name: 'Connect' }));

    const common = await screen.findByRole('checkbox', { name: /Include model 1 at position 0/ });
    expect(common).not.toBeChecked();
    expect(screen.getByText('static')).toHaveAttribute('title', expect.stringContaining('Tick the box to poll it anyway'));
    await user.click(common);
    await user.click(screen.getByRole('button', { name: 'Start polling' }));

    await vi.waitFor(() => expect(start).toHaveBeenCalledOnce());
    expect(start.mock.calls[0]?.[1]).toEqual({ interval_s: 5, model_indexes: [0, 1, 2, 3] });
    await user.click(await screen.findByRole('button', { name: 'Stop polling' }));
  });
});

describe('serial port picker', () => {
  it.each([
    ['COM3', 'rtu://COM3'],
    ['/dev/ttyUSB0', 'rtu:///dev/ttyUSB0'],
  ])('turns the port %s into the URL %s', async (path, url) => {
    const user = userEvent.setup();
    const service = new MockService();
    vi.spyOn(service, 'serialPorts').mockResolvedValue([{ path: 'COM9' }, { path }]);
    render(<App service={service} platform={fakePlatform('dark')} />);
    await screen.findByText(/^engine /);

    const field = screen.getByLabelText('Device URL');
    await user.clear(field);
    await user.type(field, 'rtu://');
    await user.selectOptions(await screen.findByLabelText('Serial port'), path);
    expect(field).toHaveValue(url);
  });
});

/** A TLS identity like the desktop application's: files are "chosen" by name, the engine gains 'tls' when it is active. */
class FakeTls implements TlsIdentity {
  current: TLSSettings = { cert_file: '', key_file: '', ca_file: '', active: false, error: '', from_flags: false };
  /** What the next native dialog "returns"; '' is a cancelled dialog. */
  nextFile = '';
  mismatch = false;

  settings = vi.fn(async () => this.current);

  choose = vi.fn(async (kind: TlsFileKind) => {
    if (this.nextFile !== '') {
      this.current = { ...this.current, [`${kind}_file`]: this.nextFile };
    }
    return this.evaluate();
  });

  clear = vi.fn(async () => {
    this.current = { ...this.current, cert_file: '', key_file: '', ca_file: '' };
    return this.evaluate();
  });

  private evaluate(): TLSSettings {
    const complete = this.current.cert_file !== '' && this.current.key_file !== '' && this.current.ca_file !== '';
    this.current = { ...this.current, active: complete && !this.mismatch, error: complete && this.mismatch ? 'the key does not match the certificate' : '' };
    return this.current;
  }
}

/** An engine whose 'tls' feature follows the fake identity, as the desktop engine's does. */
class TlsEngine extends MockService {
  healthChecks = 0;

  constructor(private readonly identity: FakeTls) {
    super();
  }

  override async health(): Promise<Health> {
    this.healthChecks++;
    return { ...(await super.health()), features: this.identity.current.active ? ['recording', 'tls'] : ['recording'] };
  }
}

describe('TLS identity', () => {
  async function openWithTlsUrl(tls: FakeTls | undefined) {
    const user = userEvent.setup();
    const service = new TlsEngine(tls ?? new FakeTls());
    const open = vi.spyOn(service, 'openSession');
    render(<App service={service} platform={{ ...fakePlatform('dark'), mode: { kind: tls ? 'local' : 'browser' }, tls }} />);
    await screen.findByText(/^engine /);
    const field = screen.getByLabelText('Device URL');
    await user.clear(field);
    await user.type(field, 'tcp+tls://10.0.0.5:802');
    return { user, service, open };
  }

  it('is chosen file by file until the engine can open TLS targets', async () => {
    const tls = new FakeTls();
    const { user, service, open } = await openWithTlsUrl(tls);

    expect(screen.getByRole('alert')).toHaveTextContent('TLS targets need a TLS identity');
    expect(screen.getByRole('alert')).not.toHaveTextContent('--tls-cert');
    expect(screen.getByRole('button', { name: 'Connect' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'Choose TLS identity…' }));

    const dialog = within(await screen.findByRole('dialog', { name: 'TLS identity' }));
    expect(await dialog.findByText('Choose all three files')).toBeInTheDocument();
    expect(dialog.getByText(/Private key files stay on this computer/)).toBeInTheDocument();
    expect(dialog.getAllByText('not chosen')).toHaveLength(3);
    expect(dialog.getByRole('button', { name: 'Clear' })).toBeDisabled();

    tls.nextFile = '';
    await user.click(dialog.getByRole('button', { name: 'Choose Certificate (PEM)' }));
    expect(dialog.getAllByText('not chosen')).toHaveLength(3);

    tls.nextFile = '/home/me/pki/client.crt';
    await user.click(dialog.getByRole('button', { name: 'Choose Certificate (PEM)' }));
    expect(await dialog.findByText('client.crt')).toBeInTheDocument();
    expect(dialog.getByText('/home/me/pki/client.crt')).toBeInTheDocument();
    tls.nextFile = 'C:\\pki\\client.key';
    await user.click(dialog.getByRole('button', { name: 'Choose Private key (PEM)' }));
    expect(await dialog.findByText('client.key')).toBeInTheDocument();
    expect(dialog.getByText('Choose all three files')).toBeInTheDocument();
    tls.nextFile = '/home/me/pki/ca.pem';
    const before = service.healthChecks;
    await user.click(dialog.getByRole('button', { name: 'Choose CA or device certificate (PEM)' }));

    expect(await dialog.findByText('Active: TLS targets can be opened')).toBeInTheDocument();
    await vi.waitFor(() => expect(service.healthChecks).toBeGreaterThan(before));
    await user.click(dialog.getByRole('button', { name: 'Close' }));
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Connect' })).toBeEnabled());
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Connect' }));
    expect(open).toHaveBeenCalledWith(expect.objectContaining({ url: 'tcp+tls://10.0.0.5:802' }));
  });

  it('shows why an identity is not active, and can be cleared', async () => {
    const tls = new FakeTls();
    tls.mismatch = true;
    tls.current = { ...tls.current, cert_file: '/pki/a.crt', key_file: '/pki/b.key', ca_file: '/pki/ca.pem', error: 'the key does not match the certificate' };
    const { user } = await openWithTlsUrl(tls);

    await user.click(screen.getByRole('button', { name: 'TLS identity…' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'TLS identity' }));
    expect(await dialog.findByText('the key does not match the certificate')).toBeInTheDocument();

    await user.click(dialog.getByRole('button', { name: 'Clear' }));
    expect(await dialog.findByText('Choose all three files')).toBeInTheDocument();
    expect(dialog.getAllByText('not chosen')).toHaveLength(3);
    expect(tls.clear).toHaveBeenCalledOnce();
  });

  it('is read-only when it came from the command line', async () => {
    const tls = new FakeTls();
    tls.current = { cert_file: '/etc/pki/a.crt', key_file: '/etc/pki/a.key', ca_file: '/etc/pki/ca.pem', active: true, error: '', from_flags: true };
    const { user } = await openWithTlsUrl(tls);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'TLS identity…' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'TLS identity' }));
    expect(await dialog.findByText(/given on the command line .* cannot be changed here/)).toBeInTheDocument();
    expect(dialog.getByText('Active: TLS targets can be opened')).toBeInTheDocument();
    for (const name of ['Choose Certificate (PEM)', 'Choose Private key (PEM)', 'Choose CA or device certificate (PEM)', 'Clear']) {
      expect(dialog.getByRole('button', { name })).toBeDisabled();
    }
  });

  it('is not offered in the browser: the daemon gets its identity from flags', async () => {
    await openWithTlsUrl(undefined);
    expect(screen.getByRole('alert')).toHaveTextContent('--tls-cert, --tls-key and --tls-ca');
    expect(screen.queryByRole('button', { name: /TLS identity/ })).not.toBeInTheDocument();
  });
});

