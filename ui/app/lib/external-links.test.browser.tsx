import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { drain } from '../test/render.tsx';
import { installExternalLinks, type LinkWindow } from './external-links.ts';

// audit 2026-10-16, M23: in the desktop window an address outside the app is handed to the shell. TEST DOUBLES only: a window
// stand-in around a document of its own (a detached one, so no click here can navigate the test page), a stand-in for the
// Navigation API, and a stand-in for Client.shellOpenExternal. Nothing here runs WebView2 or the Wails shell.

type FakeWindow = LinkWindow & { _wails?: unknown; opened: string[] };

/** A stand-in Navigation API: an event target the test fires navigate events on. */
function fakeNavigation(): Navigation {
  return new EventTarget() as Navigation;
}
function navigate(nav: Navigation, url: string, over: Partial<Record<'cancelable' | 'hashChange' | 'sameDocument' | 'downloadRequest', unknown>> = {}) {
  const e = new Event('navigate', { cancelable: over.cancelable !== false });
  Object.assign(e, { destination: { url, sameDocument: over.sameDocument === true }, hashChange: over.hashChange === true, downloadRequest: over.downloadRequest ?? null });
  nav.dispatchEvent(e);
  return e;
}

const cleanups: (() => void)[] = [];
afterEach(() => {
  for (const c of cleanups.splice(0)) c();
});

function setup(over: { host?: boolean; navigation?: boolean } = {}) {
  const doc = document.implementation.createHTMLDocument('app');
  const sent: string[] = [];
  const refusals: unknown[] = [];
  const nav = over.navigation === false ? undefined : fakeNavigation();
  const win: FakeWindow = {
    document: doc,
    location: { href: 'https://app.test/tasks/t1', origin: 'https://app.test' },
    open: () => {
      throw new Error('the page\'s own window.open ran');
    },
    navigation: nav,
    opened: sent,
    ...(over.host === false ? {} : { _wails: {} }),
  };
  const original = win.open;
  let reject: Error | undefined;
  const uninstall = installExternalLinks(win, async (url) => {
    sent.push(url);
    if (reject) throw reject;
  });
  if (uninstall) cleanups.push(uninstall);
  const link = (html: string): HTMLElement => {
    doc.body.innerHTML = html;
    return doc.body.firstElementChild as HTMLElement;
  };
  const click = (el: Element, init: MouseEventInit = {}, type = 'click') => {
    const e = new MouseEvent(type, { bubbles: true, cancelable: true, ...init });
    el.dispatchEvent(e);
    return e;
  };
  return {
    win,
    doc,
    nav,
    sent,
    original,
    uninstall,
    refusals,
    link,
    click,
    refuseWith: (e: Error) => (reject = e),
  };
}

describe('external links in the desktop window', () => {
  it('a click on a link to another site goes to the shell once and the window does not follow it', async () => {
    const t = setup();
    const a = t.link('<a href="https://example.com/docs?x=1" target="_blank" rel="noopener" data-rmx-document>docs</a>');
    const e = t.click(a);
    await drain();
    expect(t.sent).toEqual(['https://example.com/docs?x=1']);
    expect(e.defaultPrevented).toBe(true);
  });

  it('also a click inside the link, with a modifier key, the middle button, and a mail address', async () => {
    const t = setup();
    const a = t.link('<a href="https://example.com/a"><span><b>docs</b></span></a>');
    const inner = a.querySelector('b')!;
    expect(t.click(inner).defaultPrevented).toBe(true);
    expect(t.click(a, { ctrlKey: true }).defaultPrevented).toBe(true);
    expect(t.click(a, { metaKey: true }).defaultPrevented).toBe(true);
    expect(t.click(a, { shiftKey: true }).defaultPrevented).toBe(true);
    expect(t.click(a, { button: 1 }, 'auxclick').defaultPrevented).toBe(true);
    await drain();
    expect(t.sent).toEqual(Array(5).fill('https://example.com/a'));
    const mail = t.link('<a href="mailto:kyle@example.com">write</a>');
    expect(t.click(mail).defaultPrevented).toBe(true);
    expect(t.sent.at(-1)).toBe('mailto:kyle@example.com');
  });

  it('the right button opens nothing', async () => {
    const t = setup();
    const a = t.link('<a href="https://example.com/a">docs</a>');
    const e = t.click(a, { button: 2 }, 'auxclick');
    await drain();
    expect(t.sent).toEqual([]);
    expect(e.defaultPrevented).toBe(false);
  });

  it('the app\'s own routes, anchors and saved files are not touched', async () => {
    const t = setup();
    for (const href of ['/library', 'tasks/t2', '#main', 'https://app.test/room/r1?x#y', 'blob:https://app.test/0b9c1c8e-1c5e-4a40-9a2b-4f1d3a9b7f10']) {
      const a = t.link(`<a href="${href}" download="file.md">x</a>`);
      expect(t.click(a).defaultPrevented).toBe(false);
      expect(t.click(a, { ctrlKey: true }).defaultPrevented).toBe(false);
    }
    // an anchor without an address, and a click that is not on a link
    expect(t.click(t.link('<a name="x">no address</a>')).defaultPrevented).toBe(false);
    expect(t.click(t.link('<button>not a link</button>')).defaultPrevented).toBe(false);
    expect(t.click(t.doc.body).defaultPrevented).toBe(false);
    await drain();
    expect(t.sent).toEqual([]);
  });

  it('an address that is not a web or mail address is stopped and never sent', async () => {
    const t = setup();
    for (const href of ['javascript:alert(1)', 'file:///C:/Windows/System32/calc.exe', 'ms-msdt:/id PCWDiagnostic', 'ms-settings:privacy', 'ftp://example.com/x']) {
      const a = t.link(`<a href="${href}">x</a>`);
      expect(t.click(a).defaultPrevented).toBe(true);
      expect(t.click(a, { button: 1 }, 'auxclick').defaultPrevented).toBe(true);
    }
    await drain();
    expect(t.sent).toEqual([]);
  });

  it('window.open sends a web or mail address to the shell and returns null; every other address is ignored, also returning null', async () => {
    const t = setup();
    expect(t.win.open('https://example.com/a')).toBe(null);
    expect(t.win.open(new URL('https://example.com/b'))).toBe(null);
    expect(t.win.open('mailto:kyle@example.com', '_blank', 'noopener')).toBe(null);
    for (const ignored of ['javascript:alert(1)', 'file:///C:/x', 'ms-msdt:/id x', '/library', '#top', 'about:blank']) expect(t.win.open(ignored)).toBe(null);
    expect(t.win.open()).toBe(null);
    await drain();
    expect(t.sent).toEqual(['https://example.com/a', 'https://example.com/b', 'mailto:kyle@example.com']);
  });

  it('a navigation to another origin that no click caught is cancelled and sent to the shell', async () => {
    const t = setup();
    const nav = t.nav!;
    const away = navigate(nav, 'https://example.com/elsewhere');
    expect(away.defaultPrevented).toBe(true);
    const script = navigate(nav, 'file:///C:/Windows/System32/calc.exe');
    expect(script.defaultPrevented).toBe(true);
    await drain();
    expect(t.sent).toEqual(['https://example.com/elsewhere']);
  });

  it('a navigation inside the app is left to the router: its routes, a reload, an anchor, a download, one that cannot be cancelled', async () => {
    const t = setup();
    const nav = t.nav!;
    expect(navigate(nav, 'https://app.test/library').defaultPrevented).toBe(false);
    expect(navigate(nav, 'https://app.test/tasks/t1').defaultPrevented).toBe(false); // a reload
    expect(navigate(nav, 'https://app.test/tasks/t1#top', { hashChange: true, sameDocument: true }).defaultPrevented).toBe(false);
    expect(navigate(nav, 'blob:https://app.test/0b9c1c8e', { downloadRequest: 'notes.md' }).defaultPrevented).toBe(false);
    expect(navigate(nav, 'https://example.com/x', { sameDocument: true }).defaultPrevented).toBe(false);
    // an event that cannot be cancelled is not half-handled: the shell is not asked for something that will happen anyway
    expect(navigate(nav, 'https://example.com/y', { cancelable: false }).defaultPrevented).toBe(false);
    await drain();
    expect(t.sent).toEqual([]);
  });

  it('works without the Navigation API: clicks and window.open still go to the shell', async () => {
    const t = setup({ navigation: false });
    expect(t.click(t.link('<a href="https://example.com/a">x</a>')).defaultPrevented).toBe(true);
    expect(t.win.open('https://example.com/b')).toBe(null);
    await drain();
    expect(t.sent).toEqual(['https://example.com/a', 'https://example.com/b']);
  });

  it('a refusal from the shell is logged without the address, and nothing is opened by the window instead', async () => {
    const warned: unknown[][] = [];
    const warn = console.warn;
    console.warn = (...a: unknown[]) => void warned.push(a);
    try {
      const t = setup();
      const refusal = Object.assign(new Error('only http, https and mail addresses can be opened from here'), { kind: 'bad_request' });
      t.refuseWith(refusal);
      const e = t.click(t.link('<a href="https://secret.example.com/cb?code=SIGNINCODE123">x</a>'));
      await drain();
      expect(e.defaultPrevented).toBe(true);
      expect(warned).toHaveLength(1);
      const text = String(warned[0]![0]);
      expect(text).toContain('A link was not opened in your browser (bad_request)');
      expect(text).not.toContain('secret.example.com');
      expect(text).not.toContain('SIGNINCODE123');
    } finally {
      console.warn = warn;
    }
  });

  it('outside the desktop window nothing is installed: links, window.open and navigation behave as before', async () => {
    const t = setup({ host: false });
    expect(t.uninstall).toBeUndefined();
    expect(t.win.open).toBe(t.original);
    const a = t.link('<a href="https://example.com/a">x</a>');
    expect(t.click(a).defaultPrevented).toBe(false);
    expect(navigate(t.nav!, 'https://example.com/y').defaultPrevented).toBe(false);
    await drain();
    expect(t.sent).toEqual([]);
  });

  it('is installed once per window, and taking it off puts everything back', async () => {
    const t = setup();
    const second = installExternalLinks(t.win, async (url) => void t.sent.push(`second:${url}`));
    expect(second).toBe(t.uninstall); // nothing more is installed: a link is not opened twice
    t.click(t.link('<a href="https://example.com/a">x</a>'));
    await drain();
    expect(t.sent).toEqual(['https://example.com/a']);
    t.uninstall!();
    expect(t.win.open).toBe(t.original);
    expect(t.click(t.link('<a href="https://example.com/a">x</a>')).defaultPrevented).toBe(false);
    expect(navigate(t.nav!, 'https://example.com/y').defaultPrevented).toBe(false);
    expect(t.sent).toEqual(['https://example.com/a']);
  });
});
