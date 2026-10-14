import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { NeedsYouItem } from '../../api/types.ts';
import { needsHint, needsTarget } from './needs-you.ts';

const base: NeedsYouItem = { key: 'k', kind: 'approval', title: 't', severity: 1, createdAt: '2026-01-01T00:00:00Z' } as NeedsYouItem;

describe('Needs you targets and hints', () => {
  it('goes to the task, else the room, else the page that holds the decision', () => {
    expect(needsTarget({ ...base, taskId: 'a b' })).toBe('/task/a%20b');
    expect(needsTarget({ ...base, kind: 'question', roomId: 'r1' })).toBe('/room/r1');
    expect(needsTarget({ ...base, kind: 'question' })).toBe('/needs-you');
    // the approvals of a project's tool calls and outbound writes wait on the project's own Integrations page
    expect(needsTarget({ ...base, title: 'mcp_call: github/create_issue', taskId: 't1' })).toBe('/project/integrations');
    expect(needsTarget({ ...base, title: 'integration:linear/create_comment' })).toBe('/project/integrations');
    expect(needsHint({ ...base, title: 'mcp_call: github/create_issue', taskId: 't1' })).toContain("project's Integrations page");
  });

  it('explains the items that need more than a click, from a fixed set of hints', () => {
    const budget = { ...base, title: 'Budget limit reached', taskId: 't1' };
    const spec = { ...base, kind: 'review', title: 'Accept the spec: Add Multiply', taskId: 't1' };
    const outbound = { ...base, title: 'integration:linear/create_comment' };
    expect(needsTarget(budget)).toBe('/task/t1');
    expect(needsTarget(spec)).toBe('/task/t1');
    expect(needsHint(budget)).toContain('raise the limit');
    expect(needsHint(spec)).toContain('spec gate');
    expect(needsHint(outbound)).toContain('exact text');
    expect(needsHint({ ...base, kind: 'question', title: 'x' })).toBe('');
    expect(needsHint({ ...base, kind: 'question', title: 'Iteration limit reached: a human needs to decide', taskId: 't1' })).toContain('fresh loop budget');
    expect(needsHint({ ...base, kind: 'question', title: 'The reviewer keeps asking for the same change: a human needs to decide' })).toContain('stop it');
    expect(needsHint({ ...base, kind: 'watchdog_pause', title: 'Watchdog paused work: tool_spin' })).toContain('send guidance and continue');
    expect(needsHint({ ...base, kind: 'watchdog_pause', title: 'The team lead asks for a human decision on work' })).toContain('recommendation');
    const repoConfig = { ...base, title: "The repository's git configuration changed during the task (filter.evil.clean). A planted hook...", taskId: 't1' };
    expect(needsTarget(repoConfig)).toBe('/task/t1');
    expect(needsHint(repoConfig)).toContain('which items changed');
    const permission = { ...base, title: 'Permission request: Bash: npm install left-pad', taskId: 't1' };
    expect(needsTarget(permission)).toBe('/task/t1');
    expect(needsHint(permission)).toContain('exact tool and input');
  });
});
