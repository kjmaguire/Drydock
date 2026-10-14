import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { RecommendItem, Recommendation } from '../../api/extTypes.ts';
import { forWhom, itemText, openItems, triggerText } from './recommend.ts';

const names = new Map([
  ['agt_1', 'Backend #1'],
  ['agt_2', 'Lead'],
]);
const item = (over: Partial<RecommendItem>): RecommendItem => ({ id: 'r1', kind: 'skill', action: 'add', name: 'wrangler', label: 'wrangler', agents: [], reason: 'why', status: 'open', ...over });

describe('recommendation text', () => {
  it('names the agents, or the whole team', () => {
    expect(forWhom([], names)).toBe('the whole team');
    expect(forWhom(['agt_1', 'agt_2'], names)).toBe('Backend #1, Lead');
    expect(forWhom(['agt_9'], names)).toBe('agt_9');
  });

  it('says what each kind of item does', () => {
    expect(itemText(item({}), names)).toBe('Add the skill wrangler for the whole team');
    expect(itemText(item({ action: 'give', agents: ['agt_1'] }), names)).toBe('Give the skill wrangler to Backend #1');
    expect(itemText(item({ action: 'team' }), names)).toBe('Give the skill wrangler to the whole team');
    expect(itemText(item({ action: 'remove' }), names)).toBe('Stop using the skill wrangler');
    expect(itemText(item({ kind: 'mcp', action: 'give', name: 'cfdocs', label: 'Cloudflare docs', agents: ['agt_2'] }), names)).toBe('Give the MCP server Cloudflare docs to Lead');
    expect(itemText(item({ kind: 'integration', action: 'add', name: 'linear', label: 'Linear' }), names)).toBe('Assign Linear to this project');
  });

  it('says why a turn ran and which items still wait', () => {
    expect(triggerText('manual')).toBe('when you asked');
    expect(triggerText('skills_added')).toBe('after you picked skills in the Library');
    expect(triggerText('evolution')).toBe('by itself, as the project evolved');
    const r = { items: [item({ id: 'a' }), item({ id: 'b', status: 'applied' }), item({ id: 'c', status: 'dismissed' })] } as Recommendation;
    expect(openItems(r).map((i) => i.id)).toEqual(['a']);
    expect(openItems(undefined)).toEqual([]);
  });
});
