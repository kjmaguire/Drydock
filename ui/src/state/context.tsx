import { createContext, useContext, useSyncExternalStore, type ReactNode } from 'react';
import type { Client } from '../api/client';
import type { EventStore, StoreSnapshot } from './eventStore';

export interface AppServices {
  client: Client;
  store: EventStore;
  transportName: string;
}

const Ctx = createContext<AppServices | null>(null);

export function AppServicesProvider({ services, children }: { services: AppServices; children: ReactNode }) {
  return <Ctx.Provider value={services}>{children}</Ctx.Provider>;
}

export function useServices(): AppServices {
  const v = useContext(Ctx);
  if (!v) throw new Error('AppServicesProvider is missing');
  return v;
}

export function useClient(): Client {
  return useServices().client;
}

export function useStoreSnapshot(): StoreSnapshot {
  const { store } = useServices();
  return useSyncExternalStore(store.subscribe, store.getSnapshot);
}
