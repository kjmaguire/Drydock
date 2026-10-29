// Decision P109: the words and choices of "Import from Claude Code".

import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { ImportCandidate } from '../../api/chatClient.ts';
import { defaultSelection, importable, importButton, importFolders, importKey, importLine, importSummary } from './chat-import.ts';

const cand = (over: Partial<ImportCandidate> = {}): ImportCandidate => ({
  sessionId: 's1',
  wslDistro: '',
  title: 'Fix the parser',
  cwd: 'C:\\repos\\gocalc',
  projectId: '',
  messages: 4,
  firstAt: 0,
  lastAt: 0,
  size: 100,
  ...over,
});

describe('chat import helpers', () => {
  it('chooses every session not imported yet that can be continued', () => {
    const list = { sessions: [cand(), cand({ sessionId: 's2', chatId: 'chat_x' }), cand({ sessionId: 's3', problem: 'no folder' }), cand({ sessionId: 's1', wslDistro: 'Ubuntu' })] };
    expect([...defaultSelection(list)]).toEqual([importKey(cand()), importKey(cand({ wslDistro: 'Ubuntu' }))]);
    expect(importable(cand({ chatId: 'c' }))).toBe(false);
    expect(importKey(cand())).not.toBe(importKey(cand({ wslDistro: 'Ubuntu' })));
  });

  it('describes a session by where it ran, its project or folder, its length and when', () => {
    const now = Date.UTC(2026, 9, 29, 12, 0);
    expect(importLine(cand({ lastAt: now - 5 * 60_000 }), '', now)).toBe('Windows · C:\\repos\\gocalc · 4 messages · 5 min');
    expect(importLine(cand({ wslDistro: 'Ubuntu', messages: 1 }), 'gocalc', now)).toBe('WSL: Ubuntu · in gocalc · 1 message');
    expect(importLine(cand({ cwd: '' }), '', now)).toBe('Windows · no folder · 4 messages');
  });

  it('says what an import did', () => {
    const chat = {} as never;
    expect(importSummary({ imported: [{ sessionId: 'a', chat, messages: 2 }], skipped: [] })).toBe('Imported 1 chat.');
    expect(importSummary({ imported: [], skipped: [{ sessionId: 'a', wslDistro: '', reason: 'Imported already.', chatId: 'c' }] })).toBe('No chat was imported. 1 was imported before.');
    expect(
      importSummary({
        imported: [{ sessionId: 'a', chat, messages: 2 }, { sessionId: 'b', chat, messages: 2 }],
        skipped: [{ sessionId: 'c', wslDistro: '', reason: 'x', chatId: 'c' }, { sessionId: 'd', wslDistro: '', reason: 'gone' }, { sessionId: 'e', wslDistro: '', reason: 'gone' }],
      }),
    ).toBe('Imported 2 chats. 1 was imported before, 2 could not be imported.');
    expect(importButton(0)).toBe('Import');
    expect(importButton(1)).toBe('Import 1 chat');
    expect(importButton(3)).toBe('Import 3 chats');
  });

  it('names the folders it looked in', () => {
    expect(
      importFolders({
        sources: [
          { wslDistro: '', folder: 'C:\\Users\\k\\.claude\\projects', sessions: 0, skipped: 0 },
          { wslDistro: 'Ubuntu', folder: '\\\\wsl.localhost\\Ubuntu\\home\\k\\.claude\\projects', sessions: 0, skipped: 0 },
        ],
        sessions: [],
      }),
    ).toBe('C:\\Users\\k\\.claude\\projects, \\\\wsl.localhost\\Ubuntu\\home\\k\\.claude\\projects (WSL: Ubuntu)');
  });
});
