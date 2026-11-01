import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { AgentDraft } from '../../api/libraryClient.ts';
import { STARTERS, draftSourceNote, draftSummary, specFromDraft } from './agent-draft.ts';

const draft = (over: Partial<AgentDraft> = {}): AgentDraft => ({
  name: 'Security Reviewer',
  role: 'security reviewer',
  adapter: 'claude',
  model: 'opus',
  effort: 'high',
  permissionMode: 'plan',
  purpose: 'You review diffs for security problems. You never edit.',
  skills: ['security-review'],
  tools: ['github'],
  why: 'Read only, because you said "never edit".',
  source: 'rules',
  ...over,
});

describe('describe a new agent (P138)', () => {
  it('has the six starters of the mockup', () => {
    expect(STARTERS.map((s) => s.label)).toEqual(['Builder', 'Reviewer', 'Tester', 'Docs writer', 'Team lead', 'Planner']);
  });

  it('a draft becomes the agent.create spec; empty values are left to the core', () => {
    expect(specFromDraft(draft({ name: ' Sec ' }), 'prj_1')).toEqual({
      projectId: 'prj_1',
      name: 'Sec',
      role: 'security reviewer',
      adapter: 'claude',
      model: 'opus',
      effort: 'high',
      permissionMode: 'plan',
      purpose: 'You review diffs for security problems. You never edit.',
      skills: ['security-review'],
    });
    expect(specFromDraft(draft({ model: '', effort: '', permissionMode: '', purpose: '', skills: [] }), 'p')).toEqual({ projectId: 'p', name: 'Security Reviewer', role: 'security reviewer', adapter: 'claude' });
  });

  it('says who drafted it, and sums it up in a line', () => {
    expect(draftSourceNote(undefined)).toContain('Nothing is saved');
    expect(draftSourceNote(draft({ source: 'model' }))).toContain('your default model');
    expect(draftSourceNote(draft())).toContain('no model was used');
    expect(draftSummary(draft())).toBe('You review diffs for security problems.');
    expect(draftSummary(draft({ purpose: '' }))).toBe('security reviewer');
  });
});
