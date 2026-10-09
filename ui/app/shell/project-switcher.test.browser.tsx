import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { ProjectSwitcher } from './project-switcher.tsx';

afterEach(cleanup);

function svc(projects = [{ id: 'p1', name: 'gocalc-approve' }, { id: 'p2', name: 'lib-a' }]) {
  const s = testServices({
    projectList: async () => projects as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
  });
  s.workspace.start();
  return s;
}

/** The menu's popover surface is showing (role="menu" is the list inside it). */
const shown = (r: { $: (s: string) => Element | null }) => !!r.$('[role="menu"]')?.closest('[popover]')?.matches(':popover-open');
const key = (el: Element, k: string) => el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));

async function setup(projects?: { id: string; name: string }[]) {
  const s = svc(projects);
  const navigated: string[] = [];
  const r = mount(<ProjectSwitcher onNavigate={(h) => navigated.push(h)} />, s);
  const trigger = await waitFor(() => {
    const b = r.$('button[aria-label="Switch project"]') as HTMLButtonElement | null;
    if (!b) throw new Error('no switcher');
    if (projects?.length !== 0 && !b.textContent!.includes('gocalc-approve')) throw new Error('projects not loaded yet');
    return b;
  }, r);
  const open = async () => {
    await r.act(() => trigger.click());
    await waitFor(() => expect(shown(r)).toBe(true), r);
  };
  const items = () => Array.from(r.$$('[role^="menuitem"]')) as HTMLElement[];
  const radios = () => Array.from(r.$$('[role="menuitemradio"]')) as HTMLElement[];
  return { s, r, trigger, open, items, radios, navigated };
}

describe('the project switcher', () => {
  it('shows the current project and opens a menu of projects (radio items), New project and App settings', async () => {
    const { trigger, open, items, radios, r } = await setup();
    expect(trigger.getAttribute('aria-haspopup')).toBe('menu');
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
    await open();
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(r.$('[role="menu"]')!.getAttribute('aria-label')).toBe('Projects');
    expect(radios().map((e) => e.querySelector('.label')!.textContent)).toEqual(['gocalc-approve', 'lib-a']);
    expect(radios().map((e) => e.getAttribute('aria-checked'))).toEqual(['true', 'false']);
    // the projects are never plain buttons, so a name search for a button ("Approve") cannot hit "gocalc-approve"
    expect(Array.from(r.$$('button')).length).toBe(1);
    expect(items().filter((e) => e.getAttribute('role') === 'menuitem').map((e) => e.textContent!.trim())).toEqual(['New project…', 'App settings']);
  });

  it('switches project when a project is chosen', async () => {
    const { s, open, radios, trigger, r } = await setup();
    await open();
    await r.act(() => radios()[1]!.click());
    await waitFor(() => expect(s.workspace.projectId).toBe('p2'), r);
    await waitFor(() => expect(trigger.textContent).toContain('lib-a'), r);
    await waitFor(() => expect(shown(r)).toBe(false), r);
  });

  it('works from the keyboard: arrow down opens at the first item, arrows move, Enter selects', async () => {
    const { s, trigger, r } = await setup();
    trigger.focus();
    await r.act(() => key(trigger, 'ArrowDown'));
    const menu = await waitFor(() => {
      expect(shown(r)).toBe(true);
      return r.$('[role="menu"]')!;
    }, r);
    await waitFor(() => expect(r.$('[data-highlighted="true"]')!.getAttribute('data-value')).toBe('p1'), r);
    await r.act(() => key(menu, 'ArrowDown'));
    await waitFor(() => expect(r.$('[data-highlighted="true"]')!.getAttribute('data-value')).toBe('p2'), r);
    await r.act(() => key(menu, 'Enter'));
    await waitFor(() => expect(s.workspace.projectId).toBe('p2'), r);
  });

  it('goes to the new project form and to the app settings', async () => {
    const { open, items, navigated, r, trigger } = await setup();
    await open();
    await r.act(() => items().find((e) => e.textContent!.includes('New project'))!.click());
    await waitFor(() => expect(navigated).toEqual(['/project/new']), r);
    await waitFor(() => expect(shown(r)).toBe(false), r);
    await waitFor(() => expect(trigger.getAttribute('aria-expanded')).toBe('false'), r);
    // the menu finishes its selection (flash, close, back to idle) a moment after it hides; a person cannot click that fast
    await new Promise((res) => setTimeout(res, 150));
    await open();
    await r.act(() => items().find((e) => e.textContent!.includes('App settings'))!.click());
    await waitFor(() => expect(navigated).toEqual(['/project/new', '/settings']), r);
  });

  it('says "No project" and still offers a new project when there is none', async () => {
    const { trigger, open, items, radios } = await setup([]);
    expect(trigger.textContent).toContain('No project');
    await open();
    expect(radios()).toHaveLength(0);
    expect(items().map((e) => e.textContent!.trim())).toEqual(['New project…', 'App settings']);
  });
});
