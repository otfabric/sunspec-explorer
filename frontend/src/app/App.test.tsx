import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { App } from './App';
import type { Platform, Theme } from './platform/platform';
import { MockService } from './service/mock';

const fakePlatform = (theme: Theme): Platform => ({
  mode: { kind: 'browser' },
  saveFile: vi.fn(async () => true),
  saveBlob: vi.fn(async () => true),
  openExternal: vi.fn(),
  persistTheme: vi.fn(),
  loadTheme: () => theme,
  copyText: vi.fn(async () => {}),
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

    await user.click(screen.getByRole('button', { name: 'Export snapshot' }));
    expect(platform.saveFile).not.toHaveBeenCalled();
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Export' }));
    await vi.waitFor(() => expect(platform.saveFile).toHaveBeenCalledOnce());
    const [name, mime, text] = vi.mocked(platform.saveFile).mock.calls[0] as [string, string, string];
    expect(name).toMatch(/^sunspec-snapshot-\d{8}T\d{6}Z\.json$/);
    expect(mime).toBe('application/json');
    expect(JSON.parse(text)).toMatchObject({ schema: 'otfabric.sunspec-explorer.snapshot/1', metadata: 'full' });
    expect(JSON.parse(text).models[3].raw_registers).toHaveLength(12);
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
    await user.click(screen.getByRole('button', { name: 'Save a copy' }));
    await vi.waitFor(() => expect(platform.saveBlob).toHaveBeenCalledOnce());
    const [name, blob] = vi.mocked(platform.saveBlob).mock.calls[0] as [string, Blob];
    expect(name).toBe('bench-test.jsonl');
    expect((await blob.text()).trim().split('\n').map((line) => JSON.parse(line).type)).toEqual(expect.arrayContaining(['header', 'sample', 'footer']));

    await user.click(screen.getByRole('button', { name: 'Recordings' }));
    const list = within(screen.getByRole('dialog', { name: 'Recordings' }));
    const row = within(await list.findByRole('row', { name: /bench-test/ }));
    expect(row.getByText('stopped')).toBeInTheDocument();
    await user.click(row.getByRole('button', { name: 'Delete bench-test' }));
    await user.click(within(screen.getByRole('dialog', { name: 'Delete recording' })).getByRole('button', { name: 'Delete' }));
    expect(await list.findByText('There are no recordings.')).toBeInTheDocument();
    expect(screen.queryByText(/Recording bench-test stopped/)).not.toBeInTheDocument();
  }, 15_000);
});
