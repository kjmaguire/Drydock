// Decision P60: a chat's properties (agent CLI, folder, library skills, library MCP servers) in the new chat dialog and in the
// Properties dialog of an open chat, and the approvals of a chat's MCP tools. The client is a partial TEST DOUBLE.

import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Chat, ChatListItem, ChatUpdate } from '../../api/chatClient.ts';
import type { LibrarySkill } from '../../api/extTypes.ts';
import type { McpApproval, McpLibraryServer } from '../../api/mcpClient.ts';
import type { AdapterInfo, Project } from '../../api/types.ts';
import { lic } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { ChatsScreen } from './chats-screen.tsx';

afterEach(cleanup);

// --- plain data fixtures (no behavior) ---

const chat = (over: Partial<Chat> = {}): Chat => ({
  id: 'chat_a',
  projectId: 'prj_1',
  title: 'Notes',
  adapter: 'claude',
  model: '',
  effort: '',
  permissionMode: 'default',
  skipsApprovals: false,
  cwd: 'C:\\repos\\gocalc',
  sessionId: 'sess-1',
  allowedTools: [],
  readHistory: true,
  skills: [],
  mcpServers: [],
  status: 'idle',
  lastError: '',
  pinned: false,
  archived: false,
  archivedAt: 0,
  forkedFrom: '',
  createdAt: Date.now() - 3_600_000,
  updatedAt: Date.now() - 120_000,
  tokensIn: 0,
  tokensOut: 0,
  costUsd: null,
  costUnknownTurns: 0,
  version: 1,
  ...over,
});
const item = (c: Chat): ChatListItem => ({ ...c, preview: '', messageCount: 0 });
const claude: AdapterInfo = {
  id: 'claude',
  detection: { adapter: 'claude', installed: true, status: 'ready', version: '2.1.296' },
  capabilities: { modelSelection: true, efforts: ['low', 'high'], models: ['sonnet'], permissionModes: ['default', 'acceptEdits'] } as AdapterInfo['capabilities'],
};
const libSkill = (name: string, over: Partial<LibrarySkill> = {}): LibrarySkill => ({
  id: `lib_${name}`,
  registryId: 'cloudflare-skills',
  repo: 'cloudflare/skills',
  name,
  description: `Stand-in ${name} rules. More text after.`,
  license: 'MIT',
  globs: [],
  roles: [],
  sourcePath: `skills/${name}`,
  pinnedCommit: 'abc',
  contentHash: 'h',
  hasScripts: false,
  status: 'available',
  blockReason: '',
  risk: 0,
  flags: [],
  gone: false,
  updated: '',
  ...over,
});
const server = (id: string, display: string, tools: McpLibraryServer['tools']): McpLibraryServer => ({
  id,
  display,
  transport: 'stdio',
  secretEnv: [],
  hasToken: false,
  guard: true,
  readOnlyTools: [],
  tools,
  discoveredAt: 1,
  projects: [],
  createdAt: 1,
  updatedAt: 1,
});
const library = [libSkill('wrangler'), libSkill('durable-objects'), libSkill('blocked-one', { status: 'blocked', blockReason: 'no license' })];
const servers = [
  server('tst', 'Test server', [
    { name: 'echo', readOnlyHint: true },
    { name: 'envlen', readOnlyHint: false },
    { name: 'format', readOnlyHint: false, flags: ['instructions'] },
  ]),
  server('docs', 'Docs', [{ name: 'search', readOnlyHint: true }]),
];

type R = ReturnType<typeof mount>;
const doc$$ = (sel: string) => Array.from(document.querySelectorAll<HTMLElement>(sel));
function must<T>(x: T | null | undefined, what: string): T {
  if (x === null || x === undefined) throw new Error(`missing ${what}`);
  return x;
}
const must$ = (r: R, sel: string): HTMLElement => must(r.container.querySelector<HTMLElement>(sel), sel);
const openDialogWith = (testId: string) => must(doc$$('dialog[open]').find((d) => d.querySelector(`[data-testid="${testId}"]`)), testId);
const option = (root: ParentNode, kind: 'skill' | 'mcp', name: string) =>
  must(root.querySelector<HTMLElement>(kind === 'skill' ? `[data-testid="chat-skill-option"][data-name="${name}"]` : `[data-testid="chat-mcp-option"][data-server="${name}"]`), `${kind} ${name}`);
const box = (root: ParentNode, kind: 'skill' | 'mcp', name: string) => must(option(root, kind, name).querySelector<HTMLInputElement>('input[type="checkbox"]'), 'checkbox');

function setup(client: Partial<Client>, props: { id?: string; newChat?: boolean } = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as Project],
    licenseStatus: async () => lic(),
    appInfo: async () => ({ name: 'Drydock', user: 'u', human: 'h', dataDir: '/d' }),
    adapterList: async () => [claude],
    connectionList: async () => [],
    chatList: async () => [],
    skillLibraryList: async () => library,
    mcpLibList: async () => servers,
    mcpApprovalList: async () => [],
    ...client,
  });
  s.workspace.start();
  if (props.newChat) s.ui.requestNewChat();
  const r = mount(<ChatsScreen id={props.id} onNavigate={() => {}} />, s);
  return { s, r };
}

async function openProperties(r: R) {
  const btn = await waitFor(() => byText(r.container, 'button', 'Properties'), r);
  await r.act(() => btn.click());
  return waitFor(() => openDialogWith('chat-properties-form'), r);
}

describe('Chat properties (decision P60)', () => {
  it('the new chat dialog picks library skills and MCP servers and sends them on create', async () => {
    const created: unknown[] = [];
    const { r } = setup({ chatCreate: async (p) => (created.push(p), chat({ id: 'c_p' })) }, { newChat: true });
    const dlg = await waitFor(() => openDialogWith('new-chat-form'), r);
    await waitFor(() => option(dlg, 'skill', 'wrangler'), r);
    // a blocked skill is not offered; the server's flagged tool is not counted
    expect(dlg.querySelector('[data-testid="chat-skill-option"][data-name="blocked-one"]')).toBeNull();
    expect(option(dlg, 'skill', 'wrangler').textContent).toContain('Stand-in wrangler rules.');
    expect(option(dlg, 'skill', 'wrangler').textContent).not.toContain('More text after');
    await waitFor(() => expect(option(dlg, 'mcp', 'tst').textContent).toContain('2 tools'), r);
    await r.act(() => box(dlg, 'skill', 'wrangler').click());
    await r.act(() => box(dlg, 'mcp', 'tst').click());
    await waitFor(() => expect(must(dlg.querySelector('[data-testid="chat-skills-count"]'), 'count').textContent).toBe('1 chosen'), r);
    // the filter narrows the list; a chosen skill stays in view
    type(byLabel(dlg, 'Filter skills'), 'durable');
    await waitFor(() => expect(Array.from(dlg.querySelectorAll('[data-testid="chat-skill-option"]')).map((e) => e.getAttribute('data-name'))).toEqual(['wrangler', 'durable-objects']), r);
    await r.act(() => byText(dlg, 'button', 'Start chat').click());
    await waitFor(() => expect(created).toHaveLength(1), r);
    expect(created[0]).toEqual({
      projectId: 'prj_1',
      cwd: undefined,
      adapter: 'claude',
      model: undefined,
      effort: undefined,
      permissionMode: undefined,
      readHistory: true,
      skills: [{ registryId: 'cloudflare-skills', name: 'wrangler' }],
      mcpServers: [{ serverId: 'tst', tools: [] }],
    });
  });

  it("an open chat's Properties change its skills and servers, sending only what changed, and its chips show them", async () => {
    const updates: ChatUpdate[] = [];
    let c = chat({ projectId: '', cwd: '/data/chats/chat_a', skills: [{ registryId: 'cloudflare-skills', name: 'wrangler' }], mcpServers: [{ serverId: 'tst', tools: ['echo'] }] });
    const { r } = setup(
      {
        chatList: async () => [item(c)],
        chatGet: async () => ({ chat: c, messages: [], hasMore: false }),
        chatUpdate: async (u) => {
          updates.push(u);
          c = { ...c, ...u, version: c.version + 1 } as Chat;
          return c;
        },
      },
      { id: 'chat_a' },
    );
    await waitFor(() => expect(must$(r, '[data-testid="chip-skills"]').textContent).toBe('1 skill'), r);
    expect(must$(r, '[data-testid="chip-mcp"]').textContent).toBe('1 MCP server');
    const dlg = await openProperties(r);
    await waitFor(() => expect(box(dlg, 'skill', 'wrangler').checked).toBe(true), r);
    expect(box(dlg, 'mcp', 'tst').checked).toBe(true);
    expect(option(dlg, 'mcp', 'tst').textContent).toContain('Tools: echo');
    expect(byLabel<HTMLInputElement>(dlg, 'Folder').value).toBe('/data/chats/chat_a');
    expect(must(dlg.querySelector('[data-testid="chat-properties-note"]'), 'note').textContent).toContain('continues the same session');
    await r.act(() => box(dlg, 'skill', 'wrangler').click());
    await r.act(() => box(dlg, 'skill', 'durable-objects').click());
    await r.act(() => box(dlg, 'mcp', 'docs').click());
    await r.act(() => byText(dlg, 'button', 'Save').click());
    await waitFor(() => expect(updates).toHaveLength(1), r);
    // the server it had keeps its tools; the new one asks the core for its tools now (empty)
    expect(updates[0]).toEqual({
      id: 'chat_a',
      skills: [{ registryId: 'cloudflare-skills', name: 'durable-objects' }],
      mcpServers: [
        { serverId: 'tst', tools: ['echo'] },
        { serverId: 'docs', tools: [] },
      ],
    });
    await waitFor(() => expect(must$(r, '[data-testid="chip-mcp"]').textContent).toBe('2 MCP servers'), r);
    await waitFor(() => expect(doc$$('dialog[open]').filter((d) => d.querySelector('[data-testid="chat-properties-form"]'))).toHaveLength(0), r);
  });

  it('a new folder warns that a new session starts, and the core refusal shows in the dialog', async () => {
    const updates: ChatUpdate[] = [];
    const c = chat({ projectId: '', cwd: 'C:\\work\\notes' });
    const { r } = setup(
      {
        chatList: async () => [item(c)],
        chatGet: async () => ({ chat: c, messages: [], hasMore: false }),
        chatUpdate: async (u) => {
          updates.push(u);
          if (u.cwd === 'C:\\missing') throw new Error('the folder C:\\missing does not exist');
          return chat({ ...c, ...u, sessionId: '' } as Partial<Chat>);
        },
      },
      { id: 'chat_a' },
    );
    const dlg = await openProperties(r);
    expect(must(dlg.querySelector('[data-testid="chat-properties-note"]'), 'note').textContent).toContain('continues the same session');
    type(byLabel(dlg, 'Folder'), 'C:\\missing');
    await waitFor(() => expect(must(dlg.querySelector('[data-testid="chat-properties-note"]'), 'note').textContent).toContain('starts a new one, given the conversation so far'), r);
    await r.act(() => byText(dlg, 'button', 'Save').click());
    await waitFor(() => expect(must(dlg.querySelector('[role="alert"]'), 'alert').textContent).toContain('does not exist'), r);
    type(byLabel(dlg, 'Folder'), '');
    await r.act(() => byText(dlg, 'button', 'Save').click());
    await waitFor(() => expect(updates).toEqual([{ id: 'chat_a', cwd: 'C:\\missing' }, { id: 'chat_a', cwd: '' }]), r);
  });

  it("a project chat's folder is the project's, shown read-only, and nothing changed sends nothing", async () => {
    const updates: ChatUpdate[] = [];
    const c = chat();
    const { r } = setup({ chatList: async () => [item(c)], chatGet: async () => ({ chat: c, messages: [], hasMore: false }), chatUpdate: async (u) => (updates.push(u), c) }, { id: 'chat_a' });
    const dlg = await openProperties(r);
    expect(byLabel<HTMLInputElement>(dlg, 'Folder').readOnly).toBe(true);
    await waitFor(() => expect(valueOf(byLabel(dlg, 'Agent CLI'))).toBe('claude'), r);
    await r.act(() => byText(dlg, 'button', 'Save').click());
    await waitFor(() => expect(doc$$('dialog[open]').filter((d) => d.querySelector('[data-testid="chat-properties-form"]'))).toHaveLength(0), r);
    expect(updates).toEqual([]);
  });

  it("asks before one of this chat's MCP tools that is not read-only runs, and sends the decision", async () => {
    const decided: [string, boolean][] = [];
    let pending: McpApproval[] = [
      { id: 'apr_1', server: 'tst', tool: 'post_note', agentId: 'chat_a', args: '{"text":"hi"}', created: 1, expiresAt: Date.now() + 60_000, projectId: '' },
      { id: 'apr_2', server: 'tst', tool: 'post_note', agentId: 'chat_other', args: '{}', created: 1, expiresAt: 0, projectId: '' },
    ];
    const c = chat({ projectId: '', mcpServers: [{ serverId: 'tst', tools: ['post_note'] }] });
    const { r } = setup(
      {
        chatList: async () => [item(c)],
        chatGet: async () => ({ chat: c, messages: [], hasMore: false }),
        mcpApprovalList: async () => pending,
        mcpApprovalDecide: async (id, ok) => {
          decided.push([id, ok]);
          pending = pending.filter((a) => a.id !== id);
        },
      },
      { id: 'chat_a' },
    );
    const ask = await waitFor(() => must$(r, '[data-testid="chat-mcp-approval"]'), r);
    expect(r.container.querySelectorAll('[data-testid="chat-mcp-approval"]')).toHaveLength(1); // only this chat's calls
    expect(ask.textContent).toContain('tst: post_note');
    expect(ask.textContent).toContain('{"text":"hi"}');
    await r.act(() => byText(ask, 'button', 'Approve this call').click());
    await waitFor(() => expect(decided).toEqual([['apr_1', true]]), r);
    await waitFor(() => expect(r.container.querySelectorAll('[data-testid="chat-mcp-approval"]')).toHaveLength(0), r);
  });
});
