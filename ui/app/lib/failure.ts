// What a person sees when Drydock itself fails (audit 2026-10-16, H11 and L42). Plain DOM, no framework and no stylesheet: the
// framework or the tokens may be the thing that broke. System colours keep it readable in a light and a dark window.

import { visible } from './visible.ts';

/** An error as one line of text: "TypeError: x is undefined". Characters that are not drawn are written out; at most 2,000 characters. */
export function errorText(error: unknown): string {
  let text: string;
  if (error === undefined || error === null) text = '';
  else if (error instanceof Error) text = error.name && error.name !== 'Error' ? `${error.name}: ${error.message}` : error.message || error.name;
  else if (typeof error === 'string') text = error;
  else {
    try {
      text = JSON.stringify(error) ?? String(error);
    } catch {
      text = String(error);
    }
  }
  text = visible(text.trim() || 'An error without a message');
  return text.length > 2000 ? `${text.slice(0, 1999)}…` : text;
}

/** An abort (a request or a navigation that was cancelled on purpose) is not a failure. */
export function isAbort(error: unknown): boolean {
  return typeof error === 'object' && error !== null && (error as { name?: unknown }).name === 'AbortError';
}

export interface FailureOptions {
  /** What the Reload button does; the page is reloaded by default. */
  reload?: () => void;
}

const BOOT_ID = 'drydock-boot-failure';
const BANNER_ID = 'drydock-runtime-failure';

function el<K extends keyof HTMLElementTagNameMap>(doc: Document, tag: K, style: string, text?: string): HTMLElementTagNameMap[K] {
  const node = doc.createElement(tag);
  node.setAttribute('style', style);
  if (text !== undefined) node.textContent = text;
  return node;
}

const BUTTON = 'font:inherit;font-weight:600;padding:6px 16px;border:1px solid GrayText;border-radius:999px;background:ButtonFace;color:ButtonText;cursor:pointer;';
const PRE = 'margin:0 0 16px;padding:12px;border:1px solid GrayText;border-radius:8px;white-space:pre-wrap;overflow-wrap:anywhere;font:13px/1.5 ui-monospace,Consolas,monospace;';

function defaultReload(doc: Document): void {
  doc.defaultView?.location.reload();
}

/**
 * The page for a Drydock that could not start: it covers the window with what failed (the error's text) and a Reload button.
 * Calling it again replaces the text. Returns the page.
 */
export function showBootFailure(doc: Document, error: unknown, opts: FailureOptions = {}): HTMLElement {
  doc.getElementById(BOOT_ID)?.remove();
  doc.getElementById(BANNER_ID)?.remove(); // the page says it all
  const page = el(doc, 'div', 'position:fixed;inset:0;z-index:2147483647;overflow:auto;box-sizing:border-box;padding:10vh 24px;background:Canvas;color:CanvasText;color-scheme:light dark;font:15px/1.5 system-ui,sans-serif;');
  page.id = BOOT_ID;
  page.setAttribute('role', 'alert');
  const inner = el(doc, 'div', 'max-width:640px;margin:0 auto;');
  const text = el(doc, 'pre', PRE, errorText(error));
  text.setAttribute('data-testid', 'boot-failure-text');
  const reload = el(doc, 'button', BUTTON, 'Reload');
  reload.setAttribute('type', 'button');
  reload.addEventListener('click', () => (opts.reload ?? (() => defaultReload(doc)))());
  inner.append(
    el(doc, 'h1', 'margin:0 0 8px;font-size:22px;font-weight:700;', 'Drydock could not start'),
    el(doc, 'p', 'margin:0 0 16px;', 'Nothing was changed. Reload to try again; if it keeps happening, this is why:'),
    text,
    reload,
  );
  page.append(inner);
  doc.body.append(page);
  return page;
}

/**
 * Runs the start-up and, if it fails, shows the failure page instead of leaving a blank window. Resolves to whether it started.
 * The error goes to the console too.
 */
export async function startGuarded(doc: Document, start: () => Promise<void>, opts: FailureOptions = {}): Promise<boolean> {
  try {
    await start();
    return true;
  } catch (err) {
    console.error('Drydock could not start:', err);
    showBootFailure(doc, err, opts);
    return false;
  }
}

/**
 * A bar at the foot of the window for an error the running app did not handle (the framework reports one when a component
 * fails where no region boundary caught it): the error's text, Reload, and Dismiss. One bar: a second error replaces its text.
 */
export function showRuntimeFailure(doc: Document, error: unknown, opts: FailureOptions = {}): HTMLElement {
  let bar = doc.getElementById(BANNER_ID);
  if (!bar) {
    bar = el(doc, 'div', 'position:fixed;left:12px;right:12px;bottom:12px;z-index:2147483646;display:flex;flex-wrap:wrap;align-items:center;gap:8px 12px;box-sizing:border-box;max-width:720px;padding:10px 14px;border:1px solid GrayText;border-radius:12px;background:Canvas;color:CanvasText;color-scheme:light dark;box-shadow:0 4px 24px rgb(0 0 0 / 0.25);font:14px/1.45 system-ui,sans-serif;');
    bar.id = BANNER_ID;
    bar.setAttribute('role', 'alert');
    const msg = el(doc, 'div', 'flex:1 1 280px;min-width:0;');
    msg.append(el(doc, 'strong', '', 'Drydock hit an error while drawing the window. '), el(doc, 'span', 'overflow-wrap:anywhere;'));
    msg.lastElementChild?.setAttribute('data-testid', 'runtime-failure-text');
    const reload = el(doc, 'button', BUTTON, 'Reload');
    reload.setAttribute('type', 'button');
    reload.addEventListener('click', () => (opts.reload ?? (() => defaultReload(doc)))());
    const dismiss = el(doc, 'button', BUTTON, 'Dismiss');
    dismiss.setAttribute('type', 'button');
    dismiss.addEventListener('click', () => bar?.remove());
    bar.append(msg, reload, dismiss);
  }
  const span = bar.querySelector('[data-testid="runtime-failure-text"]');
  if (span) span.textContent = errorText(error);
  if (!bar.isConnected) doc.body.append(bar);
  return bar;
}
