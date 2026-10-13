import { defineConfig, type Plugin } from 'vitest/config';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

type Target = 'wails' | 'web';

// One source tree, two builds: `--mode wails` and `--mode web` pick the entry
// HTML and the output directory. Both outputs must contain an index.html.
function entryHtml(target: Target): Plugin {
  const entry = `index.${target}.html`;
  return {
    name: 'explorer-entry-html',
    enforce: 'post',
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        const [path, query] = (req.url ?? '/').split('?');
        if (path === '/' || path === '/index.html') {
          req.url = `/${entry}${query === undefined ? '' : `?${query}`}`;
        }
        next();
      });
    },
    generateBundle(_options, bundle) {
      const html = bundle[entry];
      if (html) {
        html.fileName = 'index.html';
      }
    },
  };
}

export default defineConfig(({ mode }) => {
  const target: Target = mode === 'wails' ? 'wails' : 'web';
  return {
    plugins: [react(), tailwindcss(), entryHtml(target)],
    build: {
      outDir: `dist/${target}`,
      emptyOutDir: true,
      // The web build runs under CSP `default-src 'self'`: a data: URI for a
      // small font or image would be blocked, so nothing is inlined.
      assetsInlineLimit: 0,
      rollupOptions: { input: `index.${target}.html` },
    },
    test: {
      environment: 'jsdom',
      setupFiles: ['src/test/setup.ts'],
      css: false,
    },
  };
});
