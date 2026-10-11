// Component test helpers (browser runner). Components render inside Services built from TEST DOUBLES: a partial client
// whose methods the test supplies, a FakeTransport behind the event store, fresh UI and workspace models that never touch
// localStorage. Nothing here imitates an agent.

import { render as rmxRender, type RenderResult } from 'remix/component/test';
import type { RemixNode } from 'remix/component';
import type { Client } from '../api/client.ts';
import type { DrydockEvent } from '../api/types.ts';
import { EventStore } from '../state/eventStore.ts';
import { Services, type AppServices } from '../state/services.tsx';
import { ProjectData } from '../state/project.ts';
import { UiModel } from '../state/ui.ts';
import { Workspace } from '../state/workspace.ts';
import { FakeTransport } from './fakes.ts';

export interface TestServices extends AppServices {
  transport: FakeTransport;
  /** Deliver an event as if the core had persisted it. */
  emit(e: Partial<DrydockEvent> & { type: string }): void;
}

let seq = 1000;

/** Services around a partial client (the methods the component under test calls). */
export function testServices(client: Partial<Client> = {}): TestServices {
  const transport = new FakeTransport();
  const store = new EventStore(transport);
  const c = client as Client;
  const workspace = new Workspace(c, store, { persist: false });
  return {
    client: c,
    store,
    transportName: 'fake',
    ui: new UiModel({ persist: false }),
    workspace,
    project: new ProjectData(c, store, workspace),
    transport,
    emit(e) {
      store.ingest({ seq: ++seq, time: new Date().toISOString(), ...e } as DrydockEvent);
    },
  };
}

const mounted: RenderResult[] = [];

/** Render inside Services. Call cleanup() in afterEach. */
export function mount(node: RemixNode, s: AppServices = testServices()): RenderResult & { services: AppServices } {
  const r = rmxRender(<Services value={s}>{node}</Services>);
  mounted.push(r);
  return Object.assign(r, { services: s });
}

/** Plain render (no services), tracked for cleanup. */
export function renderPlain(node: RemixNode): RenderResult {
  const r = rmxRender(node);
  mounted.push(r);
  return r;
}

export function cleanup(): void {
  for (const r of mounted.splice(0)) r.cleanup();
}

/**
 * The real timer, taken before any test can install fake timers (t.useFakeTimers()): waitFor and drain keep working while
 * the component under test runs on fake time.
 */
const realSetTimeout: typeof setTimeout = globalThis.setTimeout.bind(globalThis);

/**
 * Let every pending promise continuation run: resolves on the next macrotask, after the microtask queue is empty. Not a
 * sleep (no amount of time is waited); use it to assert that nothing followed from a step whose follow-up, if any, would be
 * promise-driven (an async test double's answer, a re-render). Code driven by timers needs fake timers instead.
 */
export function drain(): Promise<void> {
  return new Promise((res) => realSetTimeout(res, 0));
}

/** Retry `check` (flushing the root between tries) until it stops throwing or the timeout passes. */
export async function waitFor<T>(check: () => T, r?: { root: { flush(): void } }, timeoutMs = 2000): Promise<T> {
  const start = Date.now();
  let last: unknown;
  for (;;) {
    try {
      r?.root.flush();
      return check();
    } catch (e) {
      last = e;
    }
    if (Date.now() - start > timeoutMs) throw last;
    await new Promise((res) => realSetTimeout(res, 10));
  }
}

/** Set an input's value the way typing does (fires input and change). */
export function type(el: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string): void {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : el instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(el, value);
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
}

/** Find a button (or link) by its accessible text. */
export function byText<T extends HTMLElement = HTMLElement>(root: ParentNode, selector: string, text: string | RegExp): T {
  const all = Array.from(root.querySelectorAll<T>(selector));
  const hit = all.find((el) => {
    const s = (el.getAttribute('aria-label') ?? el.textContent ?? '').trim();
    return typeof text === 'string' ? s === text : text.test(s);
  });
  if (!hit) throw new Error(`no ${selector} with text ${String(text)} (have: ${all.map((e) => JSON.stringify((e.getAttribute('aria-label') ?? e.textContent ?? '').trim())).join(', ')})`);
  return hit;
}

/** Find a form control by its label text (label[for] or wrapping label or aria-label). */
export function byLabel<T extends HTMLElement = HTMLInputElement>(root: ParentNode, text: string | RegExp): T {
  const match = (s: string | null | undefined) => {
    const v = (s ?? '').trim();
    return typeof text === 'string' ? v === text : text.test(v);
  };
  for (const l of Array.from(root.querySelectorAll('label'))) {
    const own = Array.from(l.childNodes)
      .filter((n) => n.nodeType === Node.TEXT_NODE || (n instanceof HTMLElement && !n.matches('input, select, textarea, button')))
      .map((n) => n.textContent)
      .join('');
    if (match(own) || match(l.textContent)) {
      const f = l.htmlFor ? (root as Document | Element).querySelector<T>(`#${CSS.escape(l.htmlFor)}`) : l.querySelector<T>('input, select, textarea, button');
      if (f) return f;
    }
  }
  const aria = Array.from(root.querySelectorAll<T>('[aria-label]')).find((e) => match(e.getAttribute('aria-label')));
  if (aria) return aria;
  throw new Error(`no control labelled ${String(text)}`);
}
