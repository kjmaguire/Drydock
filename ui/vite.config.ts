/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';

// Development proxy: `DRYDOCK_DEV_URL=http://127.0.0.1:PORT npm run dev` forwards /rpc and /events to a running
// `cmd/drydock-dev` (the Go core with the HTTP/SSE dev transport). The Origin header is dropped because the Go server
// accepts only same-host origins (DNS rebinding guard). The token still comes from the page URL (?token=).
const target = process.env.DRYDOCK_DEV_URL;
const proxy = target
  ? Object.fromEntries(
      ['/rpc', '/events'].map((p) => [
        p,
        {
          target,
          changeOrigin: true,
          configure: (px: { on: (ev: string, cb: (req: { removeHeader: (n: string) => void }) => void) => void }) =>
            px.on('proxyReq', (req) => req.removeHeader('origin')),
        },
      ]),
    )
  : undefined;

// Defense in depth for the built UI: no inline script (so no injected event handler or javascript: URL can run) and no
// remote image or other remote load. Added only for the production build because Vite's dev server injects inline scripts.
const csp = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'";
const cspPlugin: Plugin = {
  name: 'drydock-csp',
  apply: 'build',
  transformIndexHtml: (html) => html.replace('<head>', `<head>\n    <meta http-equiv="Content-Security-Policy" content="${csp}" />`),
};

export default defineConfig({
  plugins: [react(), cspPlugin],
  base: './',
  server: { proxy },
  build: { outDir: 'dist', sourcemap: false },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    restoreMocks: true,
  },
});
