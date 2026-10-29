// Decision P109: "Import from Claude Code" on the Chats screen. The client is a partial TEST DOUBLE that answers
// chat.importList and chat.import as the core does (internal/chats/claudeimport.go).

import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Chat, ImportCandidate, ImportList, ImportRef, ImportResult } from '../../api/chatClient.ts';
import type { Project } from '../../api/types.ts';
import { lic } from '../../test/fixtures.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { ChatsScreen } from './chats-screen.tsx';

afterEach(cleanup);

// --- plain data fixtures (no behavior) ---

const cand = (over: Partial<ImportCandidate> = {}): ImportCandidate => ({
  sessionId: 's-new',
  wslDistro: '',
  title: 'Fix the parser tests',
  cwd: 'C:\\repos\\gocalc',
  projectId: 'prj_1',
  messages: 6,
  firstAt: Date.now() - 7_200_000,
  lastAt: Date.now() - 3_600_000,
  size: 2048,
  ...over,
});
const chat = (id: string): Chat =>
  ({ id, projectId: '', title: 'x', adapter: 'claude', model: '', effort: '', permissionMode: 'default', skipsApprovals: false, cwd: '', sessionId: '', allowedTools: [], readHistory: true,
    status: 'idle', lastError: '', pinned: false, archived: false, archivedAt: 0, forkedFrom: '', createdAt: 0, updatedAt: 0, tokensIn: 0, tokensOut: 0, costUsd: null, costUnknownTurns: 0, version: 1 }) as Chat;

const source = { wslDistro: '', folder: 'C:\\Users\\k\\.claude\\projects', sessions: 3, skipped: 0 };
const fourSessions = (): ImportList => ({
  sources: [source],
  sessions: [
    cand(),
    cand({ sessionId: 's-wsl', wslDistro: 'Ubuntu', title: 'Notes in the distro', cwd: '\\\\wsl.localhost\\Ubuntu\\home\\k\\notes', projectId: '', messages: 1 }),
    cand({ sessionId: 's-old', title: 'Imported before', chatId: 'chat_old' }),
    cand({ sessionId: 's-nowhere', title: 'No folder', cwd: '', projectId: '', problem: 'The session does not say which folder it ran in, so it cannot be continued.' }),
  ],
});

function setup(client: Partial<Client>) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as Project],
    licenseStatus: async () => lic(),
    appInfo: async () => ({ name: 'Drydock', user: 'u', human: 'h', dataDir: '/d' }),
    adapterList: async () => [],
    chatList: async () => [],
    ...client,
  });
  s.workspace.start();
  const navigated: string[] = [];
  const r = mount(<ChatsScreen onNavigate={(h) => void navigated.push(h)} />, s);
  return { s, r, navigated };
}

/** The element, or throw (so waitFor retries until it is there). */
function must(root: ParentNode, sel: string): HTMLButtonElement {
  const el = root.querySelector<HTMLButtonElement>(sel);
  if (!el) throw new Error(`missing ${sel}`);
  return el;
}
const dialog = () => {
  const d = Array.from(document.querySelectorAll<HTMLElement>('dialog[open]')).find((x) => x.querySelector('[data-testid="chat-import"]'));
  if (!d) throw new Error('the import dialog is not open');
  return d;
};
const rows = () => Array.from(dialog().querySelectorAll<HTMLElement>('[data-testid="chat-import-row"]'));
const box = (session: string) => dialog().querySelector<HTMLInputElement>(`[data-session="${session}"] input[type="checkbox"]`)!;
const goButton = () => dialog().querySelector<HTMLButtonElement>('[data-testid="chat-import-go"]')!;

describe('Import from Claude Code', () => {
  it('lists the sessions found, chooses every one not imported yet, and imports the chosen ones', async () => {
    let reads = 0;
    const asked: ImportRef[][] = [];
    let list = fourSessions();
    const { r } = setup({
      chatImportList: async () => (reads++, list),
      chatImport: async (refs): Promise<ImportResult> => {
        asked.push(refs);
        list = { ...list, sessions: list.sessions.map((c) => (refs.some((x) => x.sessionId === c.sessionId) ? { ...c, chatId: `chat_${c.sessionId}` } : c)) };
        return { imported: refs.map((x) => ({ sessionId: x.sessionId, chat: chat(`chat_${x.sessionId}`), messages: 6 })), skipped: [] };
      },
    });
    const open = await waitFor(() => must(r.container, '[data-testid="chat-import-open-dialog"]'), r);
    expect(open.getAttribute('aria-label')).toBe('Import from Claude Code');
    await r.act(() => open.click());
    await waitFor(() => expect(rows().length).toBe(4), r);
    expect(reads).toBe(1);

    // the two new ones are chosen; the imported one and the one without a folder cannot be
    expect(box('s-new').checked).toBe(true);
    expect(box('s-wsl').checked).toBe(true);
    expect(box('s-old').disabled).toBe(true);
    expect(box('s-nowhere').disabled).toBe(true);
    expect(dialog().querySelector('[data-testid="chat-import-count"]')!.textContent).toBe('2 of 2 chosen');
    const [first, wsl, old, nowhere] = rows();
    expect(first!.textContent).toContain('Windows · in gocalc · 6 messages');
    expect(wsl!.textContent).toContain('WSL: Ubuntu · \\\\wsl.localhost\\Ubuntu\\home\\k\\notes · 1 message');
    expect(old!.textContent).toContain('imported');
    expect(old!.querySelector('[data-testid="chat-import-open"]')).not.toBeNull();
    expect(nowhere!.textContent).toContain('cannot be continued');
    expect(goButton().textContent).toBe('Import 2 chats');

    // the person leaves one out
    await r.act(() => box('s-wsl').click());
    expect(goButton().textContent).toBe('Import 1 chat');
    await r.act(() => goButton().click());
    await waitFor(() => expect(dialog().querySelector('[data-testid="chat-import-result"]')!.textContent).toContain('Imported 1 chat.'), r);
    expect(asked).toEqual([[{ sessionId: 's-new', wslDistro: '' }]]);
    // read again: the imported one now shows its chat, nothing is chosen, the button is off
    await waitFor(() => expect(box('s-new').disabled).toBe(true), r);
    expect(reads).toBe(2);
    expect(goButton().disabled).toBe(true);
    expect(byText(dialog(), 'button', 'Done')).toBeTruthy();
  });

  it('opens an imported chat from the list', async () => {
    const { r, navigated } = setup({ chatImportList: async () => fourSessions() });
    await r.act(async () => (await waitFor(() => must(r.container, '[data-testid="chat-import-open-dialog"]'), r)).click());
    const link = await waitFor(() => must(dialog(), '[data-testid="chat-import-open"]'), r);
    await r.act(() => link.click());
    expect(navigated).toEqual(['/chats/chat_old']);
    await waitFor(() => expect(document.querySelector('dialog[open] [data-testid="chat-import"]')).toBeNull(), r);
  });

  it('says when nothing was found, and why a source could not be read', async () => {
    const { r } = setup({
      chatImportList: async () => ({ sources: [{ ...source, sessions: 0 }, { wslDistro: 'Ubuntu', folder: '', sessions: 0, skipped: 0, error: 'The WSL distro Ubuntu did not say where its user\'s home is.' }], sessions: [] }),
    });
    // from the empty list too
    await r.act(async () => (await waitFor(() => must(r.container, '[data-testid="chat-import-empty"]'), r)).click());
    const none = await waitFor(() => must(dialog(), '[data-testid="chat-import-none"]'), r);
    expect(none.textContent).toBe('No Claude Code chats were found in C:\\Users\\k\\.claude\\projects.');
    expect(dialog().textContent).toContain("WSL: Ubuntu. The WSL distro Ubuntu did not say where its user's home is.");
    expect(goButton().disabled).toBe(true);
  });

  it('shows a refused import and keeps the choice', async () => {
    const { r } = setup({ chatImportList: async () => fourSessions(), chatImport: async () => Promise.reject(new Error('Drydock is read-only: no valid license')) });
    await r.act(async () => (await waitFor(() => must(r.container, '[data-testid="chat-import-open-dialog"]'), r)).click());
    await waitFor(() => expect(rows().length).toBe(4), r);
    await r.act(() => goButton().click());
    await waitFor(() => expect(dialog().textContent).toContain('Drydock is read-only: no valid license'), r);
    expect(box('s-new').checked).toBe(true);
    expect(goButton().textContent).toBe('Import 2 chats');
  });
});
