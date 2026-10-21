import { remix } from '@pitlane/vite-plugin-remix';
import { defineConfig, type Plugin, type ProxyOptions } from 'vite';

// JSX compiles against remix/component (tsconfig: "jsx": "react-jsx", "jsxImportSource": "remix/component"); Vite reads
// both from tsconfig. Pitlane's Remix plugin in SPA mode (server: false) adds component hot module replacement in dev
// (an edited component swaps in place and keeps its state); the production build is the same static dist/. Its chunk
// import map is off: it is an inline <script type="importmap">, which the CSP below forbids.
//
// Development proxy: `DRYDOCK_DEV_URL=http://127.0.0.1:PORT npm run dev` forwards /rpc and /events to a running
// `cmd/drydock-dev` (the Go core with the HTTP/SSE dev transport). The Origin header is dropped because the Go server
// accepts only same-host origins (DNS rebinding guard). The token still comes from the page URL (?token=).
const target = process.env.DRYDOCK_DEV_URL;
const proxy: Record<string, ProxyOptions> | undefined = target
  ? Object.fromEntries(
      ['/rpc', '/events'].map((p) => [
        p,
        {
          target,
          changeOrigin: true,
          configure: (px) => {
            px.on('proxyReq', (req) => req.removeHeader('origin'));
          },
        } satisfies ProxyOptions,
      ]),
    )
  : undefined;

// Defense in depth for the built UI: no inline script (so no injected event handler or javascript: URL can run) and no
// remote image or other remote load. Added only for the production build because Vite's dev server injects inline scripts.
// Component styles are constructable stylesheets (CSSOM), which style-src does not govern; 'unsafe-inline' stays for the
// style attributes the editor and terminal widgets set.
const csp = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'";
const cspPlugin: Plugin = {
  name: 'drydock-csp',
  apply: 'build',
  transformIndexHtml: (html) => html.replace('<head>', `<head>\n    <meta http-equiv="Content-Security-Policy" content="${csp}" />`),
};

export default defineConfig({
  plugins: [remix({ server: false, assets: { chunkImportMap: false } }), cspPlugin],
  // the UI routes by path (/task/<id>), so assets must resolve from the root, never relative to the current path
  base: '/',
  server: { proxy },
  build: { outDir: 'dist', sourcemap: false, target: 'es2024' },
});
