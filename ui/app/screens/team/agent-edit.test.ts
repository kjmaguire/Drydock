import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Agent, Capabilities } from '../../api/types.ts';
import {
  editsOf,
  effortList,
  effortSegments,
  everywhereNote,
  modeList,
  modelChip,
  modelList,
  permissionChoices,
  permissionOptions,
  permissionWords,
  scopeNote,
  updateFor,
} from './agent-edit.ts';

const agent = (over: Partial<Agent> = {}): Agent => ({
  id: 'agt_1',
  projectId: 'prj_1',
  name: 'Pay',
  adapter: 'claude',
  role: 'backend',
  model: 'sonnet',
  effort: 'medium',
  providerId: '',
  permissionMode: 'acceptEdits',
  purpose: 'Own payments.',
  status: 'STOPPED',
  ...over,
});

describe('the agent editor panel (P137)', () => {
  it('never leaves model and effort without choices: the CLI list, else the known one, else free text', () => {
    const caps = { models: ['m1', 'm2'], efforts: ['low', 'high'], permissionModes: ['default'] } as Capabilities;
    expect(modelList('claude', caps)).toEqual(['m1', 'm2']);
    expect(effortList('claude', caps)).toEqual(['low', 'high']);
    // still loading, or the read failed: what the claude adapter is known to take
    expect(modelList('claude', undefined)).toEqual(['fable', 'opus', 'sonnet', 'haiku']);
    expect(effortList('claude', undefined)).toEqual(['low', 'medium', 'high', 'xhigh', 'max']);
    expect(modeList('claude', undefined)).toContain('plan');
    // another CLI: free text (an empty list)
    expect(modelList('opencode', undefined)).toEqual([]);
    expect(effortList('opencode', {} as Capabilities)).toEqual([]);
    expect(modeList('opencode', undefined)).toEqual([]);
  });

  it('offers the default effort first and keeps a saved level the CLI does not list', () => {
    expect(effortSegments(['low', 'high'], 'high').map((s) => s.value)).toEqual(['', 'low', 'high']);
    expect(effortSegments(['low', 'high'], 'ultra').map((s) => s.value)).toEqual(['', 'low', 'high', 'ultra']);
  });

  it('maps the three permission words to the real modes and keeps the others under Other', () => {
    const claude = ['default', 'acceptEdits', 'plan', 'dontAsk', 'auto', 'bypassPermissions'];
    const c = permissionChoices(claude, 'acceptEdits');
    expect(c.segments.map((s) => [s.value, s.label])).toEqual([
      ['plan', 'Read only'],
      ['default', 'Asks before edits'],
      ['acceptEdits', 'Edits freely'],
    ]);
    expect(c.others).toEqual(['dontAsk', 'auto', 'bypassPermissions']);
    // an ACP CLI with its own modes: only those, and a saved mode it no longer lists stays visible
    const acp = permissionChoices(['default', 'yolo'], 'old');
    expect(acp.segments.map((s) => s.value)).toEqual(['default']);
    expect(acp.others).toEqual(['yolo', 'old']);
    expect(permissionWords('plan')).toBe('Read only');
    expect(permissionWords('bypassPermissions')).toBe('Skips approvals');
    expect(permissionWords('')).toBe('CLI default');
    expect(permissionWords('yolo')).toBe('yolo');
    expect(permissionOptions(claude).map((o) => o.value)).toEqual(['', 'default', 'plan', 'dontAsk', 'auto', 'bypassPermissions']);
    expect(permissionOptions(claude)[2]!.label).toBe('Read only (plan)');
  });

  it('the chip says model and effort', () => {
    expect(modelChip(agent())).toBe('sonnet · medium');
    expect(modelChip(agent({ effort: '' }))).toBe('sonnet');
    expect(modelChip(agent({ model: '', effort: '' }))).toBe('default model');
  });

  it('a save sends only the fields that changed', () => {
    const a = agent();
    expect(updateFor(a, editsOf(a))).toEqual({ id: 'agt_1' });
    expect(updateFor(a, { ...editsOf(a), model: 'opus', effort: 'high', permissionMode: 'plan', purpose: ' Own payments. ' })).toEqual({
      id: 'agt_1',
      model: 'opus',
      effort: 'high',
      permissionMode: 'plan',
    });
    expect(updateFor(a, { ...editsOf(a), effort: '' })).toEqual({ id: 'agt_1', effort: '' });
  });

  it('says where a save goes', () => {
    expect(scopeNote(agent(), false, 'calculator', 'opus · high')).toBe('opus · high is used for Pay in calculator only. Other projects keep their own settings.');
    expect(scopeNote(agent({ templateId: 'tpl_1' }), true, 'calculator', 'x')).toContain("Pay's template and to every agent made from it");
    expect(scopeNote(agent(), true, 'calculator', 'x')).toContain('Also saves Pay as a template');
    expect(everywhereNote({ templateName: 'Backend', created: false, agents: 2, skipped: [] })).toBe('Saved to the template Backend and 2 more agents.');
    expect(everywhereNote({ templateName: 'Pay', created: true, agents: 0, skipped: [] })).toBe('Saved as the template Pay.');
    expect(everywhereNote({ templateName: 'Backend', created: false, agents: 0, skipped: ['Rex: busy'] })).toBe('Saved to the template Backend. Not changed: Rex: busy');
  });
});
