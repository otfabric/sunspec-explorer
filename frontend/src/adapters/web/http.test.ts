import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LinkStatus } from '../../app/service/service';
import type { ExplorerEvent } from '../../app/service/types';
import { HttpService } from './http';
import { takeToken } from './token';

const TOKEN = 's3cr3t-token-0123456789';

const platform = { saveBlob: vi.fn(async (_name: string, _blob: Blob) => true) };

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const frame = (event: Partial<ExplorerEvent>) => `id: ${event.seq}\nevent: ${event.kind}\ndata: ${JSON.stringify(event)}\n\n`;

/** An event stream that delivers its text and then ends, or stays open. */
function stream(text: string, open = false): Response {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(text));
      if (!open) {
        controller.close();
      }
    },
  });
  return new Response(body, { status: 200 });
}

const urlsOf = (fetchMock: ReturnType<typeof vi.fn>) => fetchMock.mock.calls.map(([url]) => String(url));

describe('HttpService', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('keeps the token in memory only: not in the address, storage, cookies or request URLs', async () => {
    window.history.replaceState(null, '', `/#token=${TOKEN}`);
    const fetchMock = vi.fn(async (url: string) => (url.includes('/events') ? stream(': keep-alive\n\n', true) : json({ status: 'ok' })));
    vi.stubGlobal('fetch', fetchMock);

    const service = new HttpService(takeToken(window), platform);
    const unsubscribe = service.subscribe(0, () => {});
    await service.health();
    await service.sessions();
    await vi.advanceTimersByTimeAsync(0);
    unsubscribe();

    expect(window.location.href).not.toContain(TOKEN);
    expect(window.location.hash).toBe('');
    expect(JSON.stringify({ ...localStorage })).not.toContain(TOKEN);
    expect(JSON.stringify({ ...sessionStorage })).not.toContain(TOKEN);
    expect(document.cookie).not.toContain(TOKEN);
    expect(fetchMock).toHaveBeenCalledTimes(3);
    for (const [url, init] of fetchMock.mock.calls as unknown as [string, RequestInit][]) {
      expect(url).not.toContain(TOKEN);
      expect(init.headers).toMatchObject({ Authorization: `Bearer ${TOKEN}` });
      expect(init.body ?? '').not.toContain(TOKEN);
    }
  });

  it('reports a lost stream, resumes from the last sequence number and follows a resync down', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(stream(frame({ seq: 4, kind: 'sample' }) + frame({ seq: 5, kind: 'sample' })))
      .mockRejectedValueOnce(new TypeError('network'))
      // A restarted daemon: it cannot replay and numbers its events from 1 again.
      .mockResolvedValueOnce(stream(frame({ seq: 2, kind: 'resync' })))
      .mockResolvedValueOnce(stream('', true));
    vi.stubGlobal('fetch', fetchMock);
    const events: number[] = [];
    const link: LinkStatus[] = [];

    const unsubscribe = new HttpService(TOKEN, platform).subscribe(0, (event) => events.push(event.seq), (status) => link.push(status));
    await vi.advanceTimersByTimeAsync(10_000);
    unsubscribe();

    expect(urlsOf(fetchMock)).toEqual(['/api/v1/events?since=0', '/api/v1/events?since=5', '/api/v1/events?since=5', '/api/v1/events?since=2']);
    expect(events).toEqual([4, 5, 2]);
    expect(link).toEqual(['up', 'down', 'down', 'up', 'down', 'up']);
  });

  it('treats a stream without keep-alives as lost', async () => {
    const fetchMock = vi.fn(async (_url: string, init: RequestInit) => {
      const response = stream(': keep-alive\n\n', true);
      // Like a real fetch, an aborted request fails its pending read.
      init.signal?.addEventListener('abort', () => void response.body?.cancel().catch(() => {}));
      return response;
    });
    vi.stubGlobal('fetch', fetchMock);
    const link: LinkStatus[] = [];
    const abortSpy = vi.spyOn(AbortController.prototype, 'abort');

    const unsubscribe = new HttpService(TOKEN, platform).subscribe(0, () => {}, (status) => link.push(status));
    await vi.advanceTimersByTimeAsync(44_000);
    expect(abortSpy).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(2_000);
    expect(abortSpy).toHaveBeenCalled();
    unsubscribe();
    expect(link[0]).toBe('up');
  });

  it('reports the link as down and reopens the stream when a request cannot reach the daemon', async () => {
    const fetchMock = vi.fn(async (url: string) => {
      if (url.includes('/events')) {
        return stream('', true);
      }
      throw new TypeError('network');
    });
    vi.stubGlobal('fetch', fetchMock);
    const link: LinkStatus[] = [];
    const service = new HttpService(TOKEN, platform);
    const unsubscribe = service.subscribe(0, () => {}, (status) => link.push(status));
    await vi.advanceTimersByTimeAsync(0);

    await expect(service.health()).rejects.toMatchObject({ message: 'The daemon cannot be reached.' });
    expect(link).toEqual(['up', 'down']);
    unsubscribe();
  });

  it('reports a rejected token as unauthorized and stops the stream', async () => {
    const fetchMock = vi.fn(async () => json({ error: { code: 'unauthorized', message: 'invalid token' } }, 401));
    vi.stubGlobal('fetch', fetchMock);
    const link: LinkStatus[] = [];
    const service = new HttpService('wrong', platform);
    service.subscribe(0, () => {}, (status) => link.push(status));

    await expect(service.health()).rejects.toMatchObject({ code: 'unauthorized', message: 'invalid token' });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(link).toEqual(['unauthorized', 'unauthorized']);
    expect(urlsOf(fetchMock).filter((url) => url.includes('/events'))).toHaveLength(1);
  });

  it('does not contact the daemon without a token', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const link: LinkStatus[] = [];
    const service = new HttpService(null, platform);
    service.subscribe(0, () => {}, (status) => link.push(status));

    await expect(service.sessions()).rejects.toMatchObject({ code: 'unauthorized' });
    expect(link).toEqual(['unauthorized', 'unauthorized']);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('passes error bodies on, with probe attempts, and accepts an empty 204', async () => {
    const attempts = [{ base_address: 40000, reg_type: 'holding', matched: false, error: 'timeout' }];
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(json({ error: { code: 'not_sunspec', message: 'no marker', attempts } }, 502))
        .mockResolvedValueOnce(new Response(null, { status: 204 })),
    );
    const service = new HttpService(TOKEN, platform);
    await expect(service.openSession({ url: 'tcp://10.0.0.1:502', unit_id: 1 })).rejects.toMatchObject({ code: 'not_sunspec', attempts });
    await expect(service.closeSession('s1')).resolves.toBeUndefined();
  });

  it('downloads a recording with the token in the header, not in the URL, and hands it to the platform', async () => {
    const lines = '{"type":"header"}\n{"type":"sample","seq":1}\n';
    const fetchMock = vi.fn(
      async (_url: string, _init: RequestInit) =>
        new Response(lines, { status: 200, headers: { 'Content-Type': 'application/x-ndjson', 'Content-Disposition': 'attachment; filename="night-run.jsonl"' } }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(new HttpService(TOKEN, platform).saveRecording('rec 1')).resolves.toBe(true);

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/recordings/rec%201/download');
    expect(url).not.toContain(TOKEN);
    expect(init.headers).toMatchObject({ Authorization: `Bearer ${TOKEN}` });
    const [name, blob] = platform.saveBlob.mock.calls.at(-1) as [string, Blob];
    expect(name).toBe('night-run.jsonl');
    expect(await blob.text()).toBe(lines);
  });

  it('reports why a recording cannot be downloaded or deleted', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ error: { code: 'not_found', message: 'no recording r9' } }, 404)));
    const service = new HttpService(TOKEN, platform);
    platform.saveBlob.mockClear();
    await expect(service.saveRecording('r9')).rejects.toMatchObject({ code: 'not_found', message: 'no recording r9' });
    await expect(service.deleteRecording('r9')).rejects.toMatchObject({ code: 'not_found' });
    expect(platform.saveBlob).not.toHaveBeenCalled();
  });

  it('asks for a recording as CSV with a query parameter that is not the token', async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => new Response('captured_at,seq\n', { status: 200, headers: { 'Content-Type': 'text/csv' } }));
    vi.stubGlobal('fetch', fetchMock);
    platform.saveBlob.mockClear();

    await new HttpService(TOKEN, platform).saveRecording('r1', 'csv');

    expect(urlsOf(fetchMock)).toEqual(['/api/v1/recordings/r1/download?format=csv']);
    expect((fetchMock.mock.calls[0] as [string, RequestInit])[1].headers).toMatchObject({ Authorization: `Bearer ${TOKEN}` });
    expect(platform.saveBlob.mock.calls[0]?.[0]).toBe('r1.csv');
  });

  it('has the daemon render an export', async () => {
    const file = { name: 'points.csv', mime: 'text/csv', text: 'captured_at\n' };
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => json(file));
    vi.stubGlobal('fetch', fetchMock);

    await expect(new HttpService(TOKEN, platform).exportSession('s 1', 'csv')).resolves.toEqual(file);

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/sessions/s%201/export');
    expect(init).toMatchObject({ method: 'POST', body: '{"format":"csv"}' });
  });
});
