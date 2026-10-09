// Browser entry: pick the transport (the Wails bridge in the desktop app, the HTTP dev transport elsewhere), read the
// event log head, start the event store, build the services and hand the router to the Remix SPA runtime.

import '@fontsource-variable/inter';
import './styles/app.css';
import { run } from 'remix/spa';
import { createClient } from './api/client.ts';
import { HttpTransport, resolveToken, type Transport } from './api/transport.ts';
import { isWailsHost, loadWailsRuntime, WailsTransport } from './api/wails.ts';
import { applyTheme, loadTheme } from './lib/theme.ts';
import { hashToPath } from './lib/route.ts';
import { createAppRouter } from './router.tsx';
import { EventStore } from './state/eventStore.ts';
import { ProjectData } from './state/project.ts';
import type { AppServices } from './state/services.tsx';
import { UiModel } from './state/ui.ts';
import { Workspace } from './state/workspace.ts';
import { Loading } from './ui/feedback.tsx';

async function pickTransport(): Promise<Transport> {
  // Inside the Wails shell (WebView2) use the bridge; anywhere else use the HTTP dev transport with the URL token.
  if (isWailsHost()) {
    try {
      return WailsTransport.fromRuntime(await loadWailsRuntime());
    } catch (err) {
      console.error('Wails host detected but its runtime could not be loaded; falling back to HTTP', err);
    }
  }
  let storage: Storage | undefined;
  try {
    storage = window.sessionStorage;
  } catch {
    storage = undefined;
  }
  const token = resolveToken(window.location.search, storage);
  if (new URLSearchParams(window.location.search).has('token')) {
    // keep the secret out of the address bar, history and Referer; a reload still works through sessionStorage
    window.history.replaceState(null, '', window.location.pathname);
  }
  return new HttpTransport({ token });
}

async function boot() {
  applyTheme(loadTheme());
  // links from before the UI routed by path (#/task/x) still open the right screen
  const legacy = hashToPath(window.location.hash);
  if (legacy) window.history.replaceState(null, '', legacy);

  const transport = await pickTransport();
  const client = createClient(transport);
  // Exact catch-up without replaying the whole log: read the head first, open the stream after it, and only then let the
  // screens read their projections. Anything that changes after the head arrives as an event and re-reads; anything before
  // it is already in the reads. If the head cannot be read (for example a wrong token) start from 0 and let the screens
  // show the error.
  let head = 0;
  try {
    head = await client.eventsHead();
  } catch {
    head = 0;
  }
  const store = new EventStore(transport, { notifyEveryMs: 50, initialAfter: head });
  store.start();
  const workspace = new Workspace(client, store).start();
  const services: AppServices = {
    client,
    store,
    transportName: transport.name,
    ui: new UiModel(),
    workspace,
    project: new ProjectData(client, store, workspace).start(),
  };

  if (!('navigation' in window)) console.warn('This browser has no Navigation API: links will load whole documents.');
  const app = run(createAppRouter(services), { fallback: <Loading label="Starting Drydock..." /> });
  app.addEventListener('error', (e) => console.error('Drydock UI error:', (e as ErrorEvent).error));
  await app.ready();
}

void boot();
