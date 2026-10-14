import { expect, it } from 'vitest';
import type { Platform } from '../../app/platform/platform';
import { WebPlatform } from './platform';

it('offers no TLS identity dialog: the daemon gets its identity from its operator', () => {
  const platform: Platform = new WebPlatform();
  expect(platform.tls).toBeUndefined();
});
