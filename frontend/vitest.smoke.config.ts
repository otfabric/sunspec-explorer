// `npm run test:smoke`: the web build's code against a running daemon. Not
// part of `npm test`; a Go test starts a simulated device and the daemon and
// passes where they are in the environment.

import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

const REQUIRED = ['EXPLORER_URL', 'EXPLORER_TOKEN', 'EXPLORER_TARGET'];
const missing = REQUIRED.filter((name) => !process.env[name]);
if (missing.length > 0) {
  throw new Error(
    `test:smoke needs ${missing.join(', ')}. Set EXPLORER_URL (daemon, e.g. http://127.0.0.1:8765), ` +
      'EXPLORER_TOKEN (its bearer token) and EXPLORER_TARGET (a SunSpec device, e.g. tcp://127.0.0.1:15020).',
  );
}

export default defineConfig({
  plugins: [react()],
  test: {
    include: ['smoke/**/*.smoke.tsx'],
    environment: 'jsdom',
    // The page is served by the daemon: same origin as the API.
    environmentOptions: { jsdom: { url: `${process.env.EXPLORER_URL}/` } },
    setupFiles: ['src/test/setup.ts'],
    css: false,
    testTimeout: 60_000,
    fileParallelism: false,
  },
});
