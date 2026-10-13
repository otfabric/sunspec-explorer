// Mounts the web UI the way src/main.web.tsx does (token from the fragment,
// HttpService, WebPlatform) and drives it against a real daemon and device.

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeAll, describe, expect, it } from 'vitest';
import { HttpService } from '../src/adapters/web/http';
import { WebPlatform } from '../src/adapters/web/platform';
import { takeToken } from '../src/adapters/web/token';
import { App } from '../src/app/App';
import type { Theme } from '../src/app/platform/platform';
import { persistTheme } from '../src/app/platform/themeStorage';
import type { SessionInfo } from '../src/app/service/types';

const DAEMON = process.env.EXPLORER_URL as string;
const TOKEN = process.env.EXPLORER_TOKEN as string;
const TARGET = process.env.EXPLORER_TARGET as string;
const SLOW = { timeout: 20_000 };

const nodeFetch = globalThis.fetch;
const api = (path: string, init: RequestInit = {}) =>
  nodeFetch(new URL(path, DAEMON), { ...init, headers: { Authorization: `Bearer ${TOKEN}` } });

beforeAll(() => {
  // In a browser the page's origin resolves the UI's relative URLs; Node's
  // fetch has no page, so it gets the daemon's address.
  globalThis.fetch = (input, init) => nodeFetch(typeof input === 'string' ? new URL(input, DAEMON) : input, init);
});

afterEach(async () => {
  // A failed test must not leave its session behind for the next one to adopt.
  const open = (await (await api('/api/v1/sessions')).json()) as SessionInfo[];
  await Promise.all(open.map((session) => api(`/api/v1/sessions/${session.id}`, { method: 'DELETE' })));
});

function openPage(token: string, theme: Theme) {
  persistTheme(theme);
  window.history.replaceState(null, '', `/#token=${token}`);
  const platform = new WebPlatform();
  return render(<App service={new HttpService(takeToken(window), platform)} platform={platform} />);
}

// EXPLORER_RECORDING=0 skips the recording steps, for a daemon that cannot record yet.
const RECORDING = process.env.EXPLORER_RECORDING !== '0';

const samplesRecorded = (bar: HTMLElement) => Number(/(\d+) samples?/.exec(bar.textContent ?? '')?.[1] ?? -1);

/** Start a recording, see it grow with the samples, stop it, find it in the list, delete it. */
async function recordWhilePolling(user: ReturnType<typeof userEvent.setup>) {
  const name = `smoke-${document.documentElement.dataset.theme}-${Date.now()}`;
  await user.click(screen.getByRole('button', { name: 'Record' }));
  const naming = within(screen.getByRole('dialog', { name: 'Start recording' }));
  await user.type(naming.getByLabelText('Name (optional)'), name);
  await user.click(naming.getByRole('button', { name: 'Start recording' }));

  const bar = await screen.findByRole('region', { name: 'Recording' }, SLOW);
  expect(within(bar).getByText(name)).toBeInTheDocument();
  await waitFor(() => expect(samplesRecorded(bar)).toBeGreaterThanOrEqual(1), SLOW);
  const seen = samplesRecorded(bar);
  await waitFor(() => expect(samplesRecorded(bar)).toBeGreaterThan(seen), SLOW);

  await user.click(screen.getByRole('button', { name: 'Stop recording' }));
  expect(await screen.findByText(`Recording ${name} stopped: stopped by you`, {}, SLOW)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Save a copy' })).toBeEnabled();

  await user.click(screen.getByRole('button', { name: 'Recordings' }));
  const list = within(screen.getByRole('dialog', { name: 'Recordings' }));
  const row = within(await list.findByRole('row', { name: new RegExp(name) }, SLOW));
  expect(row.getByText('stopped')).toBeInTheDocument();
  await user.click(row.getByRole('button', { name: `Delete ${name}` }));
  await user.click(within(screen.getByRole('dialog', { name: 'Delete recording' })).getByRole('button', { name: 'Delete' }));
  await waitFor(() => expect(list.queryByRole('row', { name: new RegExp(name) })).not.toBeInTheDocument(), SLOW);
  await user.click(list.getByRole('button', { name: 'Close' }));
}

const cyclesShown = () => Number(/cycles (\d+)/.exec(document.body.textContent ?? '')?.[1] ?? -1);

describe.each<Theme>(['dark', 'light'])('web UI against the daemon, %s theme', (theme) => {
  it('connects, browses, polls over the event stream and disconnects', async () => {
    const user = userEvent.setup();
    openPage(TOKEN, theme);
    expect(document.documentElement.dataset.theme).toBe(theme);
    expect(window.location.hash).toBe('');
    expect(window.location.href).not.toContain(TOKEN);

    const url = screen.getByLabelText('Device URL');
    await user.clear(url);
    await user.type(url, TARGET);
    await user.click(screen.getByRole('button', { name: 'Connect' }));

    const models = within(await screen.findByRole('navigation', { name: 'Models' }, SLOW));
    for (const id of ['1', '103', '705', '64900']) {
      expect(models.getByText(id, { exact: true })).toBeInTheDocument();
    }
    expect(models.getAllByText('160', { exact: true })).toHaveLength(2);
    expect(models.getByText('no schema')).toBeInTheDocument();

    // A point of model 103 with a number in the Value column (the third).
    await user.click(models.getByRole('button', { name: /103/ }));
    await waitFor(() => {
      const values = [...document.querySelectorAll('tbody tr[data-index] td:nth-child(3)')].map((cell) => cell.textContent ?? '');
      expect(values.some((value) => /\d/.test(value))).toBe(true);
    }, SLOW);

    const interval = screen.getByLabelText('Poll interval in whole seconds');
    await user.clear(interval);
    await user.type(interval, '1');
    await user.click(screen.getByRole('button', { name: 'Start polling' }));
    // The counter only moves on sample events from the stream.
    await waitFor(() => expect(cyclesShown()).toBeGreaterThanOrEqual(2), SLOW);
    const seen = cyclesShown();
    await waitFor(() => expect(cyclesShown()).toBeGreaterThan(seen), SLOW);

    if (RECORDING) {
      await recordWhilePolling(user);
    }

    await user.click(screen.getByRole('button', { name: 'Stop polling' }));
    expect(await screen.findByText('polling stopped', {}, SLOW)).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Disconnect' }));
    expect(await screen.findByText('not connected', {}, SLOW)).toBeInTheDocument();
    await waitFor(async () => expect(await (await api('/api/v1/sessions')).json()).toEqual([]), SLOW);
  });
});

describe('web UI with a token the daemon does not accept', () => {
  it('says that there is no valid token', async () => {
    openPage('not-the-token', 'dark');
    expect(await screen.findByRole('heading', { name: 'No valid token' }, SLOW)).toBeInTheDocument();
    expect(screen.getByText('sunspec-explorerd url')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeDisabled();
  });

  it('says the same when the page is opened without a token', async () => {
    window.history.replaceState(null, '', '/');
    const platform = new WebPlatform();
    render(<App service={new HttpService(takeToken(window), platform)} platform={platform} />);
    expect(await screen.findByRole('heading', { name: 'No valid token' }, SLOW)).toBeInTheDocument();
  });
});
