// App services reach components through Remix component context: the provider is mounted once around the shell and the
// value never changes, so nothing re-renders because of it. Changing state lives in models (TypedEventTarget) that
// components subscribe to with watch().

import { TypedEventTarget, type Handle, type RemixNode } from 'remix/component';
import type { Client } from '../api/client.ts';
import type { DrydockEvent } from '../api/types.ts';
import type { EventStore } from './eventStore.ts';
import { Live } from './live.ts';
import type { ProjectData } from './project.ts';
import type { UiModel } from './ui.ts';
import type { Workspace } from './workspace.ts';

export interface AppServices {
  client: Client;
  store: EventStore;
  transportName: string;
  ui: UiModel;
  workspace: Workspace;
  /** The selected project's shared reads. */
  project: ProjectData;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type AnyHandle = Handle<any, any>;

export function Services(handle: Handle<{ value: AppServices; children?: RemixNode }, AppServices>) {
  handle.context.set(handle.props.value);
  return () => handle.props.children;
}

/** The app services from the nearest Services provider. */
export function services(handle: AnyHandle): AppServices {
  const s = handle.context.get(Services) as AppServices | undefined;
  if (!s) throw new Error('Services provider is missing');
  return s;
}

/** Re-render the component whenever the target emits `type` (default `change`), until the component is removed. */
export function watch(handle: AnyHandle, target: EventTarget, type = 'change'): void {
  target.addEventListener(type, () => void handle.update(), { signal: handle.signal });
}

export interface QueryOptions<T> {
  load: () => Promise<T>;
  when?: (e: DrydockEvent) => boolean;
  /** Re-read from scratch when this value changes (compared as JSON). Read from handle.props inside. */
  key?: () => unknown;
  /** While false nothing is read and data stays undefined. */
  enabled?: () => boolean;
  debounceMs?: number;
  /** Also re-read on streaming events (agent.output), at most once per debounceMs (LiveOptions.noisy). */
  noisy?: boolean;
}

export interface Query<T> {
  readonly data: T | undefined;
  readonly error: Error | undefined;
  readonly loading: boolean;
  /** Read again. The promise settles when that read has (answered, failed or been replaced by a newer one); it never rejects. */
  reload(): Promise<void>;
  /** Show an answer the caller already has (an action's result) instead of reading again. */
  set(data: T): void;
}

/**
 * A live read owned by a component (call in setup): read on first render, again when an accepted event matches `when` or
 * the stream reconnects, and from scratch when `key()` changes. The component re-renders when the answer arrives.
 */
export function query<T>(handle: AnyHandle, opts: QueryOptions<T>): Query<T> {
  const live = new Live<T>(services(handle).store, opts).start(handle.signal);
  watch(handle, live);
  let lastKey: string | undefined;
  let started = false;
  const sync = () => {
    const key = JSON.stringify(opts.key?.() ?? null);
    const enabled = opts.enabled?.() ?? true;
    const k = `${enabled}:${key}`;
    if (started && k === lastKey) return;
    started = true;
    lastKey = k;
    live.reset(enabled);
    if (enabled) handle.queueTask(() => live.reload());
  };
  return {
    get data() {
      sync();
      return live.data;
    },
    get error() {
      sync();
      return live.error;
    },
    get loading() {
      sync();
      return live.loading;
    },
    reload: () => live.reload(),
    set: (data) => live.set(data),
  };
}

/** A busy flag and the last error of async actions run by a component (one at a time). */
export interface Action {
  readonly busy: string;
  readonly error: Error | undefined;
  run<T>(label: string, fn: () => Promise<T>): Promise<T | undefined>;
  clear(): void;
}

export function action(handle: AnyHandle): Action {
  let busy = '';
  let error: Error | undefined;
  return {
    get busy() {
      return busy;
    },
    get error() {
      return error;
    },
    async run<T>(label: string, fn: () => Promise<T>): Promise<T | undefined> {
      busy = label;
      error = undefined;
      void handle.update();
      try {
        return await fn();
      } catch (e) {
        error = e instanceof Error ? e : new Error(String(e));
        return undefined;
      } finally {
        busy = '';
        void handle.update();
      }
    },
    clear() {
      error = undefined;
      void handle.update();
    },
  };
}

/** A small observable value for cross-component state (TypedEventTarget with one `change` event). */
export class Model extends TypedEventTarget<{ change: Event }> {
  protected changed(): void {
    this.dispatchEvent(new Event('change'));
  }
}
