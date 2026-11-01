import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../api/client.ts';
import type { AssignPreview } from '../api/controlClient.ts';
import { lic } from '../test/fixtures.ts';
import { cleanup, mount, testServices, type, waitFor } from '../test/render.tsx';
import { adapterInfo } from '../screens/project/wizard-fixtures.ts';
import { CommandPalette } from './palette.tsx';

afterEach(cleanup);

function press(down: Element, clickTarget: Element) {
  down.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerType: 'mouse' }));
  clickTarget.dispatchEvent(new MouseEvent('click', { bubbles: true }));
}

const key = (target: EventTarget, k: string, mods: KeyboardEventInit = {}) => {
  const e = new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...mods });
  target.dispatchEvent(e);
  return e;
};

/** The calls hold this one (compared as JSON: an argument left out reads as null on both sides). */
const has = (calls: unknown[][], call: unknown[]): boolean => calls.some((c) => JSON.stringify(c) === JSON.stringify(call));

const openDialog = (c: Element) => {
  const d = c.querySelector<HTMLDialogElement>('dialog[aria-label="Command menu"]')!;
  if (!d.open) throw new Error('not open yet');
  return d;
};

// TEST DOUBLES: a partial client; the palette only reads the chats while it is open.
async function opened() {
  const s = testServices({ chatList: async () => [] });
  const r = mount(<CommandPalette onMessage={() => {}} />, s);
  s.ui.setPalette(true);
  const dlg = await waitFor(() => openDialog(r.container), r);
  return { s, r, dlg, input: dlg.querySelector('input')! };
}

describe('closing the command palette by its backdrop (L41)', () => {
  it('closes when the press and the click are both on the backdrop', async () => {
    const { s, r, dlg } = await opened();
    press(dlg, dlg);
    r.root.flush();
    expect(s.ui.paletteOpen).toBe(false);
  });

  it('stays open when a drag starts in the search box and ends on the backdrop', async () => {
    const { s, r, dlg, input } = await opened();
    press(input, dlg);
    r.root.flush();
    expect(s.ui.paletteOpen).toBe(true);
    expect(dlg.open).toBe(true);
    // a click on the list or the box never closes it
    press(input, input);
    expect(s.ui.paletteOpen).toBe(true);
    // and the next click on the backdrop closes it
    press(dlg, dlg);
    expect(s.ui.paletteOpen).toBe(false);
  });
});

describe('searching a task list that is cut (M21)', () => {
  // TEST DOUBLES: the project's shared reads; the palette searches the tasks the project model holds.
  async function search(total: number | undefined) {
    const t = { id: 't1', projectId: 'p1', seq: 1, title: 'Add Multiply', description: '', status: 'BACKLOG', priority: 0, iteration: 0, maxIterations: 3, costUsd: 0, created: 0, updated: 0, version: 1 };
    const s = testServices({
      chatList: async () => [],
      projectList: async () => [{ id: 'p1', name: 'gocalc' } as never],
      agentList: async () => [],
      roomList: async () => [],
      roomUnread: async () => [],
      needsYouList: async () => [],
      presenceList: async () => [],
      taskList: async () => [t],
      ...(total === undefined ? {} : { taskCount: async () => ({ total }) }),
    });
    s.workspace.start();
    s.project.start();
    const r = mount(<CommandPalette onMessage={() => {}} />, s);
    s.ui.setPalette(true);
    const dlg = await waitFor(() => openDialog(r.container), r);
    await waitFor(() => expect(s.project.taskList).toHaveLength(1), r);
    return { r, dlg, input: dlg.querySelector('input')! };
  }

  it('says which tasks were searched when nothing matches and the project has more', async () => {
    const { r, dlg, input } = await search(700);
    type(input, 'zzz-no-such-thing');
    await waitFor(() => expect(dlg.textContent).toContain('Nothing matches.'), r);
    expect(dlg.textContent).toContain('Only the newest 1 of 700 tasks are searched.');
  });

  it('says only "Nothing matches." while every task was searched', async () => {
    const { r, dlg, input } = await search(1);
    type(input, 'zzz-no-such-thing');
    await waitFor(() => expect(dlg.textContent).toContain('Nothing matches.'), r);
    expect(dlg.textContent).not.toContain('are searched');
  });
});

// TEST DOUBLES: a project gocalc (key GOC) with a task GOC-44, a team (Builder in WSL, a local helper on the models of this PC,
// the team lead Tess) and the core's answers the menu reads and calls; `calls` records every call that changes something.
const task44 = { id: 't44', projectId: 'p1', seq: 44, title: 'Support hex input', description: 'Parse 0x1F as 31.', status: 'READY', priority: 0, iteration: 0, maxIterations: 3, costUsd: 0, created: 0, updated: 0, version: 1 };
const team = [
  { id: 'b1', projectId: 'p1', name: 'Builder', role: 'backend', adapter: 'claude', model: 'sonnet', runMode: '', status: 'idle', providerId: '' },
  { id: 'l1', projectId: 'p1', name: 'Local helper', role: 'frontend', adapter: 'opencode', model: 'qwen', status: 'idle', providerId: 'prov_local' },
  { id: 'lead1', projectId: 'p1', name: 'Tess', role: 'lead', adapter: 'claude', model: 'opus', status: 'idle', providerId: '' },
];
const preview = (assignee: string, over: Partial<AssignPreview> = {}): AssignPreview => ({
  taskId: 't44',
  assignee,
  person: assignee === 'human:kyle',
  branch: 'drydock/t44-support-hex-input',
  hasWorktree: false,
  template: 'iterate-until-verified',
  specGate: true,
  testFirst: false,
  checks: ['test', 'lint', 'typecheck', 'secrets', 'deps'],
  mode: 'local_merge',
  pushes: false,
  asksBeforePush: false,
  asksBeforeMerge: true,
  reviewer: 'r1',
  stopsRun: false,
  agent: assignee === 'human:kyle' ? undefined : { id: assignee, name: team.find((a) => a.id === assignee)!.name, role: 'backend', adapter: 'claude', model: 'sonnet', runMode: 'wsl', wslDistro: 'Ubuntu', status: 'idle', presence: 'Idle' },
  queue: [{ id: 't43', seq: 43, title: 'Parse octal', status: 'IN_PROGRESS' }],
  waits: true,
  waitReason: 'waits for #43',
  waitsFor: { id: 't43', seq: 43, title: 'Parse octal', status: 'IN_PROGRESS' },
  ...over,
});

async function withTask(over: Partial<Client> = {}, props: { taskId?: string } = { taskId: 't44' }) {
  const calls: unknown[][] = [];
  const navigated: string[] = [];
  const s = testServices({
    chatList: async () => [],
    projectList: async () => [{ id: 'p1', name: 'gocalc', key: 'GOC', repoPath: '/r', hasRepo: true, defaultBranch: 'main', mergeStrategy: 'merge', mode: 'local_merge', created: 0 }],
    licenseStatus: async () => lic(),
    appInfo: async () => ({ name: 'Drydock', user: 'kyle', human: 'human:kyle', dataDir: '/d' }),
    agentList: async () => team as never,
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    presenceList: async () => [],
    taskList: async () => [task44 as never],
    localGet: async () => ({ usedBy: [], settings: { providerId: 'prov_local' } }) as never,
    adapterList: async () => [{ ...adapterInfo('claude'), capabilities: { ...adapterInfo('claude').capabilities, runModes: ['native', 'wsl'] } }],
    connectionList: async () => [],
    projectRunMode: async () => ({ runMode: 'wsl', wslDistro: 'Ubuntu' }),
    taskAssignPreview: async (id, assignee) => {
      calls.push(['preview', id, assignee]);
      return preview(assignee);
    },
    taskAssign: async (id, assignee, reviewer) => {
      calls.push(['assign', id, assignee, reviewer]);
      return task44 as never;
    },
    leadSplit: async (id) => {
      calls.push(['split', id]);
      return { action: 'split', message: '', reason: '', leadId: 'lead1', leadName: 'Tess', createdTasks: [], interventionId: 'i1' };
    },
    securityScanStart: async (pid) => {
      calls.push(['scan', pid]);
      return {} as never;
    },
    chatCreate: async (p) => {
      calls.push(['chatCreate', p]);
      return { id: 'chat_9' } as never;
    },
    chatSend: async (id, text) => {
      calls.push(['chatSend', id, text]);
      return { turnId: 'turn1', version: 1 };
    },
    ...over,
  });
  s.workspace.start();
  s.project.start();
  const r = mount(<CommandPalette onMessage={() => {}} onNavigate={(h) => void navigated.push(h)} taskId={props.taskId} />, s);
  await waitFor(() => expect(s.project.agentList).toHaveLength(3), r);
  await waitFor(() => expect(s.workspace.info.data?.human).toBe('human:kyle'), r);
  s.ui.setPalette(true);
  const dlg = await waitFor(() => openDialog(r.container), r);
  const input = dlg.querySelector('input')!;
  const options = () => Array.from(dlg.querySelectorAll<HTMLElement>('[role="option"]'));
  const labels = () => options().map((o) => o.querySelector('.lbl')!.textContent);
  const groups = () => Array.from(dlg.querySelectorAll('[role="group"]')).map((g) => document.getElementById(g.getAttribute('aria-labelledby')!)!.textContent);
  const panel = () => dlg.querySelector<HTMLElement>('aside[aria-label="What happens"]')!;
  const highlight = async (label: string) => {
    for (let i = 0; i < 40; i++) {
      const sel = dlg.querySelector('[role="option"][aria-selected="true"] .lbl')?.textContent;
      if (sel === label) return;
      await r.act(() => key(input, 'ArrowDown'));
    }
    throw new Error(`no option ${label}`);
  };
  return { s, r, dlg, input, calls, navigated, options, labels, groups, panel, highlight };
}

describe('the command menu over a task (the graphite mockup)', () => {
  it('names the task it acts on in a chip and lists Assign, Actions and Go to', async () => {
    const { r, dlg, input, labels, groups } = await withTask();
    const chip = await waitFor(() => dlg.querySelector('[data-testid="palette-context"]')!, r);
    expect(chip.textContent).toBe('GOC-44Support hex input');
    expect(chip.querySelector('.key')!.textContent).toBe('GOC-44');
    expect(input.getAttribute('role')).toBe('combobox');
    expect(input.placeholder).toMatch(/^Go to, open or do/);
    expect(document.getElementById(input.getAttribute('aria-describedby')!)!.textContent).toBe('Acts on GOC-44 Support hex input');
    await waitFor(() => expect(labels()).toContain('Ask Claude Code about this task'), r);
    expect(groups()).toEqual(['Assign', 'Actions', 'Go to']);
    expect(labels()).toEqual([
      'Assign to Builder',
      'Assign to Local helper',
      'Ask the team lead to split it',
      'Assign to me',
      'Scan gocalc for secrets, packages and unsafe code',
      'Ask Claude Code about this task',
      'Inbox',
      'Tasks',
      'Crew',
    ]);
    const opt = (label: string) => Array.from(dlg.querySelectorAll('[role="option"]')).find((o) => o.querySelector('.lbl')!.textContent === label)!;
    // where each agent runs (Builder follows the project, which runs in WSL), the local helper on the models of this PC
    expect(opt('Assign to Builder').querySelector('.hint')!.textContent).toBe('Claude Code WSL');
    expect(opt('Assign to Local helper').querySelector('.hint')!.textContent).toBe('Models on this PC');
    expect(Array.from(opt('Ask the team lead to split it').querySelectorAll('kbd:not(.enter)')).map((k) => k.textContent)).toEqual(['⇧', 'S']);
    expect(Array.from(opt('Assign to me').querySelectorAll('kbd:not(.enter)')).map((k) => k.textContent)).toEqual(['I']);
    expect(Array.from(opt('Crew').querySelectorAll('kbd:not(.enter)')).map((k) => k.textContent)).toEqual(['G', 'C']);
    // the footer's hints
    expect(dlg.textContent).toContain('Type > for settings, # for tasks, @ for agents');
    expect(dlg.textContent).toContain('run and open');
  });

  it('without a task keeps the groups of the menu: Navigate, Chats, Tasks, Agents, Actions', async () => {
    const { r, dlg, groups } = await withTask({}, {});
    await waitFor(() => expect(groups()).toEqual(['Navigate', 'Chats', 'Tasks', 'Agents', 'Actions']), r);
    expect(dlg.querySelector('[data-testid="palette-context"]')).toBe(null);
    expect(dlg.textContent).not.toContain('Assign to Builder');
  });

  it('narrows to settings and actions with ">", tasks with "#", agents with "@"', async () => {
    const { r, input, labels, groups } = await withTask();
    await r.act(() => type(input, '#'));
    await waitFor(() => expect(groups()).toEqual(['Tasks']), r);
    expect(labels()).toContain('GOC-44 Support hex input');
    await r.act(() => type(input, '@tess'));
    await waitFor(() => expect(labels()[0]).toBe("Open Tess's page"), r);
    expect(labels().every((l) => /Tess/.test(l!))).toBe(true);
    await r.act(() => type(input, '> theme'));
    // the settings page that holds the theme, and the three themes
    await waitFor(() => expect([...labels()].sort()).toEqual(['Settings', 'Theme: dark', 'Theme: follow the system', 'Theme: light']), r);
    await r.act(() => type(input, '> crew'));
    await waitFor(() => expect(labels()).toEqual([]), r);
  });

  it("shows what assigning does from the core's preview: the agent, the branch, the checks, the queue", async () => {
    const { r, calls, panel } = await withTask();
    await waitFor(() => expect(panel().querySelector('[data-testid="palette-steps"]')).not.toBe(null), r);
    expect(has(calls, ['preview', 't44', 'b1'])).toBe(true);
    const who = panel().querySelector('[data-testid="palette-who"]')!;
    expect(who.querySelector('.name')!.textContent).toBe('Builder');
    expect(who.querySelector('.sub')!.textContent).toBe('Claude Code WSL · sonnet · idle');
    const steps = Array.from(panel().querySelectorAll('[data-testid="palette-steps"] li')).map((l) => l.textContent);
    expect(steps).toEqual([
      'Starts in its own worktree on drydock/t44-support-hex-input',
      'Writes the spec first; you accept it at the spec gate',
      'Runs tests, lint, types, secrets and deps before review',
      'Asks you before it merges',
    ]);
    expect(panel().querySelector('code')!.textContent).toBe('drydock/t44-support-hex-input');
    expect(panel().querySelector('[data-testid="palette-wait"]')!.textContent).toBe('Builder has 1 task in progress. This one waits until GOC-43 is finished: both change the same code.');
  });

  it('leaves out the queue box when the task would start at once, and says what other options do', async () => {
    const { r, panel, highlight } = await withTask({ taskAssignPreview: async (_id, a) => preview(a, { waits: false, waitsFor: undefined, specGate: false }) });
    await waitFor(() => expect(panel().querySelector('[data-testid="palette-steps"]')).not.toBe(null), r);
    expect(panel().querySelector('[data-testid="palette-wait"]')).toBe(null);
    expect(panel().textContent).not.toContain('spec gate');
    await highlight('Ask the team lead to split it');
    expect(panel().textContent).toContain('Tess reads GOC-44 and splits it into smaller tasks in the backlog');
    await highlight('Crew');
    expect(panel().textContent).toContain('Goes to Crew.');
  });

  it('says when nobody could review the work instead of the queue', async () => {
    const { r, panel } = await withTask({ taskAssignPreview: async (_id, a) => preview(a, { reviewer: '' }) });
    const box = await waitFor(() => {
      const b = panel().querySelector('[data-testid="palette-wait"]');
      if (!b) throw new Error('no box yet');
      return b;
    }, r);
    expect(box.textContent).toBe("Nobody else on the team can review Builder's work: add another agent first.");
    expect(box.getAttribute('data-block')).toBe('true');
  });

  it('Enter on an assignment calls taskAssign without a reviewer and closes; Ctrl+Enter also opens the task', async () => {
    const { s, r, input, calls, navigated } = await withTask();
    await waitFor(() => expect(input.getAttribute('aria-activedescendant')).not.toBe(null), r);
    await r.act(() => key(input, 'Enter'));
    await waitFor(() => expect(s.ui.paletteOpen).toBe(false), r);
    expect(has(calls, ['assign', 't44', 'b1', undefined])).toBe(true);
    expect(navigated).toEqual([]);

    s.ui.setPalette(true);
    await waitFor(() => expect(input.getAttribute('aria-activedescendant')).not.toBe(null), r);
    await r.act(() => key(input, 'Enter', { ctrlKey: true }));
    await waitFor(() => expect(navigated).toEqual(['/task/t44']), r);
  });

  it('asks the team lead to split it, and assigns it to the person', async () => {
    const { s, r, calls, highlight, input } = await withTask();
    await highlight('Ask the team lead to split it');
    await r.act(() => key(input, 'Enter'));
    await waitFor(() => expect(has(calls, ['split', 't44'])).toBe(true), r);
    await waitFor(() => expect(s.ui.paletteOpen).toBe(false), r);

    s.ui.setPalette(true);
    await highlight('Assign to me');
    await waitFor(() => expect(has(calls, ['preview', 't44', 'human:kyle'])).toBe(true), r);
    await r.act(() => key(input, 'Enter'));
    await waitFor(() => expect(has(calls, ['assign', 't44', 'human:kyle', undefined])).toBe(true), r);
  });

  it("scans the project, and asks the CLI about the task in a new chat it opens", async () => {
    const { r, calls, highlight, input, navigated } = await withTask();
    await highlight('Scan gocalc for secrets, packages and unsafe code');
    await r.act(() => key(input, 'Enter'));
    await waitFor(() => expect(has(calls, ['scan', 'p1'])).toBe(true), r);
    expect(navigated).toEqual([]);

    // a CLI installed only inside WSL: the chat runs there
    const { r: r2, calls: c2, highlight: h2, input: i2, navigated: n2 } = await withTask({
      adapterList: async () => [{ ...adapterInfo('claude', false), capabilities: { ...adapterInfo('claude').capabilities, runModes: ['native', 'wsl'] } }],
      connectionList: async () => [{ adapter: 'claude', key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', installed: true, status: 'ready', usable: true, platform: 'linux' } as never],
    });
    await waitFor(() => expect(i2.getAttribute('aria-activedescendant')).not.toBe(null), r2);
    await h2('Ask Claude Code WSL about this task');
    await r2.act(() => key(i2, 'Enter'));
    await waitFor(() => expect(n2).toEqual(['/chats/chat_9']), r2);
    expect(has(c2, ['chatCreate', { projectId: 'p1', adapter: 'claude', wslDistro: 'Ubuntu', title: 'GOC-44 Support hex input' }])).toBe(true);
    expect(has(c2, ['chatSend', 'chat_9', 'About GOC-44 "Support hex input":\n\nParse 0x1F as 31.'])).toBe(true);
    void r;
  });

  it('keeps the menu open with the reason when the core refuses', async () => {
    const { s, r, input, panel } = await withTask({
      taskAssign: async () => {
        throw new Error('the task is done');
      },
    });
    await waitFor(() => expect(input.getAttribute('aria-activedescendant')).not.toBe(null), r);
    await r.act(() => key(input, 'Enter'));
    await waitFor(() => expect(panel().querySelector('[role="alert"]')!.textContent).toContain('the task is done'), r);
    expect(s.ui.paletteOpen).toBe(true);
  });

  it('hides Ask the team lead without a lead on the team', async () => {
    const { r, labels } = await withTask({ agentList: async () => [team[0], team[1], { ...team[0], id: 'b2', name: 'Bea' }] as never });
    await waitFor(() => expect(labels()).toContain('Assign to Bea'), r);
    expect(labels()).not.toContain('Ask the team lead to split it');
  });
});

describe('G then a letter', () => {
  it('goes to Crew, Tasks or Inbox from the page, never while typing in a field', async () => {
    const navigated: string[] = [];
    const s = testServices({ chatList: async () => [] });
    const r = mount(<CommandPalette onMessage={() => {}} onNavigate={(h) => void navigated.push(h)} />, s);
    key(document.body, 'g');
    const c = key(document.body, 'c');
    expect(c.defaultPrevented).toBe(true);
    key(document.body, 'g');
    key(document.body, 't');
    key(document.body, 'g');
    key(document.body, 'i');
    expect(navigated).toEqual(['/team', '/tasks', '/needs-you']);
    // a C alone, or after another key, is nobody's chord
    key(document.body, 'c');
    key(document.body, 'g');
    key(document.body, 'x');
    key(document.body, 'c');
    // typing "gc" in a field types it
    const field = document.createElement('input');
    r.container.append(field);
    key(field, 'g');
    key(field, 'c');
    // Ctrl or Shift is not the chord
    key(document.body, 'g');
    key(document.body, 'c', { ctrlKey: true });
    expect(navigated).toEqual(['/team', '/tasks', '/needs-you']);
    field.remove();
  });
});
