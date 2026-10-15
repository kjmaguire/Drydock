// Decision P60: the pure helpers behind a chat's properties (skills and MCP servers), and what the client sends and reads.

import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { createChatClient, normalizeChat, type Chat } from '../../api/chatClient.ts';
import type { LibrarySkill } from '../../api/extTypes.ts';
import type { Transport } from '../../api/transport.ts';
import { firstSentence, grantableTools, pickableSkills, propsChips, sameServers, sameSkills, skillUsable, toggleServer, toggleSkill } from './chats.ts';

const k = (name: string, over: Partial<LibrarySkill> = {}): LibrarySkill =>
  ({ id: name, registryId: 'reg', repo: 'o/r', name, description: `About ${name}.`, status: 'available', gone: false, ...over }) as LibrarySkill;

describe('chat properties helpers', () => {
  it('shows the chosen skills first, then the usable ones matching the filter', () => {
    const lib = [k('alpha'), k('beta', { description: 'Deploy workers.' }), k('gamma', { status: 'blocked' }), k('delta', { gone: true }), k('omega')];
    const chosen = [{ registryId: 'reg', name: 'omega' }];
    expect(pickableSkills(lib, chosen, '').map((x) => x.name)).toEqual(['omega', 'alpha', 'beta']);
    expect(pickableSkills(lib, chosen, 'deploy').map((x) => x.name)).toEqual(['omega', 'beta']);
    expect(pickableSkills(lib, chosen, 'REG', 2).map((x) => x.name)).toEqual(['omega', 'alpha']);
    // a chosen skill that is blocked now is still shown, so it can be taken away
    expect(pickableSkills(lib, [{ registryId: 'reg', name: 'gamma' }], 'zzz').map((x) => x.name)).toEqual(['gamma']);
    expect(skillUsable(k('x'))).toBe(true);
    expect(skillUsable(k('x', { gone: true }))).toBe(false);
  });

  it('toggles skills and servers; a server the chat had keeps its tools, a new one asks for them', () => {
    const s1 = toggleSkill([], { registryId: 'reg', name: 'a' }, true);
    expect(toggleSkill(s1, { registryId: 'reg', name: 'b' }, true)).toEqual([
      { registryId: 'reg', name: 'a' },
      { registryId: 'reg', name: 'b' },
    ]);
    expect(toggleSkill(s1, { registryId: 'reg', name: 'a' }, false)).toEqual([]);
    const had = [{ serverId: 'tst', tools: ['echo'] }];
    expect(toggleServer([], 'tst', true, had)).toEqual([{ serverId: 'tst', tools: ['echo'] }]);
    expect(toggleServer(had, 'docs', true, had)).toEqual([...had, { serverId: 'docs', tools: [] }]);
    expect(toggleServer(had, 'tst', false, had)).toEqual([]);
    expect(sameSkills(s1, [{ registryId: 'reg', name: 'a' }])).toBe(true);
    expect(sameServers(had, [{ serverId: 'tst', tools: ['echo', 'envlen'] }])).toBe(false);
  });

  it('counts what a chat was given, and the tools a server can grant', () => {
    expect(propsChips({})).toEqual([]);
    expect(propsChips({ skills: [{ registryId: 'r', name: 'a' }], mcpServers: [{ serverId: 'a', tools: [] }, { serverId: 'b', tools: [] }] })).toEqual(['1 skill', '2 MCP servers']);
    expect(grantableTools({ tools: [{ name: 'echo', readOnlyHint: true }, { name: 'format', readOnlyHint: false, flags: ['x'] }] })).toEqual(['echo']);
    expect(firstSentence('Use it.  Then more.')).toBe('Use it.');
    expect(firstSentence('x'.repeat(200), 10)).toBe(`${'x'.repeat(9)}…`);
  });

  it('the client sends the properties and fills missing lists', async () => {
    const calls: [string, unknown][] = [];
    const t = {
      call: async (method: string, params: unknown) => {
        calls.push([method, params]);
        return { id: 'chat_1', allowedTools: null, skills: null, mcpServers: [{ serverId: 'tst', tools: null }] };
      },
    } as unknown as Transport;
    const c = createChatClient(t);
    const got = await c.chatUpdate({ id: 'chat_1', cwd: '', skills: [], mcpServers: [{ serverId: 'tst', tools: [] }] });
    expect(calls).toEqual([['chat.update', { id: 'chat_1', cwd: '', skills: [], mcpServers: [{ serverId: 'tst', tools: [] }] }]]);
    expect(got.skills).toEqual([]);
    expect(got.mcpServers).toEqual([{ serverId: 'tst', tools: [] }]);
    expect(normalizeChat({ allowedTools: [] } as unknown as Chat).skills).toEqual([]);
  });
});
