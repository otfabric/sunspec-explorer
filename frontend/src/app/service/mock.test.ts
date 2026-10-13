import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MockService } from './mock';
import { ServiceError } from './service';
import type { ExplorerEvent } from './types';

const target = { url: 'tcp://127.0.0.1:502', unit_id: 1 };

describe('MockService', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('discovers the chain with contiguous addresses', async () => {
    const session = await new MockService().openSession(target);
    expect(session.models.map((model) => [model.id, model.start_address, model.length])).toEqual([
      [1, 40002, 66],
      [103, 40070, 50],
      [160, 40122, 48],
      [64900, 40172, 12],
    ]);
    expect(session.models.map((model) => model.static_only)).toEqual([true, false, false, false]);
  });

  it('fails with probe attempts for the host fail.test', async () => {
    const error = await new MockService().openSession({ ...target, url: 'tcp://fail.test:502' }).catch((err: unknown) => err);
    expect(error).toBeInstanceOf(ServiceError);
    expect(error).toMatchObject({ code: 'not_sunspec' });
    expect((error as ServiceError).attempts).toHaveLength(3);
  });

  it('polls with compact samples and reports a failing model', async () => {
    const mock = new MockService();
    const events: ExplorerEvent[] = [];
    mock.subscribe(0, (event) => events.push(event));
    const session = await mock.openSession(target);
    mock.setModelFailing(2, true);
    await mock.startPolling(session.id, { interval_s: 1 });
    vi.advanceTimersByTime(2000);

    expect(events.map((event) => event.sample?.seq)).toEqual([1, 2]);
    const models = events[0]?.sample?.models ?? [];
    expect(models.map((model) => [model.index, model.status])).toEqual([[1, 'ok'], [2, 'error'], [3, 'ok']]);
    const watts = models[0]?.group?.points.find((point) => point.name === 'W');
    expect(watts).toMatchObject({ sf_name: 'W_SF', sf_raw: 1, units: 'W' });
    expect(watts).not.toHaveProperty('label');
    expect((await mock.state(session.id)).polling).toMatchObject({ active: true, cycles: 2, failures: 2 });

    await mock.stopPolling(session.id);
    vi.advanceTimersByTime(5000);
    expect(events).toHaveLength(2);
  });
});
