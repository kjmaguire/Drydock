import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { DrydockEvent, NeedsYouItem } from '../../api/types.ts';
import { needsChanged } from '../../state/project.ts';
import { needsAgent, needsContextParts, needsHint, needsTarget } from './needs-you.ts';

const base: NeedsYouItem = { key: 'k', kind: 'approval', title: 't', severity: 1, createdAt: '2026-01-01T00:00:00Z' } as NeedsYouItem;

describe('Needs you targets and hints', () => {
  it("gives a task's item a context line: the change's size, the checks and the verdict (UI review P1-1)", () => {
    const base = { key: 'k', kind: 'review', severity: 2, title: 'Review and merge: Fix rounding', taskId: 't1', createdAt: '' } as NeedsYouItem;
    const it1 = { ...base, context: { agent: 'agt_rita', hasDiff: true, files: 1, insertions: 4, deletions: 1, checksPassed: 4, checksTotal: 4, verdict: 'SHIP' } };
    expect(needsContextParts(it1)).toEqual(['1 file +4 \u22121', '4/4 checks passed', 'reviewer: SHIP']);
    expect(needsAgent(it1)).toBe('agt_rita');
    // the agent that raised the item wins over the assignee; nothing recorded yet says nothing
    expect(needsAgent({ ...it1, agentId: 'agt_other' })).toBe('agt_other');
    expect(needsContextParts({ ...base, context: { hasDiff: false, files: 0, insertions: 0, deletions: 0, checksPassed: 0, checksTotal: 0 } })).toEqual([]);
    expect(needsContextParts(base)).toEqual([]);
    expect(needsContextParts({ ...base, context: { hasDiff: true, files: 3, insertions: 10, deletions: 0, checksPassed: 1, checksTotal: 2, verdict: 'REVISE' } })).toEqual(['3 files +10 \u22120', '1/2 checks passed', 'reviewer: REVISE']);
  });

  it('goes to the task, else the room, else the page that holds the decision', () => {
    expect(needsTarget({ ...base, taskId: 'a b' })).toBe('/task/a%20b');
    expect(needsTarget({ ...base, kind: 'question', roomId: 'r1' })).toBe('/room/r1');
    expect(needsTarget({ ...base, kind: 'question' })).toBe('/needs-you');
    // the approvals of a project's tool calls and outbound writes wait on the project's own Integrations page
    expect(needsTarget({ ...base, title: 'mcp_call: github/create_issue', taskId: 't1' })).toBe('/project/integrations');
    expect(needsTarget({ ...base, title: 'integration:linear/create_comment' })).toBe('/project/integrations');
    expect(needsHint({ ...base, title: 'mcp_call: github/create_issue', taskId: 't1' })).toContain("project's Integrations page");
  });

  it("opens a chat whose agent waits to be allowed a tool call, where Allow once and Refuse are", () => {
    const ask = { ...base, kind: 'chat_permission', title: 'Asks to use Bash: go test ./...', chatId: 'chat 1' };
    expect(needsTarget(ask)).toBe('/chats/chat%201');
    expect(needsHint(ask)).toContain('allow it once or refuse');
  });

  it('reloads every Needs you list on the same events, a chat asking among them', () => {
    const ev = (type: string): DrydockEvent => ({ type, seq: 1, time: '' });
    for (const t of ['chat.permission', 'chat.permission_answered', 'approval.requested', 'task.updated', 'watchdog.paused', 'room.message_posted']) expect(needsChanged(ev(t))).toBe(true);
    for (const t of ['chat.tool_result', 'agent.output', 'room.presence_changed']) expect(needsChanged(ev(t))).toBe(false);
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
    // the question a blocking gate asks when none of its checks could judge (M28): the title selects a fixed hint
    const verdict = { ...base, kind: 'question', title: 'No check gave a result for the checks of this task: tests skipped: no test command is set. Fix what stopped them and check again, go on without a result this time, or stop.', taskId: 't1' };
    expect(needsHint(verdict)).toBe('No check could judge this work, so nothing says it is fine. Open the task to check again, go on without a result this time, or stop.');
    expect(needsTarget(verdict)).toBe('/task/t1');
    expect(needsHint({ ...verdict, kind: 'approval' })).toBe(''); // only the question of that title
    expect(needsHint({ ...verdict, title: 'Some other question about No check gave a result for it' })).toBe('');
    expect(needsHint({ ...base, kind: 'watchdog_pause', title: 'The team lead asks for a human decision on work' })).toContain('recommendation');
    const repoConfig = { ...base, title: "The repository's git configuration changed during the task (filter.evil.clean). A planted hook...", taskId: 't1' };
    expect(needsTarget(repoConfig)).toBe('/task/t1');
    expect(needsHint(repoConfig)).toContain('which items changed');
    const permission = { ...base, title: 'Permission request: Bash: npm install left-pad', taskId: 't1' };
    expect(needsTarget(permission)).toBe('/task/t1');
    expect(needsHint(permission)).toContain('exact tool and input');
  });
});
