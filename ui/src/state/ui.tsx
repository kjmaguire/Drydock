import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';

export type RightTab = 'agents' | 'task' | 'thread' | 'inspector';

export interface UiState {
  tab: RightTab;
  setTab(t: RightTab): void;
  inspectId: string | undefined;
  openInspector(agentId: string): void;
  thread: { roomId: string; rootId: string } | undefined;
  openThread(roomId: string, rootId: string): void;
  closeThread(): void;
}

const Ctx = createContext<UiState | null>(null);

/** Small cross-region UI state: which right-column tab is showing, which agent is inspected, which thread is open. */
export function UiProvider({ children, onReveal }: { children: ReactNode; onReveal?: () => void }) {
  const [tab, setTab] = useState<RightTab>('agents');
  const [inspectId, setInspectId] = useState<string | undefined>();
  const [thread, setThread] = useState<UiState['thread']>();
  const openInspector = useCallback(
    (id: string) => {
      setInspectId(id);
      setTab('inspector');
      onReveal?.();
    },
    [onReveal],
  );
  const openThread = useCallback(
    (roomId: string, rootId: string) => {
      setThread({ roomId, rootId });
      setTab('thread');
      onReveal?.();
    },
    [onReveal],
  );
  const closeThread = useCallback(() => {
    setThread(undefined);
    setTab('agents');
  }, []);
  const value = useMemo(() => ({ tab, setTab, inspectId, openInspector, thread, openThread, closeThread }), [tab, inspectId, thread, openInspector, openThread, closeThread]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

const fallback: UiState = { tab: 'agents', setTab: () => undefined, inspectId: undefined, openInspector: () => undefined, thread: undefined, openThread: () => undefined, closeThread: () => undefined };

/** Components used outside the provider (unit tests) get inert no-ops. */
export function useUi(): UiState {
  return useContext(Ctx) ?? fallback;
}
