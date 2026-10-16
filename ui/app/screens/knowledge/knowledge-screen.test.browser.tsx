import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { MemoryEntry, Pack, RequirementTrace, TraceTask } from '../../api/extTypes.ts';
import type { AuthorKind, Message, Room } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { byLabel, byText, cleanup, drain, mount, testServices, type, typeKeys, waitFor } from '../../test/render.tsx';
import { KnowledgeScreen } from './knowledge-screen.tsx';

afterEach(cleanup);

const room = (over: Partial<Room>): Room => ({ id: 'r1', projectId: 'prj_1', kind: 'channel', name: 'general', mode: 'auto', archived: false, createdAt: '', version: 1, ...over });
const task = (taskId: string, status: string): TraceTask => ({ taskId, title: taskId, status, linkedVersion: 1, stale: false, criteria: null, evidence: null, testsPassing: false, hasTests: false });
const trace = (id: string, title: string, tasks: TraceTask[] | null, over: Partial<RequirementTrace> = {}): RequirementTrace => ({
  requirement: { id, projectId: 'prj_1', title, source: 'upload', version: 1, contentHash: '', archived: false, created: '', updated: '', chunks: 1, quarantined: 0, links: tasks?.length ?? 0, staleLinks: 0 },
  tasks,
  criteria: 1,
  tasksCompleted: tasks?.filter((x) => x.status === 'COMPLETED').length ?? 0,
  tasksStale: 0,
  verified: false,
  ...over,
});
const note = (over: Partial<MemoryEntry>): MemoryEntry => ({ id: 'm1', projectId: 'prj_1', kind: 'convention', title: 'Money is integer cents', body: '', status: 'accepted', source: 'agt1', created: 1, updated: 1, ...over });
const pack = (id: string, title: string): Pack => ({ id, title, kind: 'docs', visibility: 'project', version: '1', status: 'ready', pages: 1, chunks: 1, quarantined: 0, ageDays: 0, offline: false });
const msg = (seq: number, kind: AuthorKind, type = 'CHAT'): Message => ({ id: `m${seq}`, room_id: 'r_dec', seq, type, author: { kind, id: 'x' }, body: 'b', trust: 'trusted', created_at: '' });

const TRACES = [
  trace('rq1', 'Webhooks are retried with backoff', [task('t12', 'IN_PROGRESS')]),
  trace('rq2', 'Refunds are idempotent', [task('t9', 'COMPLETED')], { verified: true }),
  trace('rq3', 'Prices show in the customer currency', null),
  trace('rq4', 'An old one', [task('t9', 'COMPLETED')], { requirement: { ...trace('rq4', 'x', null).requirement, archived: true } }),
];
const NOTES = [note({ id: 'm1', updated: 30 }), note({ id: 'm2', title: 'Verify webhook signatures', kind: 'api_contract', source: 'human:kyle', updated: 20 }), note({ id: 'm3', title: 'Never log card numbers', status: 'proposed', source: 'importer', updated: 10 })];

function svc(over: Partial<Client> = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc', hasRepo: true }] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    agentList: async () => [{ id: 'agt1', projectId: 'prj_1', name: 'Ada', adapter: 'claude', role: 'backend', model: '', effort: '', providerId: '', permissionMode: '', status: '' }],
    roomList: async () => [room({}), room({ id: 'r_dec', name: 'decisions' })],
    roomUnread: async () => [],
    presenceList: async () => [],
    needsYouList: async () => [],
    taskList: async () => [{ id: 't12', seq: 12, status: 'IN_PROGRESS' }, { id: 't9', seq: 9, status: 'COMPLETED' }] as never,
    requirementCoverage: async () => TRACES,
    packList: async () => [pack('p1', 'Stripe API'), pack('p2', 'Go style')],
    memoryList: async () => NOTES,
    repoMapGet: async () => ({ available: true, languages: ['go', 'typescript'], result: { files: 214, languages: { go: 200, typescript: 14 } } }) as never,
    roomMessagesLatest: async () => [msg(1, 'human'), msg(2, 'agent', 'DECISION'), msg(3, 'system', 'SYSTEM')],
    ...over,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

const card = (r: { $: (s: string) => Element | null }, id: string) => r.$(`[data-testid="${id}"]`) as HTMLAnchorElement;

describe('the Knowledge hub', () => {
  it('shows each source with its real count and the words under it, each a link to its page', async () => {
    const r = mount(<KnowledgeScreen />, svc());
    await waitFor(() => expect(card(r, 'knowledge-requirements').getAttribute('data-count')).toBe('3'), r);
    expect(r.$('h1')!.textContent).toContain('Knowledge');
    expect(r.container.textContent).toContain('What the crew knows about gocalc');
    expect(card(r, 'knowledge-requirements').textContent).toContain('1 covered by tasks');
    expect(card(r, 'knowledge-requirements').getAttribute('href')).toBe(routes.requirements.href());
    await waitFor(() => expect(card(r, 'knowledge-packs').getAttribute('data-count')).toBe('2'), r);
    expect(card(r, 'knowledge-packs').textContent).toContain('Stripe API, Go style');
    await waitFor(() => expect(card(r, 'knowledge-repo-map').getAttribute('data-count')).toBe('214'), r);
    expect(card(r, 'knowledge-repo-map').textContent).toContain('files mapped · go 200, typescript 14');
    await waitFor(() => expect(card(r, 'knowledge-memory').getAttribute('data-count')).toBe('3'), r);
    expect(card(r, 'knowledge-memory').textContent).toContain('2 accepted · 1 waiting for you');
    await waitFor(() => expect(card(r, 'knowledge-decisions').getAttribute('data-count')).toBe('2'), r);
    expect(card(r, 'knowledge-decisions').getAttribute('href')).toBe(routes.room.href({ id: 'r_dec' }));
    expect(card(r, 'knowledge-decisions').textContent).toContain('agreed in decisions');
  });

  it('lists the requirements with how far a task covers each, the chip a link to that task; archived ones are left out', async () => {
    const r = mount(<KnowledgeScreen />, svc());
    const rows = await waitFor(() => {
      const list = r.$$('[data-testid="knowledge-requirement"]');
      if (list.length !== 3) throw new Error(`have ${list.length}`);
      return Array.from(list);
    }, r);
    expect(rows.map((x) => x.getAttribute('data-coverage'))).toEqual(['in progress', 'verified', 'no task yet']);
    const chips = rows.map((x) => x.querySelector('[data-testid="knowledge-coverage"]')!);
    expect(chips.map((x) => x.textContent?.trim())).toEqual(['in progress · #12', 'verified · #9', 'no task yet']);
    expect(chips[0]!.getAttribute('href')).toBe(routes.task.href({ id: 't12' }));
    expect(chips[2]!.tagName).toBe('SPAN');
    expect(rows[0]!.textContent).toContain('Webhooks are retried with backoff');
    expect(r.container.textContent).not.toContain('An old one');
  });

  it('shows the latest notes with who wrote them: an agent as its dot, a person as initials, a proposal marked', async () => {
    const r = mount(<KnowledgeScreen />, svc());
    const rows = await waitFor(() => {
      const list = r.$$('[data-testid="knowledge-note"]');
      if (list.length !== 3) throw new Error(`have ${list.length}`);
      return Array.from(list);
    }, r);
    expect(rows[0]!.querySelector('[data-dot="true"]')).not.toBeNull();
    expect(rows[0]!.textContent).toContain('Money is integer cents');
    expect(rows[0]!.textContent).toContain('Ada · convention');
    expect(rows[1]!.querySelector('[data-kind="human"]')).not.toBeNull();
    expect(rows[1]!.textContent).toContain('kyle · api contract');
    expect(rows[2]!.getAttribute('data-status')).toBe('proposed');
    expect(rows[2]!.textContent).toContain('proposed');
    expect(rows[2]!.querySelector('[data-dot="true"]')).toBeNull();
  });

  it('a question starts a chat in the project, sends it and opens the chat', async () => {
    const calls: unknown[] = [];
    const gone: string[] = [];
    const r = mount(
      <KnowledgeScreen onNavigate={(h) => void gone.push(h)} />,
      svc({
        chatCreate: async (p) => (calls.push(['create', p]), { id: 'chat_1' }) as never,
        chatSend: async (id, text) => (calls.push(['send', id, text]), { turnId: 'tr1', version: 1 }),
      }),
    );
    await waitFor(() => byLabel(r.container, 'Ask anything about gocalc'), r);
    type(byLabel(r.container, 'Ask anything about gocalc'), 'where do we round currency?');
    expect(r.container.textContent).toContain("Starts a Claude Code chat in the project's folder");
    await r.act(() => byText(r.container, 'button', 'Ask').click());
    await waitFor(() => expect(gone).toEqual([routes.chat.href({ id: 'chat_1' })]), r);
    expect(calls).toEqual([
      ['create', { projectId: 'prj_1' }],
      ['send', 'chat_1', 'where do we round currency?'],
    ]);
    expect(byLabel(r.container, 'Ask anything about gocalc').value).toBe('');
  });

  it('keeps every key typed into the ask box, and asks the whole question (H12)', async () => {
    const calls: unknown[] = [];
    const r = mount(
      <KnowledgeScreen onNavigate={() => {}} />,
      svc({
        chatCreate: async () => ({ id: 'chat_k' }) as never,
        chatSend: async (id, text) => (calls.push([id, text]), { turnId: 'tr1', version: 1 }),
      }),
    );
    const box = await waitFor(() => byLabel(r.container, 'Ask anything about gocalc'), r);
    await typeKeys(box, 'where do we round currency?', r);
    expect(box.value).toBe('where do we round currency?');
    await r.act(() => byText(r.container, 'button', 'Ask').click());
    await waitFor(() => expect(calls).toEqual([['chat_k', 'where do we round currency?']]), r);
  });

  it('an empty question starts nothing; a question that could not be sent stays with the reason and a link to the chat', async () => {
    const calls: string[] = [];
    const gone: string[] = [];
    const r = mount(
      <KnowledgeScreen onNavigate={(h) => void gone.push(h)} />,
      svc({
        chatCreate: async () => (calls.push('create'), { id: 'chat_2' }) as never,
        chatSend: async () => {
          calls.push('send');
          throw new Error('the CLI is not signed in');
        },
      }),
    );
    await waitFor(() => byLabel(r.container, 'Ask anything about gocalc'), r);
    type(byLabel(r.container, 'Ask anything about gocalc'), '   ');
    await r.act(() => byText(r.container, 'button', 'Ask').click());
    await drain();
    expect(calls).toEqual([]);
    type(byLabel(r.container, 'Ask anything about gocalc'), 'why?');
    await r.act(() => (r.$('[data-testid="knowledge-ask"]') as HTMLFormElement).requestSubmit());
    await waitFor(() => expect(r.$('[data-testid="knowledge-ask-unsent"]')).not.toBeNull(), r);
    expect(calls).toEqual(['create', 'send']);
    expect(gone).toEqual([]);
    const notice = r.$('[data-testid="knowledge-ask-unsent"]')!;
    expect(notice.textContent).toContain('the CLI is not signed in');
    expect(notice.querySelector('a')!.getAttribute('href')).toBe(routes.chat.href({ id: 'chat_2' }));
  });

  it('with nothing yet, the panels say so; a read that fails is said on its card and in an alert', async () => {
    const r = mount(
      <KnowledgeScreen />,
      svc({
        requirementCoverage: async () => [],
        memoryList: async () => [],
        packList: async () => {
          throw new Error('packs are off');
        },
      }),
    );
    await waitFor(() => expect(card(r, 'knowledge-requirements').getAttribute('data-count')).toBe('0'), r);
    expect(r.$('[data-testid="knowledge-requirements-panel"]')!.textContent).toContain('No requirements yet.');
    await waitFor(() => expect(r.$('[data-testid="knowledge-learned"]')!.textContent).toContain('Nothing yet.'), r);
    await waitFor(() => expect(card(r, 'knowledge-packs').textContent).toContain('could not be read'), r);
    expect(card(r, 'knowledge-packs').getAttribute('data-count')).toBe(null);
  });
});
