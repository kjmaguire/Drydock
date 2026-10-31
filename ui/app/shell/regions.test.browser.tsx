// The region focus model and the keys between rooms and chats (decision P132).
import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Room } from '../api/types.ts';
import { useBindings } from '../lib/keys.ts';
import { lic } from '../test/fixtures.ts';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { adapterInfo, CATALOG, prebuilt } from '../screens/project/wizard-fixtures.ts';
import { chatOrder, focusRegion, neighbour, roomOrder } from './regions.ts';
import { Shell } from './shell.tsx';

afterEach(() => {
  useBindings({});
  cleanup();
});

const press = (target: EventTarget, key: string, mods: KeyboardEventInit = {}) => {
  const e = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...mods });
  target.dispatchEvent(e);
  return e;
};

const room = (id: string, kind: Room['kind'], archived = false): Room =>
  ({ id, projectId: 'p1', kind, name: id, mode: 'normal', archived, createdAt: '2026-10-30T00:00:00Z', version: 1 }) as Room;

function shellServices(extra: Record<string, unknown> = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'p1', name: 'gocalc', hasRepo: true }] as never,
    licenseStatus: async () => lic(),
    appInfo: async () => ({}) as never,
    githubStatus: async () => ({ installed: false, loggedIn: false, login: '', error: 'gh is not installed' }),
    agentCatalogList: async () => CATALOG.map((e) => prebuilt(e.id, { codex: false })),
    adapterList: async () => [adapterInfo('claude', true, ['opus'])],
    agentTemplateList: async () => [],
    mcpLibList: async () => [],
    mcpCatalog: async () => [],
    chatList: async () => ({ chats: [], hasMore: false }) as never,
    ...extra,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

describe('regions and neighbours (decision P132)', () => {
  it('lists the rooms as the Rooms rail does and finds the one above or below', () => {
    const rooms = [room('dm1', 'dm'), room('general', 'channel'), room('t1', 'task'), room('old', 'channel', true), room('design', 'channel')];
    expect(roomOrder(rooms).map((r) => r.id)).toEqual(['general', 'design', 't1', 'dm1']);
    const order = roomOrder(rooms);
    expect([neighbour(order, 'design', 1)?.id, neighbour(order, 'design', -1)?.id, neighbour(order, 'general', -1), neighbour(order, 'old', 1)]).toEqual([
      't1',
      'general',
      undefined,
      undefined,
    ]);
  });

  it('moves the keyboard into a region: the current link, the chosen tab, the page itself; nothing when it is not there', () => {
    const root = document.createElement('div');
    root.innerHTML =
      '<aside data-region="sidebar"><a href="/a">A</a><a href="/b" aria-current="page">B</a></aside>' +
      '<main data-region="main" tabindex="-1"></main>' +
      '<section data-region="dock"><button role="tab" aria-selected="false">Terminal</button><button role="tab" aria-selected="true">Diff</button></section>';
    document.body.append(root);
    expect(focusRegion('sidebar', root)).toBe(true);
    expect(document.activeElement?.textContent).toBe('B');
    expect(focusRegion('dock', root)).toBe(true);
    expect(document.activeElement?.textContent).toBe('Diff');
    expect(focusRegion('main', root)).toBe(true);
    expect(document.activeElement?.tagName).toBe('MAIN');
    expect(focusRegion('details', root)).toBe(false);
    const rows = document.createElement('ul');
    rows.innerHTML = '<li data-testid="chat-row" data-chat-id="c2"></li><li data-testid="chat-row" data-chat-id="c1"></li>';
    root.append(rows);
    expect(chatOrder(root).map((c) => c.id)).toEqual(['c2', 'c1']);
    root.remove();
  });

  it('Ctrl+1 to Ctrl+4 move between the shell\'s regions; Ctrl+` shows and hides the terminal; none inside the terminal', async () => {
    const s = shellServices();
    const r = mount(<Shell url={new URL('http://localhost/tasks')}>{null}</Shell>, s);
    await waitFor(() => expect(s.workspace.projectId).toBe('p1'), r);
    r.root.flush();
    expect(press(document.body, '1', { ctrlKey: true, code: 'Digit1' }).defaultPrevented).toBe(true);
    expect(document.activeElement?.closest('[data-region="sidebar"]')).not.toBe(null);
    expect(press(document.body, '2', { ctrlKey: true, code: 'Digit2' }).defaultPrevented).toBe(true);
    expect(document.activeElement?.id).toBe('main');
    expect(press(document.body, '3', { ctrlKey: true, code: 'Digit3' }).defaultPrevented).toBe(true);
    expect(document.activeElement?.closest('[data-region="details"]')).not.toBe(null);
    expect(press(document.body, '4', { ctrlKey: true, code: 'Digit4' }).defaultPrevented).toBe(true);
    expect(document.activeElement?.closest('[data-region="dock"]')).not.toBe(null);

    const term = document.createElement('div');
    term.className = 'xterm';
    r.container.append(term);
    expect(press(term, '`', { ctrlKey: true, code: 'Backquote' }).defaultPrevented).toBe(false);
    expect(press(term, '1', { ctrlKey: true, code: 'Digit1' }).defaultPrevented).toBe(false);
    expect(s.ui.dock.open).toBe(false);
    term.remove();
    expect(press(document.body, '`', { ctrlKey: true, code: 'Backquote' }).defaultPrevented).toBe(true);
    expect([s.ui.dock.open, s.ui.dock.tab]).toEqual([true, 'terminal']);
    press(document.body, '`', { ctrlKey: true, code: 'Backquote' });
    expect(s.ui.dock.open).toBe(false);
    // on another tab, Ctrl+` brings the terminal forward rather than closing the dock
    s.ui.openDock('diff');
    press(document.body, '`', { ctrlKey: true, code: 'Backquote' });
    expect([s.ui.dock.open, s.ui.dock.tab]).toEqual([true, 'terminal']);
  });

  it('Ctrl+Shift+K opens the Inbox; Alt+Down and Alt+Up go to the next and previous room; the bindings the core keeps are used', async () => {
    const went: string[] = [];
    const s = shellServices({
      roomList: async () => [room('general', 'channel'), room('design', 'channel'), room('dm1', 'dm')],
      keysGet: async () => ({ bindings: { 'room-next': 'Alt+N' } }),
    });
    const r = mount(<Shell url={new URL('http://localhost/room/design')} onNavigate={(h) => void went.push(h)}>
        {null}
      </Shell>, s);
    await waitFor(() => expect(s.project.roomList.length).toBe(3), r);
    r.root.flush();
    await waitFor(() => expect(press(document.body, 'n', { altKey: true }).defaultPrevented).toBe(true), r);
    expect(press(document.body, 'ArrowDown', { altKey: true }).defaultPrevented).toBe(false); // bound elsewhere now
    expect(press(document.body, 'ArrowUp', { altKey: true }).defaultPrevented).toBe(true);
    expect(press(document.body, 'K', { ctrlKey: true, shiftKey: true }).defaultPrevented).toBe(true);
    expect(went).toEqual(['/room/dm1', '/room/general', '/needs-you']);
  });
});
