import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { templateBlurb, templateState } from './tiles.ts';

describe('library tiles', () => {
  it('says what a template is and what an agent made from it gets', () => {
    expect(templateBlurb({ role: 'backend', adapter: 'claude' })).toBe('backend, on claude');
    expect(templateBlurb({ role: '', adapter: 'codex' })).toBe('Team member on codex');
    const lib = new Set(['tst', 'docs']);
    const skill = { name: 'k', registry: 'r', ref: 'x' } as never;
    expect(templateState({ mcpServers: [], skills: [] }, lib)).toEqual({ text: 'No servers or skills', tone: '' });
    expect(templateState({ mcpServers: ['tst'], skills: [] }, lib)).toEqual({ text: 'Gets 1 server', tone: 'ok' });
    expect(templateState({ mcpServers: ['tst', 'docs'], skills: [skill] }, lib)).toEqual({ text: 'Gets 2 servers and 1 skill', tone: 'ok' });
    // a server it names that is no longer connected (on the Integrations page) is counted, and warns
    expect(templateState({ mcpServers: ['tst', 'gone', 'old'], skills: [] }, lib)).toEqual({ text: '2 servers not connected', tone: 'warn' });
  });
});
