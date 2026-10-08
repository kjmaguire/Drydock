// Read hooks. Every hook is "an RPC read plus the events that invalidate it": the screen state is a projection of what the
// core holds, re-read when the event log says something relevant changed. Nothing here is authoritative.

import { useCallback, useEffect, useRef, useState } from 'react';
import type { DrydockEvent, Message, PresenceState } from '../api/types';
import { RpcError } from '../api/transport';
import { useClient, useServices, useStoreSnapshot } from './context';

/** Events that arrive in bulk while an agent streams; they never invalidate a read by themselves. */
export const isNoisy = (e: DrydockEvent): boolean => e.type === 'agent.output' || e.type === 'workflow.node_chunk';

export interface Live<T> {
  data: T | undefined;
  error: RpcError | Error | undefined;
  loading: boolean;
  reload(): void;
}

/**
 * Run `fetcher` now and again whenever an accepted event satisfies `invalidates` (debounced) or the stream reconnects.
 * Responses that arrive out of order are dropped. `deps` re-run the fetch when they change; pass `enabled=false` to idle.
 */
export function useLive<T>(
  fetcher: () => Promise<T>,
  deps: readonly unknown[],
  invalidates: (e: DrydockEvent) => boolean,
  enabled = true,
  debounceMs = 100,
): Live<T> {
  const { store } = useServices();
  const { epoch } = useStoreSnapshot();
  const [state, setState] = useState<{ data: T | undefined; error: Error | undefined; loading: boolean }>({ data: undefined, error: undefined, loading: enabled });
  const fetchRef = useRef(fetcher);
  fetchRef.current = fetcher;
  const invRef = useRef(invalidates);
  invRef.current = invalidates;
  const gen = useRef(0);
  const mounted = useRef(true);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const lastDeps = useRef<string | undefined>(undefined);

  const run = useCallback(() => {
    const my = ++gen.current;
    fetchRef.current().then(
      (data) => {
        if (mounted.current && my === gen.current) setState({ data, error: undefined, loading: false });
      },
      (error: Error) => {
        if (mounted.current && my === gen.current) setState((s) => ({ data: s.data, error, loading: false }));
      },
    );
  }, []);

  // deps change: reset and fetch
  const depKey = JSON.stringify(deps);
  useEffect(() => {
    mounted.current = true;
    if (!enabled) {
      setState({ data: undefined, error: undefined, loading: false });
      return () => {
        mounted.current = false;
      };
    }
    const changed = lastDeps.current !== depKey;
    lastDeps.current = depKey;
    setState((s) => (changed ? { data: undefined, error: undefined, loading: true } : { ...s, loading: true }));
    run();
    return () => {
      mounted.current = false;
      gen.current++;
    };
  }, [depKey, enabled, epoch, run]);

  useEffect(() => {
    if (!enabled) return;
    const off = store.onEvent((e) => {
      if (isNoisy(e) || !invRef.current(e)) return;
      clearTimeout(timer.current);
      timer.current = setTimeout(run, debounceMs);
    });
    return () => {
      off();
      clearTimeout(timer.current);
    };
  }, [store, enabled, run, debounceMs]);

  return { ...state, reload: run };
}

const starts = (e: DrydockEvent, ...p: string[]) => p.some((x) => e.type.startsWith(x));

export function useProjects() {
  const c = useClient();
  return useLive(() => c.projectList(), [], (e) => starts(e, 'project.'));
}

export function useAgents(projectId: string | undefined) {
  const c = useClient();
  return useLive(() => c.agentList(projectId!), [projectId], (e) => starts(e, 'agent.created') && (!e.projectId || e.projectId === projectId), !!projectId);
}

export function useRooms(projectId: string | undefined) {
  const c = useClient();
  return useLive(() => c.roomList(projectId!), [projectId], (e) => starts(e, 'room.created', 'room.archived', 'room.member', 'room.kind', 'room.mode'), !!projectId);
}

export function useUnread(projectId: string | undefined) {
  const c = useClient();
  return useLive(() => c.roomUnread(projectId!), [projectId], (e) => starts(e, 'room.message_posted', 'room.read_cursor', 'room.created', 'room.archived'), !!projectId);
}

export function useNeedsYou(projectId: string | undefined) {
  const c = useClient();
  return useLive(
    () => c.needsYouList(projectId!),
    [projectId],
    (e) => starts(e, 'approval.', 'workflow.request', 'task.', 'watchdog.', 'room.message_posted', 'room.archived'),
    !!projectId,
  );
}

export function usePresence() {
  const c = useClient();
  return useLive<PresenceState[]>(() => c.presenceList(), [], (e) => starts(e, 'room.presence_changed', 'agent.', 'watchdog.'));
}

export function useTasks(projectId: string | undefined) {
  const c = useClient();
  return useLive(() => c.taskList(projectId!), [projectId], (e) => starts(e, 'task.') && (!e.projectId || e.projectId === projectId), !!projectId);
}

export function useTaskDetail(taskId: string | undefined) {
  const c = useClient();
  return useLive(
    () => c.taskGet(taskId!),
    [taskId],
    (e) => e.taskId === taskId || (e.type.startsWith('approval.') && !e.taskId),
    !!taskId,
    80,
  );
}

export function useAdapters() {
  const c = useClient();
  return useLive(() => c.adapterList(), [], () => false);
}

/** A room's messages in seq order, loaded in pages and extended when room.message_posted names the room. */
export function useMessages(roomId: string | undefined): { messages: Message[]; error: Error | undefined; loading: boolean } {
  const c = useClient();
  const { store } = useServices();
  const { epoch } = useStoreSnapshot();
  const [messages, setMessages] = useState<Message[]>([]);
  const [error, setError] = useState<Error | undefined>();
  const [loading, setLoading] = useState(!!roomId);
  const lastSeq = useRef(0);
  const inFlight = useRef(false);
  const again = useRef(false);
  const room = useRef<string | undefined>(roomId);
  room.current = roomId;

  const pull = useCallback(async () => {
    if (inFlight.current) {
      again.current = true;
      return;
    }
    inFlight.current = true;
    const mine = room.current;
    try {
      for (;;) {
        again.current = false;
        if (!mine) return;
        const page = await c.roomMessages(mine, lastSeq.current, 200);
        if (room.current !== mine) return;
        if (page.length > 0) {
          lastSeq.current = Math.max(lastSeq.current, ...page.map((m) => m.seq));
          setMessages((cur) => {
            const have = new Set(cur.map((m) => m.id));
            const add = page.filter((m) => !have.has(m.id));
            return add.length ? [...cur, ...add].sort((a, b) => a.seq - b.seq) : cur;
          });
        }
        setError(undefined);
        if (page.length < 200 && !again.current) return;
      }
    } catch (e) {
      if (room.current === mine) setError(e as Error);
    } finally {
      inFlight.current = false;
      setLoading(false);
    }
  }, [c]);

  useEffect(() => {
    lastSeq.current = 0;
    setMessages([]);
    setError(undefined);
    setLoading(!!roomId);
    if (roomId) void pull();
  }, [roomId, pull]);

  useEffect(() => {
    if (roomId) void pull();
  }, [epoch, roomId, pull]);

  useEffect(() => {
    if (!roomId) return;
    return store.onEvent((e) => {
      if (e.type !== 'room.message_posted') return;
      const d = e.data as { room?: string } | undefined;
      if (d?.room === roomId) void pull();
    });
  }, [store, roomId, pull]);

  return { messages, error, loading };
}
