import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { modelChip, purposeLine, starterDraft, templateBlurb, templateState } from './tiles.ts';

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

describe('the My agents card and a starter as my agent (decision P140)', () => {
  it('words the chip and the one line of purpose', () => {
    expect(modelChip({ adapter: 'claude', model: 'opus', effort: 'high' })).toBe('opus · high');
    expect(modelChip({ adapter: 'claude', model: 'opus', effort: '' })).toBe('opus');
    expect(modelChip({ adapter: 'codex', model: '', effort: 'high' })).toBe('codex');
    expect(purposeLine({ purpose: 'Keeps the crew on task. Escalates when stuck.' })).toBe('Keeps the crew on task.');
    expect(purposeLine({ purpose: 'First line\nsecond line' })).toBe('First line');
    expect(purposeLine({ purpose: '' })).toBe('');
    expect(purposeLine({ purpose: 'v1.2 is the version' })).toBe('v1.2 is the version');
  });

  it("drafts a starter as my agent: its preferred CLI, model, effort and mode, its skills, and only the servers already in the library", () => {
    const d = starterDraft({
      id: 'ui-engineer',
      name: 'UI Engineer',
      role: 'frontend',
      category: 'design',
      summary: '',
      description: '',
      purpose: 'Builds interfaces.',
      preferred: { adapter: 'claude', model: 'sonnet', effort: 'high', permissionMode: 'acceptEdits', practices: false },
      alternatives: [],
      mcp: ['ctx7', 'figma'],
      skills: [{ registry: 'anthropic-skills', name: 'frontend-design', ref: 'abc' }],
      tags: [],
      availability: {
        adapters: [],
        preferredUsable: true,
        mcp: [
          { id: 'ctx7', inLibrary: true, libraryId: 'lib_ctx7' },
          { id: 'figma', inLibrary: false },
        ],
        skills: [],
      },
    });
    expect(d).toEqual({
      name: 'UI Engineer',
      role: 'frontend',
      purpose: 'Builds interfaces.',
      adapter: 'claude',
      model: 'sonnet',
      effort: 'high',
      permissionMode: 'acceptEdits',
      practices: false,
      mcpServers: ['lib_ctx7'],
      skills: [{ registry: 'anthropic-skills', name: 'frontend-design', ref: 'abc' }],
    });
    expect('id' in d).toBe(false);
  });
});
