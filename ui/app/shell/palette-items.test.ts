import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { AssignPreview } from '../api/controlClient.ts';
import { PER_GROUP, RECENT_KEY, RECENT_MAX, arrange, buildItems, contextItems, filterItems, isLeadRole, keepsRecent, parseQuery, previewBlock, previewSteps, previewWait, pushRecent, readRecent, recordRecent, score, taskKey, type TaskContext } from './palette-items.ts';

const rooms = [
  { id: 'r1', projectId: 'p', name: 'general', kind: 'channel', archived: false },
  { id: 'r2', projectId: 'p', name: 'dm:human:me+a1', kind: 'dm', archived: false },
  { id: 'r3', projectId: 'p', name: 'old', kind: 'channel', archived: true },
] as never[];
const agents = [{ id: 'a1', name: 'Ada', role: 'backend' }] as never[];
const tasks = [{ id: 't1', seq: 7, title: 'Add Multiply', status: 'READY', runId: 'run1' }] as never[];

describe('command palette items', () => {
  it('offers rooms (not archived ones), screens, tasks, agents and harmless actions', () => {
    const items = buildItems(rooms, tasks, agents);
    const labels = items.map((i) => i.label);
    // a room by its name, with the hint room: '#' is what the palette types for tasks (critique 2026-11-13)
    expect(items.find((i) => i.label === 'general')).toMatchObject({ hint: 'room', href: '/room/r1' });
    expect(labels).toContain('Direct message Ada');
    expect(labels.some((l) => l.includes('old'))).toBe(false);
    expect(labels).toContain('Crew');
    expect(labels).toContain('#7 Add Multiply');
    expect(labels).toContain('Workflow of #7 Add Multiply');
    expect(labels).toContain('Inspect Ada');
    // an agent's own page, by its name
    expect(items.find((i) => i.label === "Open Ada's page")).toMatchObject({ group: 'Agents', hint: 'backend', href: '/team/a1' });
    expect(filterItems(items, 'ada').map((i) => i.label)).toContain("Open Ada's page");
    // nothing that approves, merges, deletes or stops
    // (the Guide's topics are reading, and are named after what they explain: "Approvals and the Inbox")
    expect(labels.filter((l) => !l.startsWith('Guide')).some((l) => /approve|merge|delete|stop|cancel/i.test(l))).toBe(false);
    // every option says what it does
    expect(items.filter((i) => !i.what)).toEqual([]);
  });

  it('finds a Guide topic by its title or a word people use', () => {
    const items = buildItems(rooms, tasks, agents);
    expect(items.find((i) => i.label === 'Guide: Scan a repository for problems')).toMatchObject({ group: 'Navigate', href: '/guide/scan' });
    expect(filterItems(items, 'rule sets')[0]!.href).toBe('/guide/rule-licences');
    expect(filterItems(items, 'guide')[0]!.label).toBe('Guide');
  });

  it('ranks an exact screen name first', () => {
    const items = buildItems(rooms, tasks, agents);
    expect(filterItems(items, 'team')[0]!.label).toBe('Crew');
    expect(filterItems(items, 'multiply')[0]!.label).toBe('#7 Add Multiply');
    expect(filterItems(items, '7')[0]!.id).toBe('task:t1');
    // the MCP servers page was folded into Integrations (decision P47): "mcp" still finds it
    expect(items.map((i) => i.label)).not.toContain('MCP servers of this project');
    expect(filterItems(items, 'mcp')[0]!.label).toBe('Integrations');
    // the sidebar calls Needs you "Inbox": both words find it
    expect(filterItems(items, 'inbox')[0]!.href).toBe('/needs-you');
    expect(filterItems(items, 'needs you')[0]!.href).toBe('/needs-you');
  });

  it("offers the project's own Integrations page next to the workspace's", () => {
    const items = buildItems(rooms, tasks, agents);
    const proj = items.find((i) => i.label === 'Project integrations')!;
    expect(proj.href).toBe('/project/integrations');
    expect(items.find((i) => i.label === 'Integrations')!.href).toBe('/integrations');
    expect(filterItems(items, 'project integrations')[0]!.label).toBe('Project integrations');
    expect(filterItems(items, 'approvals').map((i) => i.label)).toContain('Project integrations');
    // the Agents library keeps the agent templates as My agents (decision P140); the MCP servers you add are on Integrations (P98)
    expect(items.find((i) => i.label === 'My agents')!.href).toBe('/library');
    expect(items.map((i) => i.label)).not.toContain('Library: agents and MCP servers');
    expect(filterItems(items, 'templates')[0]!.label).toBe('My agents');
  });

  it('offers the GitHub settings page, found by "gh" and by what it holds', () => {
    const items = buildItems(rooms, tasks, agents);
    expect(items.find((i) => i.label === 'GitHub')!.href).toBe('/github');
    expect(filterItems(items, 'gh')[0]!.label).toBe('GitHub');
    expect(filterItems(items, 'runners').map((i) => i.label)).toContain('GitHub');
  });

  it('offers Models, Agent CLIs and Integrations, sections of Settings, found by what they hold (decisions P98, P140)', () => {
    const items = buildItems(rooms, tasks, agents);
    const models = items.find((i) => i.label === 'Models')!;
    expect(models).toMatchObject({ group: 'Navigate', href: '/models', what: 'Goes to Models.' });
    // a section of Settings: listed by ">"
    expect(models.scope).toBe('>');
    expect(filterItems(items, 'models on this pc')[0]!.href).toBe('/models');
    expect(filterItems(items, 'local models')[0]!.href).toBe('/models');
    expect(filterItems(items, 'gguf')[0]!.href).toBe('/models');
    expect(filterItems(items, 'hugging face').map((i) => i.href)).toContain('/models');
    // the model providers are on the same page now
    expect(filterItems(items, 'providers')[0]!.href).toBe('/models');
    expect(filterItems(items, 'openrouter')[0]!.href).toBe('/models');
    // the agent CLIs, found by their old page's name too
    expect(filterItems(items, 'connections')[0]!.href).toBe('/agents');
    expect(filterItems(items, 'claude code')[0]!.href).toBe('/agents');
    expect(items.find((i) => i.label === 'Agent CLIs')!.scope).toBe('>');
    expect(items.find((i) => i.label === 'Integrations')!.scope).toBe('>');
    expect(items.some((i) => i.label === 'Connections' || i.label === 'Providers')).toBe(false);
  });

  it('reaches every page that left the sidebar (decision P140): the Settings sections, the Agents tabs, All crews, Running, My tasks, the Guide', () => {
    const items = buildItems(rooms, tasks, agents);
    const hrefs = items.map((i) => i.href);
    for (const href of [
      '/agents', '/models', '/routing', '/integrations', '/github', '/security-scan', '/backups', '/data', '/settings', '/settings/shortcuts',
      '/settings/about', '/support', '/license', '/guide', '/library', '/library/starters', '/library/skills', '/crew', '/needs-you?tab=running', '/my-tasks',
    ]) {
      expect([href, hrefs.includes(href)]).toEqual([href, true]);
    }
    expect(filterItems(items, 'security scan').map((i) => i.href)).toContain('/security-scan');
    expect(filterItems(items, 'models')[0]!.href).toBe('/models');
    expect(filterItems(items, 'all crews')[0]!.href).toBe('/crew');
    expect(filterItems(items, 'shortcuts')[0]!.href).toBe('/settings/shortcuts');
  });

  it('matches letters in order as a last resort and drops non-matches', () => {
    const it0 = { id: 'x', group: 'Navigate' as const, label: 'Settings and about' };
    expect(score(it0, 'stab')).toBeGreaterThan(0);
    expect(score(it0, 'zzz')).toBe(0);
    expect(score(it0, 'settings')).toBeGreaterThan(score(it0, 'about'));
  });

  it("names a task by its project's key when the project has one", () => {
    const items = buildItems(rooms, tasks, agents, [], { projectKey: 'GOC' });
    expect(items.find((i) => i.id === 'task:t1')!.label).toBe('GOC-7 Add Multiply');
    expect(filterItems(items, 'goc-7')[0]!.id).toBe('task:t1');
    expect(taskKey(41, 'GOC')).toBe('GOC-41');
    expect(taskKey(41)).toBe('#41');
  });

  it('shows the screens with a G shortcut with their keys', () => {
    const items = buildItems(rooms, tasks, agents);
    expect(items.find((i) => i.label === 'Crew')!.keys).toEqual(['G', 'C']);
    expect(items.find((i) => i.label === 'Tasks')!.keys).toEqual(['G', 'T']);
    expect(items.find((i) => i.label === 'Inbox')!.keys).toEqual(['G', 'I']);
    expect(items.find((i) => i.label === 'Rooms')!.keys).toBe(undefined);
  });
});

describe('the prefixes', () => {
  const items = buildItems(rooms, tasks, agents, [], { projectKey: 'GOC' });

  it('reads the prefix and the words after it', () => {
    expect(parseQuery('> theme')).toEqual({ scope: '>', text: 'theme' });
    expect(parseQuery('#7')).toEqual({ scope: '#', text: '7' });
    expect(parseQuery('@ada')).toEqual({ scope: '@', text: 'ada' });
    expect(parseQuery(' crew ')).toEqual({ text: 'crew' });
  });

  it('">" lists the settings pages and the actions only', () => {
    const all = filterItems(items, '>');
    const labels = all.map((i) => i.label);
    expect(labels).toContain('Model routing');
    expect(labels).toContain('Project settings');
    expect(labels).toContain('Theme: dark');
    expect(labels).toContain('Open a terminal');
    expect(labels).not.toContain('Crew');
    expect(labels).not.toContain('GOC-7 Add Multiply');
    expect(filterItems(items, '> dark')[0]!.label).toBe('Theme: dark');
    // a word that names no setting or action finds nothing behind ">"
    expect(filterItems(items, '> multiply')).toEqual([]);
  });

  it('"#" lists the tasks, by key, number or title', () => {
    expect(filterItems(items, '#').every((i) => i.group === 'Tasks')).toBe(true);
    expect(filterItems(items, '#GOC-7')[0]!.id).toBe('task:t1');
    expect(filterItems(items, '#7')[0]!.id).toBe('task:t1');
    expect(filterItems(items, '#multiply')[0]!.id).toBe('task:t1');
    expect(filterItems(items, '#crew')).toEqual([]);
  });

  it('"@" lists the agents', () => {
    const labels = filterItems(items, '@').map((i) => i.label);
    expect(labels).toEqual(["Open Ada's page", 'Inspect Ada', 'Message Ada']);
    expect(filterItems(items, '@ada')[0]!.label).toBe("Open Ada's page");
  });
});

describe('the options about the task in context', () => {
  const team = [
    { id: 'b1', name: 'Builder', role: 'backend', adapter: 'claude', runMode: 'wsl' },
    { id: 'l1', name: 'Local helper', role: 'frontend', adapter: 'opencode', providerId: 'prov_local' },
    { id: 'lead1', name: 'Tess', role: 'Team-Lead', adapter: 'claude' },
  ] as never[];
  const ctx = (over: Partial<TaskContext> = {}): TaskContext => ({
    task: { id: 't44', seq: 44, title: 'Support hex input', description: 'Parse 0x1F.', projectId: 'p1' },
    projectName: 'gocalc',
    hasRepo: true,
    human: 'human:kyle',
    localIds: new Set(['l1']),
    runtime: (a) => (a.runMode === 'wsl' ? 'Claude Code WSL' : 'Claude Code'),
    askCli: 'Claude Code WSL',
    ...over,
  });

  it('knows the team lead by its role words, as the core does', () => {
    expect(isLeadRole('lead')).toBe(true);
    expect(isLeadRole('Team-Lead')).toBe(true);
    expect(isLeadRole('engineering_manager')).toBe(true);
    expect(isLeadRole('backend')).toBe(false);
    expect(isLeadRole('lead developer')).toBe(false);
  });

  it('offers Assign (each worker with where it runs, the local helper, the lead, me) and the actions', () => {
    const items = contextItems(team, ctx(), 'GOC');
    expect(items.map((i) => [i.group, i.label, i.hint ?? '', (i.keys ?? []).join(' ')])).toEqual([
      ['Assign', 'Assign to Builder', 'Claude Code WSL', ''],
      ['Assign', 'Assign to Local helper', 'Models on this PC', ''],
      ['Assign', 'Ask the team lead to split it', '', '⇧ S'],
      ['Assign', 'Assign to me', '', 'I'],
      ['Actions', 'Scan gocalc for secrets, packages and unsafe code', '', ''],
      ['Actions', 'Ask Claude Code WSL about this task', '', ''],
    ]);
    expect(items[0]!.op).toEqual({ kind: 'assign', assignee: 'b1', agentId: 'b1' });
    expect(items[2]!.op).toEqual({ kind: 'split' });
    expect(items[2]!.agent).toEqual({ id: 'lead1', name: 'Tess' });
    expect(items[3]!.op).toEqual({ kind: 'assign', assignee: 'human:kyle' });
    expect(items[4]!.op).toEqual({ kind: 'scan' });
    expect(items[5]!.op).toEqual({ kind: 'ask' });
    expect(items[5]!.what).toContain('GOC-44');
  });

  it('leaves out what cannot be done: no lead, no human yet, no repository, no CLI', () => {
    const labels = contextItems(team.slice(0, 2), ctx({ human: undefined, hasRepo: false, askCli: undefined })).map((i) => i.label);
    expect(labels).toEqual(['Assign to Builder', 'Assign to Local helper']);
  });

  it('puts a change waiting to be merged first, found by "merge", opening the decision rather than merging (critique 2026-11-08)', () => {
    const items = buildItems(rooms, tasks, team, [], { projectKey: 'GOC', context: ctx({ awaitsMerge: true }) });
    const groups = arrange(filterItems(items, ''), '');
    expect(groups[0]!.group).toBe('Decide');
    const merge = groups[0]!.items[0]!;
    expect(merge.label).toBe('Review and merge GOC-44');
    expect(merge.op).toBe(undefined);
    expect(merge.href).toBe('/task/t44');
    expect(filterItems(items, 'merge')[0]!.id).toBe('merge');
    // nothing waits: no such option
    expect(contextItems(team, ctx(), 'GOC').some((i) => i.id === 'merge')).toBe(false);
  });

  it('lists only the options about the task with nothing typed, grouped Assign, Actions, Go to', () => {
    const items = buildItems(rooms, tasks, team, [], { projectKey: 'GOC', context: ctx() });
    const shown = filterItems(items, '');
    const groups = arrange(shown, '');
    expect(groups.map((g) => g.group)).toEqual(['Assign', 'Actions', 'Go to']);
    expect(groups[2]!.items.map((i) => [i.label, (i.keys ?? []).join(' ')])).toEqual([
      ['Inbox', 'G I'],
      ['Tasks', 'G T'],
      ['Crew', 'G C'],
    ]);
    // a query searches everything, the task's options with the rest
    expect(filterItems(items, 'assign builder')[0]!.label).toBe('Assign to Builder');
    expect(filterItems(items, 'multiply')[0]!.id).toBe('task:t1');
    // "@" finds the assignments to an agent too
    expect(filterItems(items, '@builder').map((i) => i.label)).toContain('Assign to Builder');
  });

  it('groups a query by where its best match ranks, so the first option is the best one', () => {
    const items = buildItems(rooms, tasks, team, [], { context: ctx() });
    const groups = arrange(filterItems(items, 'crew'), 'crew');
    expect(groups[0]!.group).toBe('Go to');
    expect(groups[0]!.items[0]!.label).toBe('Crew');
    const noCtx = arrange(filterItems(buildItems(rooms, tasks, agents), ''), '');
    expect(noCtx.map((g) => g.group)).toEqual(['Navigate', 'Chats', 'Tasks', 'Agents', 'Actions']);
    expect(noCtx.every((g) => g.items.length <= PER_GROUP)).toBe(true);
  });
});

describe('what an assignment does', () => {
  const base: AssignPreview = {
    taskId: 't44',
    assignee: 'b1',
    person: false,
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
    agent: { id: 'b1', name: 'Builder', role: 'backend', adapter: 'claude', model: 'sonnet', runMode: 'wsl', status: 'idle' },
    queue: [{ id: 't43', seq: 43, title: 'Parse octal', status: 'IN_PROGRESS' }],
    waits: true,
    waitReason: 'waits for #43 "Parse octal", which changes the same code (parse.go)',
    waitsFor: { id: 't43', seq: 43, title: 'Parse octal', status: 'IN_PROGRESS' },
  };

  it('lists the worktree branch, the spec gate, the real checks and what it asks before', () => {
    expect(previewSteps(base)).toEqual([
      { text: 'Starts in its own worktree on ', code: 'drydock/t44-support-hex-input' },
      { text: 'Writes the spec first; you accept it at the spec gate' },
      { text: 'Runs tests, lint, types, secrets and deps before review' },
      { text: 'Asks you before it merges' },
    ]);
    const pr = previewSteps({ ...base, specGate: false, checks: ['build', 'ci'], pushes: true, asksBeforePush: true, hasWorktree: true, stopsRun: true });
    expect(pr.map((x) => x.text)).toEqual(['Stops the current turn; Builder takes over in the same worktree', 'Goes on in its worktree on ', 'Runs build and CI before review', 'Asks you before it pushes or merges']);
    expect(previewSteps({ ...base, pushes: true, asksBeforePush: false }).at(-1)!.text).toBe('Pushes after the review; asks you before it merges');
  });

  it("says the agent's queue and what the task waits for, or nothing when it starts at once", () => {
    expect(previewWait(base, 'GOC')).toBe('Builder has 1 task in progress. This one waits until GOC-43 is finished: both change the same code.');
    expect(previewWait({ ...base, queue: [], waitsFor: undefined, waitReason: 'the project is at its limit of 3 parallel agents' })).toBe(
      'This one waits for a free slot: the project is at its limit of 3 parallel agents.',
    );
    expect(previewWait({ ...base, waits: false, waitsFor: undefined })).toBe('');
  });

  it('says when nobody could review the work (the core refuses the assignment then)', () => {
    expect(previewBlock({ ...base, reviewer: '' })).toBe("Nobody else on the team can review Builder's work: add another agent first.");
    expect(previewBlock(base)).toBe('');
    expect(previewBlock({ ...base, person: true, agent: undefined, reviewer: '' })).toBe('');
  });

  it('for the person: no agent works on it', () => {
    const me = previewSteps({ ...base, person: true, agent: undefined });
    expect(me[0]!.text).toBe('No agent works on it: it is yours on the board');
    expect(previewWait({ ...base, person: true })).toBe('');
  });
});

describe('Recent (G10)', () => {
  const items = buildItems(rooms, tasks, agents);

  it('keeps what was run newest first, once, at most RECENT_MAX', () => {
    let list = pushRecent([], 'a', 1);
    list = pushRecent(list, 'b', 2);
    list = pushRecent(list, 'a', 3);
    expect(list).toEqual([
      { id: 'a', at: 3 },
      { id: 'b', at: 2 },
    ]);
    for (let i = 0; i < 20; i++) list = pushRecent(list, `x${i}`, 10 + i);
    expect(list.length).toBe(RECENT_MAX);
    expect(list[0]!.id).toBe('x19');
  });

  it('lists what was run first with nothing typed, drops an id no longer offered, and leaves it out of its own group', () => {
    const shown = filterItems(items, '', 60, ['task:t1', 'gone:x', 'act:theme-dark']);
    const groups = arrange(shown, '');
    expect(groups[0]!.group).toBe('Recent');
    expect(groups[0]!.items.map((i) => [i.id, i.from])).toEqual([
      ['task:t1', 'Tasks'],
      ['act:theme-dark', 'Actions'],
    ]);
    expect(shown.filter((i) => i.id === 'task:t1').length).toBe(1);
    expect(groups.find((g) => g.group === 'Actions')!.items.some((i) => i.id === 'act:theme-dark')).toBe(false);
    // nothing run yet: no Recent group
    expect(arrange(filterItems(items, ''), '')[0]!.group).toBe('Navigate');
  });

  it('ranks a recent option above an equal match while typing, not above a better one', () => {
    expect(filterItems(items, 'theme:')[0]!.id).toBe('act:theme-dark');
    expect(filterItems(items, 'theme:', 60, ['act:theme-system'])[0]!.id).toBe('act:theme-system');
    expect(score(items.find((i) => i.id === 'act:theme-light')!, 'zzz', new Set(['act:theme-light']))).toBe(0);
    // an exact name still wins over a recent partial match
    expect(filterItems(items, 'crew', 60, ['screen:/crew'])[0]!.label).toBe('Crew');
  });

  it('keeps runs of the options about a task out of Recent, but not a Go to over a task', () => {
    const team = [
      { id: 'b1', name: 'Builder', role: 'backend' },
      { id: 'lead1', name: 'Tess', role: 'lead' },
    ] as never[];
    const ctx: TaskContext = {
      task: { id: 't44', seq: 44, title: 'Support hex input', description: '', projectId: 'p1' },
      projectName: 'gocalc',
      hasRepo: true,
      human: 'human:kyle',
      localIds: new Set(),
      runtime: () => 'Claude Code',
      askCli: 'Claude Code',
    };
    const over = buildItems(rooms, tasks, team, [], { context: ctx });
    const ops = over.filter((i) => i.op);
    expect(ops.map((i) => i.id)).toEqual(['assign:b1', 'split', 'assign:me', 'scan', 'ask']);
    expect(ops.some(keepsRecent)).toBe(false);
    const goTo = over.filter((i) => i.group === 'Go to');
    expect(goTo.length > 0 && goTo.every(keepsRecent)).toBe(true);
    // every Go to is offered without a task too, so Recent can list it on the board
    const board = new Set(buildItems(rooms, tasks, team).map((i) => i.id));
    expect(goTo.every((i) => board.has(i.id))).toBe(true);
    expect(items.filter((i) => !i.op).every(keepsRecent)).toBe(true);
  });

  it('remembers runs in the browser, and reads nothing from what does not parse', () => {
    const store = new Map<string, string>();
    const g = globalThis as { localStorage?: Storage };
    const before = g.localStorage;
    g.localStorage = { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v) } as Storage;
    try {
      recordRecent('task:t1', 5);
      recordRecent('act:dock', 6);
      expect(readRecent()).toEqual([
        { id: 'act:dock', at: 6 },
        { id: 'task:t1', at: 5 },
      ]);
      store.set(RECENT_KEY, '{not json');
      expect(readRecent()).toEqual([]);
    } finally {
      g.localStorage = before;
    }
  });
});

describe('the dock\'s options (G6)', () => {
  it('leave out Open a terminal on a page without the dock', () => {
    expect(buildItems(rooms, tasks, agents).some((i) => i.id === 'act:terminal')).toBe(true);
    expect(buildItems(rooms, tasks, agents, [], { dock: false }).some((i) => i.id === 'act:terminal')).toBe(false);
  });
});
