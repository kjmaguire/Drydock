import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { createClient } from './api/client';
import { HttpTransport, resolveToken, type Transport } from './api/transport';
import { isWailsHost, loadWailsRuntime, WailsTransport } from './api/wails';
import { AppServicesProvider } from './state/context';
import { EventStore } from './state/eventStore';
import './styles.css';

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
    window.history.replaceState(null, '', window.location.pathname + window.location.hash);
  }
  return new HttpTransport({ token });
}

async function boot() {
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
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <AppServicesProvider services={{ client, store, transportName: transport.name }}>
        <App />
      </AppServicesProvider>
    </StrictMode>,
  );
}
void boot();
