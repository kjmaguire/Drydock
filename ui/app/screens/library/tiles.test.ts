import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { serverBlurb, serverState, templateBlurb, templateState } from './tiles.ts';

const server = { transport: 'stdio' as const, tools: [{ name: 'echo', readOnlyHint: true }], projects: ['p1'], oauth: false, oauthAt: 0, lastError: undefined as string | undefined };
const tools2 = [
  { name: 'a', readOnlyHint: true },
  { name: 'b', readOnlyHint: false },
];

describe('library tiles', () => {
  it('says what a server is: how it is reached and how many tools it has', () => {
    expect(serverBlurb({ transport: 'stdio', tools: [] })).toBe('Program on this computer');
    expect(serverBlurb({ transport: 'http', tools: server.tools })).toBe('Web address, 1 tool');
    expect(serverBlurb({ transport: 'http', tools: tools2 })).toBe('Web address, 2 tools');
  });

  it("says a server's state, the most pressing first", () => {
    expect(serverState({ ...server, lastError: 'refused', oauth: true })).toEqual({ text: 'Could not connect', tone: 'bad' });
    expect(serverState({ ...server, oauth: true, oauthAt: 0 })).toEqual({ text: 'Not signed in', tone: 'warn' });
    expect(serverState({ ...server, oauth: true, oauthAt: 5 })).toEqual({ text: 'In 1 project', tone: 'ok' });
    expect(serverState({ ...server, tools: [] })).toEqual({ text: 'Tools not found yet', tone: 'warn' });
    expect(serverState({ ...server, projects: [] })).toEqual({ text: 'In no project yet', tone: '' });
    expect(serverState({ ...server, projects: ['a', 'b'] })).toEqual({ text: 'In 2 projects', tone: 'ok' });
  });

  it('says what a template is and what an agent made from it gets', () => {
    expect(templateBlurb({ role: 'backend', adapter: 'claude' })).toBe('backend, on claude');
    expect(templateBlurb({ role: '', adapter: 'codex' })).toBe('Team member on codex');
    const lib = new Set(['tst', 'docs']);
    const skill = { name: 'k', registry: 'r', ref: 'x' } as never;
    expect(templateState({ mcpServers: [], skills: [] }, lib)).toEqual({ text: 'No servers or skills', tone: '' });
    expect(templateState({ mcpServers: ['tst'], skills: [] }, lib)).toEqual({ text: 'Gets 1 server', tone: 'ok' });
    expect(templateState({ mcpServers: ['tst', 'docs'], skills: [skill] }, lib)).toEqual({ text: 'Gets 2 servers and 1 skill', tone: 'ok' });
    expect(templateState({ mcpServers: ['tst', 'gone', 'old'], skills: [] }, lib)).toEqual({ text: '2 servers not in the Library', tone: 'warn' });
  });
});
