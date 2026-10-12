import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../api/client.ts';
import type { Room } from '../api/types.ts';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { Sidebar } from './sidebar.tsx';

afterEach(cleanup);

const room = (over: Partial<Room>): Room => ({ id: 'r1', projectId: 'p', kind: 'channel', name: 'general', mode: 'auto', archived: false, createdAt: '', version: 1, ...over });

function svc(over: Partial<Client> = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'p', name: 'gocalc' }] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    agentList: async () => [{ id: 'agt1', projectId: 'p', name: 'Backend #1', adapter: 'claude', role: 'backend', model: '', effort: '', providerId: '', permissionMode: '', status: '' }],
    roomList: async () => [room({}), room({ id: 'r2', kind: 'task', name: 'task-3-subtract', taskId: 't3' })],
    roomUnread: async () => [{ roomId: 'r1', roomName: 'general', kind: 'channel', unread: 3, mentions: 0, muted: false, lastReadSeq: 0 }],
    presenceList: async () => [],
    taskList: async () => [],
    needsYouList: async () => [],
    ...over,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

const links = (r: { $$: (s: string) => NodeListOf<Element> }) => Array.from(r.$$('[data-testid="room-link"]')) as HTMLAnchorElement[];

describe('the sidebar', () => {
  it('starts with the Drydock mark and the project switcher (no project rail)', async () => {
    const r = mount(<Sidebar route={{ name: 'home' }} />, svc());
    await waitFor(() => expect(r.$('button[aria-label="Switch project"]')!.textContent).toContain('gocalc'), r);
    expect(r.$('[role="img"][aria-label="Drydock"]')).not.toBeNull();
    expect(r.$('[role="radiogroup"]')).toBeNull();
  });

  it('names rooms without a "#": an icon, then the name, then a quiet unread count', async () => {
    const r = mount(<Sidebar route={{ name: 'home' }} />, svc());
    await waitFor(() => expect(links(r)).toHaveLength(2), r);
    const [general, task] = links(r);
    expect(general!.textContent!.includes('#')).toBe(false);
    expect(general!.querySelector('.label')!.textContent).toBe('general');
    expect(general!.querySelector('.room-icon svg')!.getAttribute('aria-hidden')).toBe('true');
    await waitFor(() => expect(general!.getAttribute('data-unread')).toBe('true'), r);
    // the number is drawn once and read once ("general, 3 unread")
    const count = general!.querySelector('[data-testid="unread-count"]')!;
    expect(count.querySelector('[aria-hidden="true"]')!.textContent).toBe('3');
    expect(count.querySelector('.sr-only')!.textContent).toBe(', 3 unread');
    expect(task!.querySelector('.label')!.textContent).toBe('task-3-subtract');
    expect(task!.querySelector('[data-testid="unread-count"]')).toBeNull();
  });

  it('offers Chats after the quick links with a project, and alone without one', async () => {
    const r = mount(<Sidebar route={{ name: 'chat', id: 'c1' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="nav-needs-you"]')).not.toBeNull(), r);
    const nav = Array.from(r.$$('[data-nav]')).map((e) => e.getAttribute('data-testid'));
    expect(nav.indexOf('nav-chats')).toBe(nav.indexOf('nav-activity') + 1);
    expect(nav[0]).toBe('nav-needs-you');
    expect(r.$('[data-testid="nav-chats"]')!.getAttribute('aria-current')).toBe('page');
    cleanup();
    const none = mount(<Sidebar route={{ name: 'chats' }} />, svc({ projectList: async () => [] }));
    await waitFor(() => expect(none.$('[data-testid="nav-chats"]')).not.toBeNull(), none);
    expect(none.$('[data-testid="nav-needs-you"]')).toBeNull();
  });

  it('keeps the needs-you count and the workspace links', async () => {
    const r = mount(<Sidebar route={{ name: 'home' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="needs-count"]')!.getAttribute('aria-label')).toBe('0 items need you'), r);
    for (const id of ['nav-library', 'nav-integrations', 'nav-connections', 'nav-providers', 'nav-routing', 'nav-backups', 'nav-data', 'nav-support', 'nav-license', 'nav-settings']) {
      expect(r.$(`[data-testid="${id}"]`)).not.toBeNull();
    }
    // the Project group starts with Security
    const nav = Array.from(r.$$('[data-nav]')).map((e) => e.getAttribute('data-testid'));
    expect(nav.indexOf('nav-security')).toBe(nav.indexOf('nav-milestones') - 1);
    expect(r.$('[data-testid="nav-security"]')!.textContent).toBe('Security');
  });

  it('without a project still shows the switcher and the workspace links', async () => {
    const r = mount(<Sidebar route={{ name: 'library' }} />, svc({ projectList: async () => [] }));
    await waitFor(() => expect(r.$('button[aria-label="Switch project"]')!.textContent).toContain('No project'), r);
    expect(r.$('[data-testid="nav-library"]')!.getAttribute('aria-current')).toBe('page');
    expect(r.$('[data-testid="nav-tasks"]')).toBeNull();
  });

  it('moves between links with the arrow keys, but leaves keys from the switcher alone', async () => {
    const r = mount(<Sidebar route={{ name: 'home' }} />, svc());
    await waitFor(() => expect(links(r)).toHaveLength(2), r);
    const first = r.$('[data-nav]') as HTMLElement;
    first.focus();
    await r.act(() => first.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true })));
    expect(document.activeElement).toBe(r.$$('[data-nav]')[1]!);
    const sw = r.$('button[aria-label="Switch project"]') as HTMLButtonElement;
    sw.focus();
    await r.act(() => sw.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true, cancelable: true })));
    expect(document.activeElement).toBe(sw);
  });
});
