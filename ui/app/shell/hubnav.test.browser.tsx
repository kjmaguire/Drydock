import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../api/client.ts';
import type { Room } from '../api/types.ts';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { RoomsRail, SectionPills, SettingsRail, SubNav } from './hubnav.tsx';
import { PROJECT_SECTIONS } from './project-sections.ts';

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
  it('the Agents library and a Settings section of more than one screen show their pages as pills with the old test ids', () => {
    // one Agents library (decision P140): its title, My agents, Starters, Skills, then every project's crew set apart
    for (const name of ['library', 'library-starters', 'library-skills', 'crew'] as const) {
      const l = mount(<SubNav route={{ name }} />);
      expect(Array.from(l.$$('a')).map((a) => a.getAttribute('data-testid'))).toEqual(['nav-library-mine', 'nav-library-starters', 'nav-library-skills', 'nav-crew']);
      expect(l.$('nav')!.textContent).toBe('AgentsMy agentsStartersSkillsAll crews');
      expect(l.$$('[aria-current="page"]')).toHaveLength(1);
      cleanup();
    }
    expect(mount(<SubNav route={{ name: 'library-starters' }} />).$('[data-testid="nav-library-starters"]')!.getAttribute('aria-current')).toBe('page');
    cleanup();
    // a Settings section of more than one screen shows them as pills beside the rail (decision P144: SectionPills, not over the
    // rail, where they pushed it down a row); a section of one shows none
    expect(mount(<SubNav route={{ name: 'data' }} />).$('nav')).toBeNull();
    cleanup();
    const b = mount(<SectionPills route={{ name: 'data' }} />);
    expect(Array.from(b.$$('a')).map((a) => [a.getAttribute('data-testid'), a.getAttribute('href')])).toEqual([
      ['nav-backups-page', '/backups'],
      ['nav-data', '/data'],
    ]);
    expect(b.$('[data-testid="nav-data"]')!.getAttribute('aria-current')).toBe('page');
    cleanup();
    const h = mount(<SectionPills route={{ name: 'license' }} />);
    expect(Array.from(h.$$('a')).map((a) => a.getAttribute('data-testid'))).toEqual(['nav-about', 'nav-support', 'nav-license', 'nav-guide']);
    cleanup();
    expect(mount(<SubNav route={{ name: 'models' }} />).$('nav')).toBeNull();
    cleanup();
    expect(mount(<SectionPills route={{ name: 'models' }} />).$('nav')).toBeNull();
    cleanup();
    expect(mount(<SectionPills route={{ name: 'library' }} />).$('nav')).toBeNull();
    cleanup();
    expect(mount(<SubNav route={{ name: 'needs-you' }} />).$('nav')).toBeNull();
  });

  it("a project's pages have one tab bar (decision P141): its name, Work, Crew and Settings with the old test ids, New task on Work", async () => {
    const tabs = (r: ReturnType<typeof mount>) => Array.from(r.$$('nav[aria-label="Project"] > a')).map((a) => [a.getAttribute('data-testid'), a.textContent, a.getAttribute('href'), a.getAttribute('aria-current')]);
    const w = mount(<SubNav route={{ name: 'home' }} />, svc());
    await waitFor(() => expect(w.$('[data-testid="project-name"]')!.textContent).toBe('gocalc'), w);
    expect(tabs(w)).toEqual([
      ['nav-overview', 'Work', '/', 'page'],
      ['nav-team', 'Crew', '/team', null],
      ['nav-project-settings', 'Settings', '/project', null],
    ]);
    expect(w.$('[data-testid="work-new-task"]')!.getAttribute('href')).toBe('/tasks/new');
    // Work has no pills
    expect(w.$('nav[aria-label="Section"]')).toBeNull();
    cleanup();
    // the board, a task, the pull requests and the rooms are Work's; the tab bar's New task is on Work's own page only
    for (const route of [{ name: 'tasks' }, { name: 'task', id: 't1' }, { name: 'pulls' }, { name: 'room', id: 'r1' }] as const) {
      const r = mount(<SubNav route={route} />, svc());
      expect(r.$('[data-testid="nav-overview"]')!.getAttribute('aria-current')).toBe('page');
      expect(r.$('[data-testid="work-new-task"]')).toBeNull();
      expect(r.$('nav[aria-label="Section"]')).toBeNull();
      cleanup();
    }
    const c = mount(<SubNav route={{ name: 'agent', id: 'agt1' }} />, svc());
    expect(c.$('[data-testid="nav-team"]')!.getAttribute('aria-current')).toBe('page');
    expect(c.$$('[aria-current="page"]')).toHaveLength(1);
    cleanup();
    // Settings: the pages that left the old tabs; a section of more than one page shows its pills (Knowledge, Plan & milestones)
    const k = mount(<SubNav route={{ name: 'repo-map' }} />, svc());
    expect(k.$('[data-testid="nav-project-settings"]')!.getAttribute('aria-current')).toBe('page');
    // the section's pills are beside the rail, not in the tab bar's row (decision P144)
    expect(k.$('nav[aria-label="Section"]')).toBeNull();
    cleanup();
    const kp = mount(<SectionPills route={{ name: 'repo-map' }} />, svc());
    expect(Array.from(kp.$$('nav[aria-label="Section"] a')).map((a) => a.getAttribute('data-testid'))).toEqual(['nav-knowledge-home', 'nav-requirements', 'nav-packs', 'nav-skills', 'nav-memory', 'nav-repo-map']);
    expect(kp.$('[data-testid="nav-repo-map"]')!.getAttribute('aria-current')).toBe('page');
    cleanup();
    const m = mount(<SectionPills route={{ name: 'milestones' }} />, svc());
    expect(Array.from(m.$$('nav[aria-label="Section"] a')).map((a) => a.getAttribute('data-testid'))).toEqual(['nav-plan', 'nav-milestones']);
    cleanup();
    for (const name of ['project', 'project-integrations', 'budgets', 'activity', 'evals', 'security'] as const) {
      const r = mount(<SubNav route={{ name }} />, svc());
      expect([name, r.$('[data-testid="nav-project-settings"]')!.getAttribute('aria-current')]).toEqual([name, 'page']);
      expect(r.$('nav[aria-label="Section"]')).toBeNull();
      cleanup();
      expect([name, mount(<SectionPills route={{ name }} />, svc()).$('nav')]).toEqual([name, null]);
      cleanup();
    }
  });

  it("the project's Settings rail: its sections with the old test ids, the current one marked, and a search", async () => {
    const r = mount(<SettingsRail route={{ name: 'packs' }} sections={PROJECT_SECTIONS} title="Project settings" />, svc());
    const sections = () => Array.from(r.$$('ul[aria-label="Sections"] > li > a')).map((a) => [a.getAttribute('data-testid'), a.textContent]);
    expect(r.$('nav[aria-label="Project settings"] h2')!.textContent).toBe('Project settings');
    expect(sections()).toEqual([
      ['nav-project', 'General'],
      ['nav-project-integrations', 'Integrations'],
      ['nav-knowledge', 'Knowledge'],
      ['nav-planning', 'Plan & milestones'],
      ['nav-activity', 'Activity'],
      ['nav-budgets', 'Budgets'],
      ['nav-evals', 'Evals report'],
      ['nav-security', 'Security'],
    ]);
    expect(r.$('[data-testid="nav-knowledge"]')!.getAttribute('aria-current')).toBe('page');
    expect(r.$$('[aria-current="page"]')).toHaveLength(1);
    const box = r.$('[data-testid="settings-search"]') as HTMLInputElement;
    expect(box.getAttribute('aria-label')).toBe('Search project settings');
    await r.act(() => {
      box.value = 'milestones';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(sections()).toEqual([['nav-planning', 'Plan & milestones']]);
  });

  it('the Settings rail (decision P140): "Settings", a search box, then the ten sections with their old test ids, the current one marked; nothing of one project', async () => {
    const r = mount(<SettingsRail route={{ name: 'agents', key: 'claude' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="nav-side-agents"]')).not.toBeNull(), r);
    const sections = () => Array.from(r.$$('ul[aria-label="Sections"] > li > a')).map((a) => a.textContent);
    expect(r.$('h2')!.textContent).toBe('Settings');
    expect(sections()).toEqual(['General', 'Agent CLIs', 'Models', 'Routing & budgets', 'Integrations', 'GitHub', 'Security scan', 'Backups & data', 'Shortcuts', 'Help & about']);
    expect(r.$('[data-testid="nav-side-agents"]')!.getAttribute('aria-current')).toBe('page');
    expect(r.$('[data-testid="nav-side-agents"]')!.getAttribute('href')).toBe('/agents');
    expect(r.$$('[aria-current="page"]')).toHaveLength(1);
    expect(r.$('[data-testid="nav-project"]')).toBeNull();
    expect(r.$('[data-testid="nav-project-integrations"]')).toBeNull();
    // the search keeps the sections that match and names the rows it found; Esc clears it
    const box = r.$('[data-testid="settings-search"]') as HTMLInputElement;
    expect(box.getAttribute('aria-label')).toBe('Search settings');
    await r.act(() => {
      box.value = 'theme';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(sections()).toEqual(['General']);
    expect(Array.from(r.$$('[data-testid="settings-hit"]')).map((a) => [a.textContent, a.getAttribute('href')])).toEqual([['Theme', '/settings']]);
    await r.act(() => {
      box.value = 'export';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(sections()).toEqual(['Backups & data']);
    expect(Array.from(r.$$('[data-testid="settings-hit"]')).map((a) => a.getAttribute('href'))).toEqual(['/backups', '/data']);
    await r.act(() => {
      box.value = 'qqq';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(sections()).toEqual([]);
    expect(r.$('[data-testid="settings-no-match"]')!.textContent).toBe('No setting matches "qqq".');
    await r.act(() => box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })));
    expect(sections()).toHaveLength(10);
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
    expect(task!.querySelector('.label')!.textContent).toBe('task-3-subtract, muted');
    expect(task!.querySelector('[data-testid="room-muted"]')!.getAttribute('aria-hidden')).toBe('true');
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
