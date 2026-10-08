import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { createClient } from './api/client';
import { HttpTransport, resolveToken } from './api/transport';
import { AppServicesProvider } from './state/context';
import { EventStore } from './state/eventStore';
import './styles.css';

function boot() {
  // The Wails shell will pick WailsTransport here (src/api/wails.ts); only the HTTP dev transport is wired today.
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
  const transport = new HttpTransport({ token });
  const store = new EventStore(transport, { notifyEveryMs: 50 });
  store.start();
  const client = createClient(transport);
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <AppServicesProvider services={{ client, store, transportName: transport.name }}>
        <App />
      </AppServicesProvider>
    </StrictMode>,
  );
}
boot();
