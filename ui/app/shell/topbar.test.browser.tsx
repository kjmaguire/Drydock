import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../api/client.ts';
import type { Room } from '../api/types.ts';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { RoomsRail, SettingsRail, SubNav } from './hubnav.tsx';
import { TopBar } from './topbar.tsx';

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

const ids = (r: { $$: (s: string) => NodeListOf<Element> }) => Array.from(r.$$('[data-nav]')).map((e) => e.getAttribute('data-testid'));

describe('the top bar', () => {
  it("on a project's page: the mark (the way home), the switcher, the five tabs, search, the bell with its count, the gear, the stream", async () => {
    const r = mount(<TopBar route={{ name: 'needs-you' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="needs-count"]')!.getAttribute('aria-label')).toBe('0 items need you'), r);
    expect(r.$('nav[aria-label="Main"]')).not.toBeNull();
    expect(r.$('[role="img"][aria-label="Drydock"]')).not.toBeNull();
    expect(ids(r)).toEqual(['nav-workspace', 'nav-overview', 'nav-tasks', 'nav-rooms', 'nav-team', 'nav-knowledge', null, 'nav-needs-you', 'nav-settings']);
    expect(r.$('[data-testid="nav-workspace"]')!.getAttribute('href')).toBe('/home');
    expect(r.$('[data-testid="nav-rooms"]')!.getAttribute('href')).toBe('/rooms');
    expect(r.$('[data-testid="nav-knowledge"]')!.getAttribute('href')).toBe('/knowledge');
    expect(r.$('[data-testid="nav-team"]')!.textContent).toBe('Crew');
    // Needs you is the bell: current on its page, and its count is read once
    expect(r.$('[data-testid="nav-needs-you"]')!.getAttribute('aria-current')).toBe('page');
    expect(r.$('[data-testid="nav-needs-you"]')!.getAttribute('href')).toBe('/needs-you');
    // the bell's page belongs to no tab: only the bell is current
    expect(r.$('[data-testid="nav-overview"]')!.getAttribute('aria-current')).toBeNull();
    expect(r.$$('[aria-current="page"]')).toHaveLength(1);
    expect(r.$('button[aria-label="Switch project"]')!.textContent).toContain('gocalc');
    expect(r.$('[data-testid="stream"]')).not.toBeNull();
    for (const id of ['nav-library', 'nav-chats', 'nav-security-scan']) expect([id, r.$(`[data-testid="${id}"]`)]).toEqual([id, null]);
  });

  it('marks the tab of each hub current: Tasks on a task and on Pull requests, Knowledge on the repo map, the gear on every settings page', async () => {
    for (const [route, tab] of [
      [{ name: 'task', id: 't1' }, 'nav-tasks'],
      [{ name: 'pulls' }, 'nav-tasks'],
      [{ name: 'repo-map' }, 'nav-knowledge'],
      [{ name: 'room', id: 'r1' }, 'nav-rooms'],
      [{ name: 'team' }, 'nav-team'],
      [{ name: 'agent', id: 'agt1' }, 'nav-team'],
      [{ name: 'activity' }, 'nav-overview'],
      // a project's settings are the project's own: Overview is current, not the gear
      [{ name: 'project' }, 'nav-overview'],
      [{ name: 'project-integrations' }, 'nav-overview'],
      [{ name: 'connections' }, 'nav-settings'],
      [{ name: 'github' }, 'nav-settings'],
    ] as const) {
      const r = mount(<TopBar route={route} />, svc());
      await waitFor(() => expect(r.$(`[data-testid="${tab}"]`)!.getAttribute('aria-current')).toBe('page'), r);
      expect(r.$$('[aria-current="page"]')).toHaveLength(1);
      cleanup();
    }
  });

  it('on a workspace page shows the workspace: Home, Chats, Library and Security scan, no bell, and the switcher says Home', async () => {
    const r = mount(<TopBar route={{ name: 'workspace' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="nav-workspace"]')!.getAttribute('aria-current')).toBe('page'), r);
    // the mark has no test id here: the Home tab is the way home
    expect(ids(r)).toEqual([null, 'nav-workspace', 'nav-chats', 'nav-library', 'nav-security-scan', null, 'nav-settings']);
    expect(r.$('[data-testid="nav-workspace"]')!.getAttribute('href')).toBe('/home');
    expect(r.$('[data-testid="nav-security-scan"]')!.getAttribute('href')).toBe('/security-scan');
    expect(r.$('[data-testid="nav-needs-you"]')).toBeNull();
    expect(r.$('button[aria-label="Switch project"]')!.textContent).toContain('Home');
    cleanup();
    const c = mount(<TopBar route={{ name: 'chat', id: 'c1' }} />, svc());
    await waitFor(() => expect(c.$('[data-testid="nav-chats"]')!.getAttribute('aria-current')).toBe('page'), c);
  });

  it('without a project shows the workspace bar on every page', async () => {
    const r = mount(<TopBar route={{ name: 'license' }} />, svc({ projectList: async () => [] }));
    await waitFor(() => expect(r.$('[data-testid="nav-settings"]')!.getAttribute('aria-current')).toBe('page'), r);
    expect(r.$('[data-testid="nav-tasks"]')).toBeNull();
    expect(r.$('[data-testid="nav-chats"]')).not.toBeNull();
  });

  it('moves between its links with the arrow keys, but leaves keys from the switcher alone', async () => {
    const r = mount(<TopBar route={{ name: 'home' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="nav-tasks"]')).not.toBeNull(), r);
    const first = r.$('[data-nav]') as HTMLElement;
    first.focus();
    await r.act(() => first.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true })));
    expect(document.activeElement).toBe(r.$$('[data-nav]')[1]!);
    const sw = r.$('button[aria-label="Switch project"]') as HTMLButtonElement;
    sw.focus();
    await r.act(() => sw.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true, cancelable: true })));
    expect(document.activeElement).toBe(sw);
  });
});

describe('the hubs', () => {
  it('Tasks, Knowledge, Overview and Library show their pages as pills with the old test ids; Crew and Rooms have none', () => {
    const r = mount(<SubNav route={{ name: 'pulls' }} />);
    const pills = Array.from(r.$$('nav[aria-label="Section"] a')).map((a) => a.getAttribute('data-testid'));
    expect(pills).toEqual(['nav-tasks-board', 'nav-plan', 'nav-pulls', 'nav-milestones']);
    expect(r.$('[data-testid="nav-pulls"]')!.getAttribute('aria-current')).toBe('page');
    expect(r.$('[data-testid="nav-tasks-board"]')!.getAttribute('aria-current')).toBeNull();
    cleanup();
    const k = mount(<SubNav route={{ name: 'repo-map' }} />);
    expect(Array.from(k.$$('a')).map((a) => a.getAttribute('data-testid'))).toEqual(['nav-knowledge-home', 'nav-requirements', 'nav-packs', 'nav-skills', 'nav-memory', 'nav-repo-map']);
    expect(k.$('[data-testid="nav-repo-map"]')!.getAttribute('aria-current')).toBe('page');
    expect(k.$('[data-testid="nav-repo-map"]')!.textContent).toBe('Repo map');
    cleanup();
    const o = mount(<SubNav route={{ name: 'budgets' }} />);
    expect(Array.from(o.$$('a')).map((a) => a.getAttribute('data-testid'))).toEqual(['nav-overview-home', 'nav-activity', 'nav-budgets', 'nav-evals', 'nav-security', 'nav-project', 'nav-project-integrations']);
    cleanup();
    const l = mount(<SubNav route={{ name: 'library-skills' }} />);
    expect(Array.from(l.$$('a')).map((a) => a.getAttribute('data-testid'))).toEqual(['nav-library-home', 'nav-library-skills']);
    expect(l.$('[data-testid="nav-library-skills"]')!.textContent).toBe('Skills');
    cleanup();
    expect(mount(<SubNav route={{ name: 'team' }} />).$('nav')).toBeNull();
    cleanup();
    expect(mount(<SubNav route={{ name: 'needs-you' }} />).$('nav')).toBeNull();
    cleanup();
    expect(mount(<SubNav route={{ name: 'room', id: 'r1' }} />).$('nav')).toBeNull();
  });

  it("a project's own settings are the last pills of its Overview, set apart after a gap: Project settings (with a gear) and Integrations", () => {
    const r = mount(<SubNav route={{ name: 'project' }} />);
    const nav = r.$('nav[aria-label="Section"]')!;
    const ps = r.$('[data-testid="nav-project"]')!;
    expect(ps.getAttribute('href')).toBe('/project');
    expect(ps.textContent).toBe('Project settings');
    expect(ps.querySelector('svg')).not.toBeNull();
    expect(ps.getAttribute('aria-current')).toBe('page');
    expect(r.$('[data-testid="nav-overview-home"]')!.getAttribute('aria-current')).toBeNull();
    const pi = r.$('[data-testid="nav-project-integrations"]')!;
    expect(pi.getAttribute('href')).toBe('/project/integrations');
    expect(pi.textContent).toBe('Integrations');
    // the gap and the hairline sit between Security and Project settings, and are decoration
    const kids = Array.from(nav.children);
    const sec = kids.indexOf(r.$('[data-testid="nav-security"]')!);
    expect(kids.slice(sec + 1, sec + 3).every((k) => k.tagName === 'SPAN' && k.getAttribute('aria-hidden') === 'true')).toBe(true);
    expect(kids[sec + 3]).toBe(ps);
    cleanup();
    const i = mount(<SubNav route={{ name: 'project-integrations' }} />);
    expect(i.$('[data-testid="nav-project-integrations"]')!.getAttribute('aria-current')).toBe('page');
    expect(i.$$('[aria-current="page"]')).toHaveLength(1);
  });

  it("the Settings rail lists the workspace's pages (with GitHub) and the app's, marking the current one, and nothing of one project", async () => {
    const r = mount(<SettingsRail route={{ name: 'github' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="nav-github"]')).not.toBeNull(), r);
    const links = Array.from(r.$$('nav[aria-label="Settings"] a')).map((a) => a.getAttribute('data-testid'));
    expect(links).toEqual(['nav-integrations', 'nav-connections', 'nav-github', 'nav-providers', 'nav-routing', 'nav-settings-about', 'nav-license', 'nav-backups', 'nav-data', 'nav-support']);
    expect(r.$('[data-testid="nav-github"]')!.getAttribute('aria-current')).toBe('page');
    expect(r.$('[data-testid="nav-github"]')!.getAttribute('href')).toBe('/github');
    expect(r.$('[data-testid="nav-github"]')!.textContent).toBe('GitHub');
    // the project's pages moved to the project's Overview: no "This project" group, even with a project open
    expect(r.$('[data-testid="nav-project"]')).toBeNull();
    expect(r.$('[data-testid="nav-project-integrations"]')).toBeNull();
    expect(Array.from(r.$$('h2')).map((h) => h.textContent)).toEqual(['Workspace', 'App']);
    // the MCP servers page was merged into Integrations (decision P47): /mcp marks Integrations current, and has no link of its own
    cleanup();
    const m = mount(<SettingsRail route={{ name: 'mcp' }} />, svc({ projectList: async () => [] }));
    await waitFor(() => expect(m.$('[data-testid="nav-integrations"]')!.getAttribute('aria-current')).toBe('page'), m);
    expect(m.$('[data-testid="nav-project"]')).toBeNull();
    expect(m.container.querySelector('a[href="/mcp"]')).toBeNull();
  });

  it('the Rooms rail names rooms without a "#": an icon, the name, then a quiet unread count read once', async () => {
    const r = mount(<RoomsRail route={{ name: 'room', id: 'r2' }} />, svc());
    await waitFor(() => expect(r.$$('[data-testid="room-link"]')).toHaveLength(2), r);
    const [general, task] = Array.from(r.$$('[data-testid="room-link"]')) as HTMLAnchorElement[];
    expect(general!.textContent!.includes('#')).toBe(false);
    expect(general!.querySelector('.label')!.textContent).toBe('general');
    expect(general!.querySelector('.room-icon svg')!.getAttribute('aria-hidden')).toBe('true');
    await waitFor(() => expect(general!.getAttribute('data-unread')).toBe('true'), r);
    const count = general!.querySelector('[data-testid="unread-count"]')!;
    expect(count.querySelector('[aria-hidden="true"]')!.textContent).toBe('3');
    expect(count.querySelector('.sr-only')!.textContent).toBe(', 3 unread');
    expect(task!.querySelector('.label')!.textContent).toBe('task-3-subtract');
    expect(task!.getAttribute('aria-current')).toBe('page');
    expect(task!.querySelector('[data-testid="unread-count"]')).toBeNull();
    expect(r.$('nav[aria-label="Rooms"][data-rooms]')).not.toBeNull();
  });
});
