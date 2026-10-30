import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../api/client.ts';
import type { Room } from '../api/types.ts';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { RoomsRail, SettingsRail, SubNav } from './hubnav.tsx';

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
    // Agent templates and Skills are entries of the sidebar (decision P106): no pills between them
    expect(mount(<SubNav route={{ name: 'library-skills' }} />).$('nav')).toBeNull();
    cleanup();
    expect(mount(<SubNav route={{ name: 'library' }} />).$('nav')).toBeNull();
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

  it("the Settings rail lists Routing and the app's pages, marking the current one, and nothing of one project; Agents, Integrations and Models are the sidebar's own (decision P98)", async () => {
    const r = mount(<SettingsRail route={{ name: 'routing' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="nav-routing"]')).not.toBeNull(), r);
    const links = Array.from(r.$$('nav[aria-label="Settings"] a')).map((a) => a.getAttribute('data-testid'));
    expect(links).toEqual(['nav-routing', 'nav-settings-about', 'nav-license', 'nav-backups', 'nav-data', 'nav-support', 'nav-guide']);
    expect(r.$('[data-testid="nav-routing"]')!.getAttribute('aria-current')).toBe('page');
    expect(r.$('[data-testid="nav-routing"]')!.getAttribute('href')).toBe('/routing');
    // the project's pages moved to the project's Overview: no "This project" group, even with a project open
    expect(r.$('[data-testid="nav-project"]')).toBeNull();
    expect(r.$('[data-testid="nav-project-integrations"]')).toBeNull();
    expect(Array.from(r.$$('h2')).map((h) => h.textContent)).toEqual(['Workspace', 'App']);
    for (const gone of ['/integrations', '/connections', '/agents', '/github', '/providers', '/models', '/mcp']) expect(r.container.querySelector(`a[href="${gone}"]`)).toBeNull();
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

  it('says a muted room is muted, after its name and once for a reader (P130)', async () => {
    const r = mount(
      <RoomsRail route={{ name: 'room', id: 'r1' }} />,
      svc({ roomUnread: async () => [{ roomId: 'r2', roomName: 'task-3-subtract', kind: 'task', unread: 1, mentions: 1, muted: true, lastReadSeq: 0 }] }),
    );
    await waitFor(() => expect(r.$$('[data-room-muted], [data-muted="true"]')).toHaveLength(1), r);
    const [general, task] = Array.from(r.$$('[data-testid="room-link"]')) as HTMLAnchorElement[];
    expect(general!.getAttribute('data-muted')).toBeNull();
    expect(task!.querySelector('[data-testid="room-muted"] .sr-only')!.textContent).toBe(', muted');
    expect(task!.textContent).toBe('task-3-subtract, muted1, 1 unread');
  });

  it('cuts a long room name with an ellipsis inside its column instead of running past it (UI audit 2026-10-26)', async () => {
    const long = 'task-2-add-a-multiply-function-with-table-tests-and-a-name-longer-than-any-column';
    const r = mount(
      <div style={{ width: '220px' }} data-testid="column">
        <RoomsRail route={{ name: 'room', id: 'r1' }} />
      </div>,
      svc({ roomList: async () => [room({}), room({ id: 'r2', kind: 'task', name: long, taskId: 't3' })] }),
    );
    await waitFor(() => expect(r.$$('[data-testid="room-link"]')).toHaveLength(2), r);
    const column = r.$('[data-testid="column"]')!.getBoundingClientRect();
    const link = r.$$('[data-testid="room-link"]')[1] as HTMLElement;
    expect(link.getBoundingClientRect().right <= column.right + 1).toBe(true);
    const label = link.querySelector('.label') as HTMLElement;
    expect(label.textContent).toBe(long);
    expect(label.scrollWidth > label.clientWidth).toBe(true);
  });
});
