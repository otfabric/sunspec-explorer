// Web entry, served by sunspec-explorerd. Nothing reachable from here may
// import @wailsio/runtime: the daemon's bundle must stay free of it.

import { HttpService } from './adapters/web/http';
import { WebPlatform } from './adapters/web/platform';
import { takeToken } from './adapters/web/token';
import { mount } from './app/mount';
import type { Service } from './app/service/service';

async function createService(platform: WebPlatform): Promise<Service> {
  const params = new URLSearchParams(location.search);
  // Development only: `?mock` runs against the in-memory device, and
  // `?mock&fail=2` makes the model at chain position 2 fail its reads.
  if (import.meta.env.DEV && params.has('mock')) {
    const { MockService } = await import('./app/service/mock');
    const mock = new MockService(platform);
    params.getAll('fail').forEach((index) => mock.setModelFailing(Number(index), true));
    return mock;
  }
  return new HttpService(takeToken(window), platform);
}

const platform = new WebPlatform();
void createService(platform).then((service) => mount(service, platform));
