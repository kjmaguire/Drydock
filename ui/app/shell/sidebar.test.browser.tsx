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

  it("on a project's page shows the project: Home first, then Needs you, Tasks and the rest, and no workspace links", async () => {
    const r = mount(<Sidebar route={{ name: 'needs-you' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="needs-count"]')!.getAttribute('aria-label')).toBe('0 items need you'), r);
    const nav = Array.from(r.$$('[data-nav]')).map((e) => e.getAttribute('data-testid'));
    expect(nav.slice(0, 3)).toEqual(['nav-workspace', 'nav-needs-you', 'nav-tasks']);
    expect(r.$('[data-testid="nav-workspace"]')!.getAttribute('href')).toBe('/home');
    expect(r.$('[data-testid="nav-needs-you"]')!.getAttribute('aria-current')).toBe('page');
    for (const id of ['nav-library', 'nav-library-skills', 'nav-integrations', 'nav-connections', 'nav-security-scan', 'nav-providers', 'nav-routing', 'nav-backups', 'nav-data', 'nav-support', 'nav-license', 'nav-settings', 'nav-chats']) {
      expect([id, r.$(`[data-testid="${id}"]`)]).toEqual([id, null]);
    }
    // the Project group starts with Security, then the project's own Integrations; the project's own Skills page is under Knowledge
    expect(nav.indexOf('nav-security')).toBe(nav.indexOf('nav-project-integrations') - 1);
    expect(nav.indexOf('nav-project-integrations')).toBe(nav.indexOf('nav-milestones') - 1);
    expect(r.$('[data-testid="nav-security"]')!.textContent).toBe('Security');
    expect(nav).toContain('nav-skills');
    expect(r.$('button[aria-label="Switch project"]')!.textContent).toContain('gocalc');
    // the MCP servers page was merged into Integrations (decision P47): no entry of its own
    expect(nav).not.toContain('nav-mcp');
    expect(r.container.querySelector('a[href="/mcp"]')).toBeNull();
  });

  it('on a workspace page shows the workspace: Home, Chats, the projects, Library, Connect and App, and nothing of the project', async () => {
    const r = mount(<Sidebar route={{ name: 'workspace' }} />, svc());
    await waitFor(() => expect(r.$$('[data-testid="project-link"]')).toHaveLength(1), r);
    const nav = Array.from(r.$$('[data-nav]')).map((e) => e.getAttribute('data-testid'));
    expect(nav.slice(0, 2)).toEqual(['nav-workspace', 'nav-chats']);
    expect(r.$('[data-testid="nav-workspace"]')!.getAttribute('aria-current')).toBe('page');
    for (const id of ['nav-library-skills', 'nav-library', 'nav-integrations', 'nav-connections', 'nav-security-scan', 'nav-providers', 'nav-routing', 'nav-backups', 'nav-data', 'nav-support', 'nav-license', 'nav-settings', 'nav-project-new']) {
      expect([id, r.$(`[data-testid="${id}"]`) === null]).toEqual([id, false]);
    }
    // the workspace's Security scan (any folder, decision P61) is beside Home and Chats
    expect(nav.slice(0, 3)).toEqual(['nav-workspace', 'nav-chats', 'nav-security-scan']);
    expect(r.$('[data-testid="nav-security-scan"]')!.getAttribute('href')).toBe('/security-scan');
    for (const id of ['nav-needs-you', 'nav-tasks', 'nav-team', 'nav-skills', 'nav-project', 'nav-project-integrations']) {
      expect([id, r.$(`[data-testid="${id}"]`)]).toEqual([id, null]);
    }
    expect(links(r)).toHaveLength(0);
    expect(r.$('[aria-label="Inspect Backend #1"]')).toBeNull();
    expect(r.$('[data-testid="nav-library-skills"]')!.textContent).toBe('Skills');
    expect(r.$('[data-testid="project-link"]')!.textContent).toContain('gocalc');
    expect(r.$('button[aria-label="Switch project"]')!.textContent).toContain('Home');
  });

  it("has the project's own Integrations page in the Project group, marked current there, with the project's sidebar", async () => {
    const r = mount(<Sidebar route={{ name: 'project-integrations' }} />, svc());
    const link = await waitFor(() => {
      const l = r.$('[data-testid="nav-project-integrations"]');
      if (!l) throw new Error('not yet');
      return l as HTMLAnchorElement;
    }, r);
    expect(link.getAttribute('href')).toBe('/project/integrations');
    expect(link.getAttribute('aria-current')).toBe('page');
    expect(link.textContent).toBe('Integrations');
    const nav = Array.from(r.$$('[data-nav]')).map((e) => e.getAttribute('data-testid'));
    // in the Project group, right after Security
    expect(nav.indexOf('nav-project-integrations')).toBe(nav.indexOf('nav-security') + 1);
    // still the project's sidebar: no workspace links
    expect(r.$('[data-testid="nav-integrations"]')).toBeNull();
    expect(r.$('[data-testid="nav-tasks"]')).not.toBeNull();
  });

  it('has the Repo map in the Knowledge group, after Memory, marked current there', async () => {
    const r = mount(<Sidebar route={{ name: 'repo-map' }} />, svc());
    const link = await waitFor(() => {
      const l = r.$('[data-testid="nav-repo-map"]');
      if (!l) throw new Error('not yet');
      return l as HTMLAnchorElement;
    }, r);
    expect(link.getAttribute('href')).toBe('/repo-map');
    expect(link.getAttribute('aria-current')).toBe('page');
    expect(link.textContent).toBe('Repo map');
    const nav = Array.from(r.$$('[data-nav]')).map((e) => e.getAttribute('data-testid'));
    expect(nav.indexOf('nav-repo-map')).toBe(nav.indexOf('nav-memory') + 1);
  });

  it('marks Chats current on a chat, and Integrations on its alias /mcp', async () => {
    const r = mount(<Sidebar route={{ name: 'chat', id: 'c1' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="nav-chats"]')!.getAttribute('aria-current')).toBe('page'), r);
    cleanup();
    const m = mount(<Sidebar route={{ name: 'mcp' }} />, svc());
    await waitFor(() => expect(m.$('[data-testid="nav-integrations"]')).not.toBeNull(), m);
    expect(m.$('[data-testid="nav-integrations"]')!.getAttribute('aria-current')).toBe('page');
  });

  it('a project link selects that project and opens it', async () => {
    const s = svc({ projectList: async () => [{ id: 'p', name: 'gocalc' }, { id: 'q', name: 'billing' }] as never });
    // the link's own navigation is the app's; here it is stopped after the link's handler ran, so the test page stays
    const stop = (e: Event) => e.preventDefault();
    document.addEventListener('click', stop);
    try {
      const r = mount(<Sidebar route={{ name: 'workspace' }} />, s);
      await waitFor(() => expect(r.$$('[data-testid="project-link"]')).toHaveLength(2), r);
      const billing = Array.from(r.$$('[data-testid="project-link"]')).find((e) => e.textContent!.includes('billing')) as HTMLAnchorElement;
      expect(billing.getAttribute('href')).toBe('/');
      await r.act(() => billing.click());
      expect(s.workspace.projectId).toBe('q');
    } finally {
      document.removeEventListener('click', stop);
    }
  });

  it('without a project shows the workspace sidebar on every page', async () => {
    const r = mount(<Sidebar route={{ name: 'library' }} />, svc({ projectList: async () => [] }));
    await waitFor(() => expect(r.$('[data-testid="nav-library"]')!.getAttribute('aria-current')).toBe('page'), r);
    expect(r.$('[data-testid="nav-tasks"]')).toBeNull();
    expect(r.$$('[data-testid="project-link"]')).toHaveLength(0);
    expect(r.$('[data-testid="nav-project-new"]')).not.toBeNull();
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
