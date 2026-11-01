import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { AgentTemplate } from '../../api/libraryClient.ts';
import type { Agent, Project, SetupRequest } from '../../api/types.ts';
import { projectWorkHref } from '../../lib/project-work.ts';
import { CATALOG } from '../project/wizard-fixtures.ts';
import {
  chatSettings,
  crewFor,
  crewKinds,
  crewLabel,
  crewMembers,
  defaultTuning,
  firstWork,
  folderName,
  newFolderPath,
  pickPair,
  planStart,
  projectName,
  recall,
  remember,
  runJustChat,
  runStart,
  slugify,
  START_KEY,
  StartError,
  taskTitle,
  TASK_TITLE_MAX,
  whereKey,
  type Crew,
  type StartClient,
  type StartPlan,
  type Tuning,
} from './start.ts';

const solo: Crew = { kind: 'solo', templates: [], drafted: [] };
const team: Crew = { kind: 'team', templates: [], drafted: [] };
const tuning: Tuning = { model: 'opus', effort: 'high', permission: 'default' };
const project = (id: string, over: Partial<Project> = {}): Project => ({ id, name: id, repoPath: `/src/${id}`, hasRepo: true, defaultBranch: 'main', mergeStrategy: 'merge', mode: 'local_merge', created: 1, ...over });
const agent = (id: string, role: string, over: Partial<Agent> = {}): Agent => ({ id, name: id, role, adapter: 'claude', model: 'sonnet', effort: 'medium', permissionMode: 'acceptEdits', ...over }) as Agent;
const template = (id: string, name: string, role: string): AgentTemplate =>
  ({ id, name, role, purpose: `You are ${name}.`, adapter: 'claude', model: 'haiku', effort: 'low', permissionMode: '', providerId: '', mcpServers: [], skills: [], createdAt: 0, updatedAt: 0 }) as AgentTemplate;

/** A LABELLED TEST DOUBLE of the client calls a start makes: it records them and answers like the core. */
function fakeClient(o: { agents?: Agent[]; hasRepo?: boolean; failStart?: boolean } = {}) {
  const calls: string[] = [];
  const sent: Record<string, unknown> = {};
  const c: StartClient = {
    projectSetup: async (req: SetupRequest) => {
      calls.push(`setup:${req.repo.kind}`);
      sent.setup = req;
      const agents = req.agents.map((a, i) => agent(`a${i}`, a.role ?? (a.prebuilt === 'team-lead' ? 'lead' : a.prebuilt === 'code-reviewer' ? 'reviewer' : 'backend'), { model: a.model ?? '', effort: a.effort ?? '', permissionMode: a.permissionMode ?? '' }));
      return { project: { ...project('p1'), name: req.name, hasRepo: o.hasRepo ?? req.repo.kind !== 'none' }, agents, warnings: [] };
    },
    agentList: async (id) => (calls.push(`agents:${id}`), o.agents ?? []),
    taskCreate: async (req) => {
      calls.push(`task:${req.title}`);
      sent.task = req;
      return { id: 't1' } as never;
    },
    taskAssign: async (id, w, r) => (calls.push(`assign:${w}/${r}`), { id } as never),
    taskStart: async () => {
      if (o.failStart) throw new Error('the license is read-only');
      calls.push('start');
      return 'run1';
    },
    chatCreate: async (p) => {
      calls.push('chat');
      sent.chat = p;
      return { id: 'c1' } as never;
    },
    chatSend: async (_id, text) => (calls.push(`send:${text}`), {}),
  };
  return { c, calls, sent };
}

const plan = (o: Partial<Parameters<typeof planStart>[0]>): StartPlan => {
  const p = planStart({ where: { kind: 'new', path: '/code/csv-json' }, text: 'A small Go CLI that converts CSV to JSON', crew: solo, tuning, catalog: CATALOG, templates: [], projects: [], ...o });
  if ('problem' in p) throw new Error(p.problem);
  return p;
};

describe('the start screen: names and folders (P142)', () => {
  it('makes a folder name of the first words that say something', () => {
    expect(slugify('A small Go CLI that converts CSV to JSON')).toBe('go-cli-converts');
    expect(slugify('Add a currency converter page to the calculator app')).toBe('currency-converter-page');
    expect(slugify('Café crème!')).toBe('cafe-creme');
    expect(slugify('   ')).toBe('new-project');
    expect(slugify('a the to')).toBe('a-the-to');
  });

  it('puts the new folder in the usual code folder, with its separator', () => {
    expect(newFolderPath('C:\\Users\\me\\source\\repos', 'Go CLI converts CSV')).toBe('C:\\Users\\me\\source\\repos\\go-cli-converts');
    expect(newFolderPath('/home/me/source/repos/', 'csv2json tool')).toBe('/home/me/source/repos/csv2json-tool');
    expect(newFolderPath('', 'x')).toBe('');
    expect(folderName('C:\\code\\csv2json\\')).toBe('csv2json');
  });

  it("names the project after its folder or repository, else the description's first words", () => {
    expect(projectName({ kind: 'new', path: '/code/csv2json' }, 'whatever')).toBe('csv2json');
    expect(projectName({ kind: 'folder', path: 'D:\\src\\calc', init: false }, 'x')).toBe('calc');
    expect(projectName({ kind: 'clone', repo: 'acme/billing-api', cloneDir: '/c' }, 'x')).toBe('billing-api');
    expect(projectName({ kind: 'project', projectId: 'p' }, 'Fix the flaky login test today')).toBe('Fix the flaky login');
  });

  it('titles the task with the first sentence, cut at a word within the core limit', () => {
    expect(taskTitle('Add Multiply to the calculator. Then add tests.')).toBe('Add Multiply to the calculator');
    expect(taskTitle('Is the build green?\nCheck CI.')).toBe('Is the build green?');
    const long = taskTitle(`${'word '.repeat(60)}end.`);
    expect(long.endsWith('…')).toBe(true);
    expect(long.length).toBeLessThanOrEqual(91);
    const wide = taskTitle('漢'.repeat(400));
    expect(new TextEncoder().encode(wide).length).toBeLessThanOrEqual(TASK_TITLE_MAX);
  });
});

describe('the start screen: crews (P142)', () => {
  it('offers a crew made for a new place, and the project crew or Solo in a project', () => {
    expect(crewKinds({ kind: 'new', path: '' })).toEqual(['solo', 'team', 'mine']);
    expect(crewKinds({ kind: 'project', projectId: 'p' })).toEqual(['project', 'solo']);
    expect(crewFor({ kind: 'project', projectId: 'p' }, 'team')).toBe('project');
    expect(crewFor({ kind: 'new', path: '' }, 'team')).toBe('team');
    expect(crewFor({ kind: 'new', path: '' })).toBe('solo');
  });

  it("takes the builder's model and effort as the chips' first values; a new agent edits freely", () => {
    expect(defaultTuning(CATALOG)).toEqual({ model: 'sonnet', effort: 'medium', permission: 'acceptEdits' });
    expect(defaultTuning([])).toEqual({ model: '', effort: '', permission: 'acceptEdits' });
  });

  it('Solo is the builder with the chips; Small team the suggested three with the chips on the builder only', () => {
    const one = crewMembers(solo, CATALOG, [], tuning);
    expect(one.map((m) => [m.ref, m.model, m.effort, m.permissionMode])).toEqual([['backend-engineer', 'opus', 'high', 'default']]);
    const three = crewMembers(team, CATALOG, [], tuning);
    expect(three.map((m) => m.ref)).toEqual(['team-lead', 'backend-engineer', 'code-reviewer']);
    expect(three.find((m) => m.ref === 'code-reviewer')!.model).toBe('opus');
    expect(three.find((m) => m.ref === 'backend-engineer')!.permissionMode).toBe('default');
    // no catalog: a plain builder on the CLI given
    expect(crewMembers(solo, [], [], tuning, 'codex').map((m) => [m.source, m.role, m.adapter])).toEqual([['custom', 'backend', 'codex']]);
  });

  it('My agents are templates; a drafted agent joins any crew', () => {
    const mine: Crew = {
      kind: 'mine',
      templates: ['t1', 'gone'],
      drafted: [{ name: 'Docs Writer', role: 'docs', adapter: 'claude', model: 'haiku', effort: 'low', permissionMode: 'plan', purpose: 'Writes docs.', tools: ['ctx'], confirmed: '' }],
    };
    const m = crewMembers(mine, CATALOG, [template('t1', 'Reviewer Rae', 'reviewer')], tuning);
    expect(m.map((x) => [x.source, x.ref ?? '', x.name])).toEqual([
      ['template', 't1', 'Reviewer Rae'],
      ['custom', '', 'Docs Writer'],
    ]);
    const p = plan({ crew: mine, templates: [template('t1', 'Reviewer Rae', 'reviewer')] });
    expect(p.setup!.agents).toEqual([
      { template: 't1', name: 'Reviewer Rae' },
      { name: 'Docs Writer', adapter: 'claude', model: 'haiku', effort: 'low', permissionMode: 'plan', role: 'docs', purpose: 'Writes docs.', servers: ['ctx'] },
    ]);
    // the chips set nothing on saved agents
    expect(p.tuning).toBeUndefined();
  });

  it("labels the crew chip", () => {
    expect(crewLabel(solo, { adapter: 'claude', catalog: CATALOG })).toEqual({ name: 'Solo', detail: 'Claude Code' });
    expect(crewLabel(team, { adapter: 'claude', catalog: CATALOG })).toEqual({ name: 'Small team', detail: '3 agents' });
    expect(crewLabel({ kind: 'project', templates: [], drafted: [] }, { adapter: 'claude', catalog: CATALOG, projectAgents: 1 })).toEqual({ name: 'Project crew', detail: '1 agent' });
  });

  it('pairs a worker with a different reviewer, never the lead', () => {
    expect(pickPair([agent('l', 'lead'), agent('b', 'backend'), agent('r', 'reviewer')])).toEqual({ worker: 'b', reviewer: 'r' });
    expect(pickPair([agent('r', 'reviewer'), agent('f', 'frontend'), agent('b', 'backend')])).toEqual({ worker: 'f', reviewer: 'r' });
    expect(pickPair([agent('a', 'backend'), agent('b', 'frontend')])).toEqual({ worker: 'a', reviewer: 'b' });
    expect(pickPair([agent('l', 'lead'), agent('b', 'backend')])).toBeUndefined();
    expect(pickPair([agent('b', 'backend'), agent('r', 'reviewer', { archived: true } as never)])).toBeUndefined();
  });
});

describe('what Enter does (P142)', () => {
  const crew3 = [agent('l', 'lead'), agent('b', 'backend'), agent('r', 'reviewer')];

  it('a task with a repository and a pair; a chat for Solo, without a repository, or without a pair', () => {
    expect(firstWork('team', true, crew3)).toEqual({ kind: 'task', worker: 'b', reviewer: 'r' });
    expect(firstWork('project', true, crew3)).toEqual({ kind: 'task', worker: 'b', reviewer: 'r' });
    expect(firstWork('solo', true, crew3)).toEqual({ kind: 'chat' });
    expect(firstWork('team', false, crew3)).toEqual({ kind: 'chat' });
    expect(firstWork('mine', true, [agent('b', 'backend')])).toEqual({ kind: 'chat' });
  });

  it('says what is missing before anything is sent', () => {
    const p = (o: Partial<Parameters<typeof planStart>[0]>) => planStart({ where: { kind: 'new', path: '/c/x' }, text: 'Build it', crew: solo, tuning, catalog: CATALOG, templates: [], projects: [], ...o });
    expect(p({ text: '  ' })).toEqual({ problem: 'Describe what you want to build.' });
    expect(p({ where: { kind: 'new', path: '' } })).toEqual({ problem: 'Choose the new folder.' });
    expect(p({ where: { kind: 'clone', repo: '', cloneDir: '/c' } })).toEqual({ problem: 'Choose a repository to clone.' });
    expect(p({ where: { kind: 'project', projectId: 'gone' } })).toEqual({ problem: 'That project is gone: choose another place.' });
    expect(p({ text: 'x'.repeat(8001) })).toEqual({ problem: 'Keep it under 8000 characters.' });
  });

  it('a new folder: one project.setup with the wizard defaults (a new repository, local merge, no connectors, skills)', () => {
    const p = plan({});
    expect(p.setup).toEqual({
      name: 'csv-json',
      brief: 'A small Go CLI that converts CSV to JSON',
      repo: { kind: 'local', path: '/code/csv-json', init: true },
      mode: 'local_merge',
      libraryServers: [],
      catalogServers: [],
      agents: [{ prebuilt: 'backend-engineer', name: 'Backend Engineer', adapter: 'claude', model: 'opus', effort: 'high', permissionMode: 'default' }],
      installSkills: true,
    });
    expect(p.tuning).toEqual(tuning);
  });

  it('an opened folder and a clone: their repository source', () => {
    expect(plan({ where: { kind: 'folder', path: '/src/calc', init: false } }).setup!.repo).toEqual({ kind: 'local', path: '/src/calc' });
    expect(plan({ where: { kind: 'folder', path: '/src/notes', init: true } }).setup!.repo).toEqual({ kind: 'local', path: '/src/notes', init: true });
    const c = plan({ where: { kind: 'clone', repo: 'acme/billing-api', cloneDir: '/repos' } }).setup!;
    expect([c.name, c.repo]).toEqual(['billing-api', { kind: 'github', github: 'acme/billing-api', cloneDir: '/repos' }]);
  });

  it('new folder + Small team: makes the project, then a task assigned to the builder and the reviewer, and starts it', async () => {
    const { c, calls, sent } = fakeClient();
    const steps: string[] = [];
    const out = await runStart(c, plan({ crew: team, text: 'Add Multiply. With tests.' }), (s) => steps.push(s));
    expect(steps).toEqual(['Creating project…', 'Starting task…']);
    expect(calls).toEqual(['setup:local', 'task:Add Multiply', 'assign:a1/a2', 'start']);
    expect(sent.task).toEqual({ projectId: 'p1', title: 'Add Multiply', description: 'Add Multiply. With tests.' });
    expect(out.item).toEqual({ kind: 'task', id: 't1' });
    expect(projectWorkHref(out.item)).toBe('/?item=task:t1');
  });

  it('new folder + Solo: makes the project, then a chat in it on the chips, sent the description', async () => {
    const { c, calls, sent } = fakeClient();
    const out = await runStart(c, plan({}), () => {});
    expect(calls).toEqual(['setup:local', 'chat', 'send:A small Go CLI that converts CSV to JSON']);
    expect(sent.chat).toEqual({ projectId: 'p1', adapter: 'claude', model: 'opus', effort: 'high', permissionMode: 'default' });
    expect(projectWorkHref(out.item)).toBe('/?item=chat:c1');
  });

  it("a recent project with its crew: no setup, a task with the project's pair", async () => {
    const { c, calls } = fakeClient({ agents: crew3 });
    const p = plan({ where: { kind: 'project', projectId: 'p7' }, crew: { kind: 'project', templates: [], drafted: [] }, projects: [project('p7')], text: 'Fix the login bug' });
    expect(p.setup).toBeUndefined();
    expect(p.tuning).toBeUndefined();
    await runStart(c, p, () => {});
    expect(calls).toEqual(['agents:p7', 'task:Fix the login bug', 'assign:b/r', 'start']);
  });

  it("a recent project without a repository: a chat with its worker's settings", async () => {
    const { c, calls, sent } = fakeClient({ agents: crew3 });
    const p = plan({ where: { kind: 'project', projectId: 'p8' }, crew: { kind: 'project', templates: [], drafted: [] }, projects: [project('p8', { hasRepo: false, repoPath: '' })], text: 'Plan the API' });
    await runStart(c, p, () => {});
    expect(calls).toEqual(['agents:p8', 'chat', 'send:Plan the API']);
    expect(sent.chat).toEqual({ projectId: 'p8', adapter: 'claude', model: 'sonnet', effort: 'medium', permissionMode: 'acceptEdits' });
  });

  it('a recent project + Solo: a chat on the chips', async () => {
    const { c, calls } = fakeClient({ agents: crew3 });
    await runStart(c, plan({ where: { kind: 'project', projectId: 'p7' }, crew: solo, projects: [project('p7')], text: 'What does main.go do?' }), () => {});
    expect(calls).toEqual(['agents:p7', 'chat', 'send:What does main.go do?']);
  });

  it('a failure after the project exists names it, so the next try works there', async () => {
    const { c } = fakeClient({ failStart: true });
    const err = await runStart(c, plan({ crew: team }), () => {}).catch((e: unknown) => e);
    expect(err instanceof StartError).toBe(true);
    expect((err as StartError).projectId).toBe('p1');
    expect((err as StartError).message).toBe('the license is read-only');
  });

  it('Just chat: a chat without a project, sent the text when there is one', async () => {
    const { c, calls, sent } = fakeClient();
    expect(await runJustChat(c, '  hello  ', { adapter: 'claude', model: 'opus' })).toBe('c1');
    expect(calls).toEqual(['chat', 'send:hello']);
    expect(sent.chat).toEqual({ adapter: 'claude', model: 'opus' });
    const empty = fakeClient();
    await runJustChat(empty.c, '', {});
    expect(empty.calls).toEqual(['chat']);
  });

  it("a chat's settings leave out what is not set", () => {
    expect(chatSettings({ model: '', effort: 'low', permission: 'plan' }, 'claude', [])).toEqual({ adapter: 'claude', effort: 'low', permissionMode: 'plan' });
    expect(chatSettings(undefined, 'claude', [])).toEqual({});
  });
});

describe('remembered choices (P142)', () => {
  const memory = () => {
    const m = new Map<string, string>();
    return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), m };
  };

  it('keeps the last choices per place and reads them back', () => {
    const st = memory();
    expect(recall(st, 'new')).toBeUndefined();
    remember(st, ['new', 'project:p1'], { crew: 'team', templates: [], model: 'opus', effort: 'high', permission: 'plan' });
    expect(recall(st, 'new')).toEqual({ crew: 'team', templates: [], model: 'opus', effort: 'high', permission: 'plan' });
    expect(recall(st, 'project:p1')!.crew).toBe('team');
    remember(st, ['project:p1'], { crew: 'project', templates: [], model: '', effort: '', permission: 'default' });
    expect(recall(st, 'project:p1')!.crew).toBe('project');
    expect(recall(st, 'new')!.crew).toBe('team');
  });

  it('survives a broken or missing store', () => {
    const st = memory();
    st.m.set(START_KEY, '{not json');
    expect(recall(st, 'new')).toBeUndefined();
    st.m.set(START_KEY, JSON.stringify({ new: { crew: 'nobody' } }));
    expect(recall(st, 'new')).toBeUndefined();
    const throwing = { getItem: () => { throw new Error('blocked'); }, setItem: () => { throw new Error('blocked'); } };
    expect(recall(throwing, 'new')).toBeUndefined();
    remember(throwing, ['new'], { crew: 'solo', templates: [], model: '', effort: '', permission: '' });
    remember(undefined, ['new'], { crew: 'solo', templates: [], model: '', effort: '', permission: '' });
  });

  it('keys a place by what it is', () => {
    expect(whereKey({ kind: 'new', path: '/x' })).toBe('new');
    expect(whereKey({ kind: 'folder', path: ' /src/a ', init: false })).toBe('folder:/src/a');
    expect(whereKey({ kind: 'clone', repo: 'a/b', cloneDir: '' })).toBe('clone:a/b');
    expect(whereKey({ kind: 'project', projectId: 'p1' })).toBe('project:p1');
  });
});
