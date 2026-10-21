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
import { UiModel, wideWindow } from '../state/ui.ts';
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
    // a wide window: what a component draws does not depend on the size of the runner's window (a test of a narrow one says so)
    ui: new UiModel({ persist: false, media: wideWindow }),
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
export function type(el: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement | HTMLElement, value: string): void {
  if (isDropdown(el)) return choose(el, value);
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : el instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(el, value);
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
}

/**
 * Type text the way a person does, one key at a time. Each key appends one character to what the field shows now, fires
 * `input`, then lets the microtasks and the render that follow run before the next key. After every key the field must
 * still show everything typed so far, or this throws: a controlled field whose handler only assigns a variable is put back to
 * its old value by the runtime (it never re-rendered), and `type()`, which sets the whole text in one go, can not see that.
 * Pass the render result so a render the handler asked for is applied before the check. A field whose whole text is selected
 * (a rename box that selects itself when it opens) has that text replaced by the first key, as in a browser.
 */
export async function typeKeys(el: HTMLInputElement | HTMLTextAreaElement, text: string, r?: { root: { flush(): void } }): Promise<void> {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  const setValue = Object.getOwnPropertyDescriptor(proto, 'value')!.set!;
  let replace = el.value !== '' && el.selectionStart === 0 && el.selectionEnd === el.value.length;
  let want = replace ? '' : el.value;
  for (const ch of Array.from(text)) {
    want += ch;
    setValue.call(el, replace ? ch : el.value + ch);
    replace = false;
    el.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: ch }));
    r?.root.flush();
    await drain();
    r?.root.flush();
    if (el.value !== want) throw new Error(`after typing ${JSON.stringify(ch)} the field shows ${JSON.stringify(el.value)}, not ${JSON.stringify(want)}`);
  }
}

/** The kit's Select (ui/select.tsx): a combobox button whose options live in the listbox it controls. */
const isDropdown = (el: Element): el is HTMLButtonElement => el instanceof HTMLButtonElement && el.getAttribute('role') === 'combobox';

/** The listbox of a kit Select. */
function listboxOf(el: HTMLElement): HTMLElement {
  const id = el.getAttribute('aria-controls');
  const list = id ? el.ownerDocument.getElementById(id) : null;
  if (!list) throw new Error(`the dropdown ${el.getAttribute('aria-label') ?? el.id} has no listbox`);
  return list;
}

/** One option of a dropdown (a native <select> or the kit's Select), as a test sees it. */
export interface DropdownOption {
  value: string;
  label: string;
  disabled: boolean;
}

/** Choose an option by value in a native <select> or the kit's Select (the option is clicked; it need not be open). */
export function choose(el: HTMLElement, value: string): void {
  if (el instanceof HTMLSelectElement) return type(el, value);
  if (!isDropdown(el)) throw new Error(`not a dropdown: <${el.tagName.toLowerCase()} role=${el.getAttribute('role')}>`);
  const opt = Array.from(listboxOf(el).querySelectorAll<HTMLElement>('[role="option"]')).find((o) => o.getAttribute('data-value') === value);
  if (!opt) throw new Error(`no option ${JSON.stringify(value)} in ${el.getAttribute('aria-label') ?? el.id} (have: ${optionsOf(el).map((o) => JSON.stringify(o.value)).join(', ')})`);
  opt.click();
}

/** The options of a native <select> or the kit's Select, in order. */
export function optionsOf(el: HTMLElement): DropdownOption[] {
  if (el instanceof HTMLSelectElement) return Array.from(el.options).map((o) => ({ value: o.value, label: (o.textContent ?? '').trim(), disabled: o.disabled }));
  if (!isDropdown(el)) throw new Error(`not a dropdown: <${el.tagName.toLowerCase()} role=${el.getAttribute('role')}>`);
  return Array.from(listboxOf(el).querySelectorAll<HTMLElement>('[role="option"]')).map((o) => ({
    value: o.getAttribute('data-value') ?? '',
    label: (o.textContent ?? '').trim(),
    disabled: o.getAttribute('aria-disabled') === 'true',
  }));
}

/** The chosen value of a native <select>, the kit's Select, or any other control. */
export function valueOf(el: HTMLElement): string {
  if (isDropdown(el)) return el.getAttribute('data-value') ?? '';
  return (el as HTMLInputElement).value;
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

/**
 * The error a Field shows under a control: the text of the alert the control's aria-describedby names, or null when the control
 * is not marked invalid or points at no alert. A control that is not `aria-invalid="true"` has no error, whatever else it points at.
 */
export function errorOf(el: HTMLElement): string | null {
  if (el.getAttribute('aria-invalid') !== 'true') return null;
  const note = el.ownerDocument.getElementById(el.getAttribute('aria-describedby') ?? '');
  return note && note.getAttribute('role') === 'alert' ? (note.textContent ?? '') : null;
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
