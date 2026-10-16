// Addresses outside the app, in the desktop window (audit 2026-10-16, M23). The window is a WebView2 page, and what WebView2 does
// with a link to another site is its own default: nobody has run it, and it may open a window of the app, or take the app's own
// window to the site. So in the desktop window the page does not leave it to the default: a click on a link to another
// address, a window.open and a navigation to another origin are handed to the shell (shell.openExternal), which opens only web
// and mail addresses, in the person's own browser or mail program. This has not been run in WebView2.
//
// Three places, because an address can leave the page three ways:
// - a click on (or inside) a link, with the mouse, a modifier key, the middle button or the keyboard (click, auxclick);
// - window.open, which returns null (no window of the app is ever made);
// - a navigation to another origin that nothing above caught (the Navigation API's navigate event, which the router uses too).
// What is not a web or mail address and not the app's own (javascript:, file:, ms-msdt:, ...) is stopped and never sent: the core
// would refuse it, and a protocol handler of the system is exactly what must not be started by a link. The app's own addresses
// (its routes, #anchors), blob: and data: addresses (a saved file) and about: are left to the page.
//
// Outside the desktop window (a browser, the dev server, every Playwright run) nothing is installed and links behave as before.

import { isRecord } from '../api/guards.ts';
import { isWailsHost } from '../api/wails.ts';

/** What this module uses of a window: the real one, or a stand-in in a test. */
export interface LinkWindow {
  readonly document: Document;
  readonly location: Pick<Location, 'href' | 'origin'>;
  open: Window['open'];
  /** The Navigation API; missing in a browser without it (the click and open parts then work alone). */
  readonly navigation?: Navigation;
}

/** Hands an address to the shell (Client.shellOpenExternal). */
export type OpenExternal = (url: string) => Promise<unknown>;

/** What an address is to the page it is opened from. */
export type Address =
  /** A web or mail address outside the app: the shell opens it (`href` is the normalized address). */
  | { kind: 'external'; href: string }
  /** The app's own (a route, an #anchor), or a saved file (blob:, data:) or about:blank: not this module's business. */
  | { kind: 'own' }
  /** Anything else, or not an address at all: stopped, never sent. */
  | { kind: 'refused' };

/** Schemes of addresses the page itself makes and uses for files it offers: left alone. */
const OWN_SCHEMES = new Set(['blob:', 'data:', 'about:']);

/** What `raw` is, as a link or window.open of a page at `base` would reach it. */
export function classifyAddress(raw: string, base: string, ownOrigin: string): Address {
  let u: URL;
  try {
    u = new URL(raw, base);
  } catch {
    return { kind: 'refused' };
  }
  if (u.protocol === 'http:' || u.protocol === 'https:') return u.origin === ownOrigin ? { kind: 'own' } : { kind: 'external', href: u.href };
  if (u.protocol === 'mailto:') return { kind: 'external', href: u.href };
  return OWN_SCHEMES.has(u.protocol) ? { kind: 'own' } : { kind: 'refused' };
}

const isElement = (t: EventTarget | null): t is Element => isRecord(t) && typeof t.closest === 'function';

/** The link a click happened on or inside, if any. */
function linkOf(target: EventTarget | null): Element | null {
  return isElement(target) ? target.closest('a[href], area[href]') : null;
}

/** Why a link was not opened, with no address in it (an address can carry a sign-in code). */
function describeRefusal(err: unknown): string {
  const kind = isRecord(err) && typeof err.kind === 'string' ? err.kind : 'error';
  return `A link was not opened in your browser (${kind}). The desktop shell refused it or could not be reached.`;
}

/** The windows this module is installed on, so a second call does not open every link twice. */
const installed = new WeakMap<object, () => void>();

/**
 * Hands addresses outside the app to the shell, when the page runs in the desktop window (a Wails host); otherwise does nothing.
 * Returns the function that takes it off again, or undefined where nothing was installed. Calling it again on the same window
 * installs nothing more and returns the first one's.
 */
export function installExternalLinks(win: LinkWindow, openExternal: OpenExternal): (() => void) | undefined {
  if (!isWailsHost(win)) return undefined;
  const already = installed.get(win);
  if (already) return already;

  const abort = new AbortController();
  const originalOpen = win.open;
  const refused = (err: unknown) => console.warn(describeRefusal(err));
  const send = (href: string) => {
    try {
      openExternal(href).catch(refused);
    } catch (err) {
      refused(err);
    }
  };
  const classify = (raw: string) => classifyAddress(raw, win.location.href, win.location.origin);

  // a click on a link: with the mouse, a modifier key or the keyboard (Enter on a focused link is a click too)
  const onLink = (e: MouseEvent) => {
    const a = linkOf(e.target);
    const raw = a?.getAttribute('href');
    if (raw === null || raw === undefined) return;
    const addr = classify(raw);
    if (addr.kind === 'own') return;
    e.preventDefault();
    if (addr.kind === 'external') send(addr.href);
  };
  win.document.addEventListener('click', onLink, { capture: true, signal: abort.signal });
  // the middle button opens a link in a new window of a browser; the other buttons (the context menu) open nothing
  win.document.addEventListener('auxclick', (e) => (e.button === 1 ? onLink(e) : undefined), { capture: true, signal: abort.signal });

  // window.open never makes a window of the app: a web or mail address goes to the shell, everything else is ignored
  win.open = (url) => {
    const addr = classify(url === undefined ? 'about:blank' : String(url));
    if (addr.kind === 'external') send(addr.href);
    return null;
  };

  // a navigation to another origin that no click caught (location.href = ..., a script, a redirect)
  win.navigation?.addEventListener(
    'navigate',
    (e) => {
      if (!e.cancelable || e.hashChange || e.destination.sameDocument || e.downloadRequest !== null) return;
      const addr = classify(e.destination.url);
      if (addr.kind === 'own') return;
      e.preventDefault();
      if (addr.kind === 'external') send(addr.href);
    },
    { signal: abort.signal },
  );

  const uninstall = () => {
    abort.abort();
    win.open = originalOpen;
    installed.delete(win);
  };
  installed.set(win, uninstall);
  return uninstall;
}
