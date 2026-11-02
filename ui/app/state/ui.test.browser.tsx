import { expect } from 'remix/assert';
import { afterEach, beforeEach, describe, it } from 'remix/test';
import { DOCK_VIEWS_MAX, parseDetails, parseDock, parseDockViews, parseInboxTab, UiModel, wideWindow } from './ui.ts';

// What is in localStorage can be anything (audit 2026-10-16, L43): an older version's shape, another tool's, a hand edit. A
// value that is not what the UI expects is its default, and nothing else is trusted.

const DOCK = 'drydock.dock';
const DETAILS = 'drydock.details';
const INBOX = 'drydock.inbox';
const keep: Record<string, string | null> = {};
beforeEach(() => {
  for (const k of [DOCK, DETAILS, INBOX]) keep[k] = localStorage.getItem(k);
});
afterEach(() => {
  for (const k of [DOCK, DETAILS, INBOX]) {
    if (keep[k] === null) localStorage.removeItem(k);
    else localStorage.setItem(k, keep[k]!);
  }
});

describe('parseDock', () => {
  it('takes a well-formed entry as it is', () => {
    expect({ ...parseDock({ p1: { open: true, tab: 'logs', height: 400 } }) }).toEqual({ p1: { open: true, tab: 'logs', height: 400 } });
  });

  it('gives a bad field its default, field by field', () => {
    const got = parseDock({
      a: { open: 'yes', tab: 'bogus', height: 'tall' },
      b: { open: true, tab: 'terminal', height: NaN },
      c: { open: 1, tab: 7, height: null },
      d: { open: false, tab: 'activity' },
      e: {},
    });
    expect({ ...got }).toEqual({
      a: { open: false, tab: 'diff', height: 280 },
      b: { open: true, tab: 'terminal', height: 280 },
      c: { open: false, tab: 'diff', height: 280 },
      d: { open: false, tab: 'activity', height: 280 },
      e: { open: false, tab: 'diff', height: 280 },
    });
  });

  it('keeps the height in a range a dock can have', () => {
    expect({ ...parseDock({ a: { height: -50 }, b: { height: 1e9 }, c: { height: 333.4 } }) }).toEqual({
      a: { open: false, tab: 'diff', height: 120 },
      b: { open: false, tab: 'diff', height: 2400 },
      c: { open: false, tab: 'diff', height: 333 },
    });
  });

  it('leaves out what is not an entry, and what is not an object is no dock at all', () => {
    expect({ ...parseDock({ a: 'x', b: null, c: [1], d: 5, e: { open: true } }) }).toEqual({ e: { open: true, tab: 'diff', height: 280 } });
    for (const junk of [null, undefined, 'text', 12, true, [], [{ open: true }]]) expect(Object.keys(parseDock(junk))).toEqual([]);
  });

  it('has no prototype to collide with a project id', () => {
    const dock = parseDock(JSON.parse('{"constructor":{"open":true},"__proto__":{"open":true},"toString":{"tab":"logs"}}'));
    expect(Object.keys(dock).sort()).toEqual(['__proto__', 'constructor', 'toString']);
    expect(dock['constructor']).toEqual({ open: true, tab: 'diff', height: 280 });
    expect(({} as Record<string, unknown>)['open']).toBeUndefined(); // nothing leaked into Object.prototype
  });
});

describe('the dock remembers per task what it showed (X22)', () => {
  it('parses a stored view field by field, and keeps only the newest tasks', () => {
    expect({ ...parseDockViews({ t1: { live: true, file: 'a.go', log: 'ev:1' }, t2: { live: 'yes', file: 7, log: '' }, t3: 'x' }) }).toEqual({
      t1: { live: true, file: 'a.go', log: 'ev:1' },
      t2: {},
    });
    expect(parseDockViews('junk')).toBeUndefined();
    const many = Object.fromEntries(Array.from({ length: DOCK_VIEWS_MAX + 5 }, (_, i) => [`t${i}`, { file: 'f' }]));
    const kept = Object.keys(parseDockViews(many)!);
    expect(kept.length).toBe(DOCK_VIEWS_MAX);
    expect(kept[0]).toBe('t5');
    const p1 = parseDock({ p1: { open: true, views: { t1: { file: 'x.go' } } } }).p1!;
    expect({ ...p1.views }).toEqual({ t1: { file: 'x.go' } });
    expect(p1.open).toBe(true);
  });

  it('stores the choices with the dock state, per project, and drops the oldest task', () => {
    const a = new UiModel({ media: wideWindow });
    a.setProject('p1');
    a.setDock({ open: true, tab: 'diff' });
    a.setDockView('t1', { file: 'calc.go' });
    a.setDockView('t1', { live: false });
    a.setDockView('t2', { log: 'ev:3' });
    const b = new UiModel({ media: wideWindow });
    b.setProject('p1');
    expect(b.dockView('t1')).toEqual({ file: 'calc.go', live: false });
    expect(b.dockView('t2')).toEqual({ log: 'ev:3' });
    expect(b.dock.open).toBe(true);
    b.setProject('p2');
    expect(b.dockView('t1')).toEqual({});
    b.setProject('p1');
    for (let i = 0; i < DOCK_VIEWS_MAX; i++) b.setDockView(`n${i}`, { file: 'f' });
    expect(b.dockView('t1')).toEqual({});
    expect(b.dockView(`n${DOCK_VIEWS_MAX - 1}`)).toEqual({ file: 'f' });
  });
});

describe('parseDetails', () => {
  it('takes booleans and defaults everything else', () => {
    expect(parseDetails({ open: false, other: true })).toEqual({ open: false, other: true });
    expect(parseDetails({ open: 'no', other: 1 })).toEqual({ open: true, other: false });
    expect(parseDetails({ open: false })).toEqual({ open: false, other: false });
    for (const junk of [null, undefined, 'text', 3, [], [true]]) expect(parseDetails(junk)).toEqual({ open: true, other: false });
  });
});

describe('UiModel reads its stored state with those checks (L43)', () => {
  it('starts from the defaults when the stored text is not JSON, or JSON of the wrong shape', () => {
    for (const text of ['not json', '[]', 'null', '"string"', '42', '{"p1":"x"}', '{"p1":{"tab":"nope","height":"tall","open":"yes"}}']) {
      localStorage.setItem(DOCK, text);
      localStorage.setItem(DETAILS, text);
      const ui = new UiModel({ media: wideWindow });
      ui.setProject('p1');
      expect([text, ui.dock]).toEqual([text, { open: false, tab: 'diff', height: 280 }]);
      ui.detailsPage('conversation');
      expect([text, ui.detailsOpen]).toEqual([text, true]);
      ui.detailsPage('other');
      expect([text, ui.detailsOpen]).toEqual([text, false]);
    }
  });

  it('restores what it stored itself', () => {
    const a = new UiModel({ media: wideWindow });
    a.setProject('p1');
    a.setDock({ open: true, tab: 'logs', height: 350 });
    a.detailsPage('other');
    a.setDetails(true);
    const b = new UiModel({ media: wideWindow });
    b.setProject('p1');
    expect(b.dock).toEqual({ open: true, tab: 'logs', height: 350 });
    b.detailsPage('other');
    expect(b.detailsOpen).toBe(true);
  });
});

describe('a narrow window (P85)', () => {
  const narrow = (below: number) => (q: string) => ({ matches: Number(/max-width: (\d+)px/.exec(q)?.[1] ?? 0) >= below, addEventListener: () => {} });
  it('starts with the details panel closed, whatever was stored, and keeps the choice for the session only', () => {
    localStorage.setItem(DETAILS, JSON.stringify({ open: true, other: true }));
    const ui = new UiModel({ media: narrow(1000) });
    ui.detailsPage('conversation');
    expect(ui.narrowDetails).toBe(true);
    expect(ui.detailsOpen).toBe(false);
    ui.setDetails(true);
    expect(ui.detailsOpen).toBe(true);
    expect(JSON.parse(localStorage.getItem(DETAILS)!)).toEqual({ open: true, other: true }); // the wide window's choice is kept
  });
  it('opens and closes the drawer of a rail or the chat list', () => {
    const ui = new UiModel({ media: narrow(800) });
    expect(ui.narrowRails).toBe(true);
    expect(ui.drawerOpen).toBe(false);
    ui.setDrawer(true);
    expect(ui.drawerOpen).toBe(true);
    ui.setDrawer(false);
    expect(ui.drawerOpen).toBe(false);
  });
  it('is never narrow in a wide window', () => {
    const ui = new UiModel({ media: wideWindow });
    expect([ui.narrowDetails, ui.narrowRails]).toEqual([false, false]);
  });
});

describe('the Inbox tab is remembered (drydock.inbox)', () => {
  it('opens on the tab it was left on; a stored value that is not a tab is Needs you', () => {
    const a = new UiModel({ media: wideWindow });
    a.setInboxTab('done');
    expect(new UiModel({ media: wideWindow }).inboxTab).toBe('done');
    // a test's model (persist: false) neither reads nor writes it
    const quiet = new UiModel({ persist: false, media: wideWindow });
    expect(quiet.inboxTab).toBe('needs');
    quiet.setInboxTab('mine');
    expect(new UiModel({ media: wideWindow }).inboxTab).toBe('done');
    for (const raw of ['nope', 7, null, undefined, {}]) expect(parseInboxTab(raw)).toBe('needs');
    localStorage.setItem(INBOX, 'not json');
    expect(new UiModel({ media: wideWindow }).inboxTab).toBe('needs');
  });
});
