import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { DrydockEvent, NeedsYouItem } from '../../api/types.ts';
import { needsChanged } from '../../state/project.ts';
import type { ApprovalDetail } from '../../api/inboxClient.ts';
import { summariesChanged } from '../../state/workspace.ts';
import {
  answerOf,
  canAck,
  clock,
  doingWords,
  msOf,
  needsAgent,
  needsContextParts,
  needsHint,
  needsLook,
  needsRowTitle,
  needsSubtitle,
  needsTarget,
  scanProgress,
  shortHash,
  startOfToday,
  stepFraction,
  taskKey,
  untilWords,
  verdictWords,
} from './needs-you.ts';

const base: NeedsYouItem = { key: 'k', kind: 'approval', title: 't', severity: 1, createdAt: '2026-01-01T00:00:00Z' } as NeedsYouItem;

describe('Needs you targets and hints', () => {
  it("gives a task's item a context line: the change's size, the checks and the verdict (UI review P1-1)", () => {
    const base = { key: 'k', kind: 'review', severity: 2, title: 'Review and merge: Fix rounding', taskId: 't1', createdAt: '' } as NeedsYouItem;
    const it1 = { ...base, context: { agent: 'agt_rita', hasDiff: true, files: 1, insertions: 4, deletions: 1, checksPassed: 4, checksTotal: 4, verdict: 'SHIP' } };
    expect(needsContextParts(it1)).toEqual(['1 file +4 \u22121', '4/4 checks passed', 'reviewer approved']);
    expect(needsAgent(it1)).toBe('agt_rita');
    // the agent that raised the item wins over the assignee; nothing recorded yet says nothing
    expect(needsAgent({ ...it1, agentId: 'agt_other' })).toBe('agt_other');
    expect(needsContextParts({ ...base, context: { hasDiff: false, files: 0, insertions: 0, deletions: 0, checksPassed: 0, checksTotal: 0 } })).toEqual([]);
    expect(needsContextParts(base)).toEqual([]);
    expect(needsContextParts({ ...base, context: { hasDiff: true, files: 3, insertions: 10, deletions: 0, checksPassed: 1, checksTotal: 2, verdict: 'REVISE' } })).toEqual(['3 files +10 \u22120', '1/2 checks passed', 'reviewer asked for changes']);
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
    for (const t of ['chat.permission', 'chat.permission_answered', 'approval.requested', 'task.updated', 'watchdog.paused', 'room.message_posted', 'needsyou.acked', 'approval.rule.added']) expect(needsChanged(ev(t))).toBe(true);
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

describe('the Inbox helpers', () => {
  const now = Date.parse('2026-10-22T12:00:00Z');
  const dot = ' · ';
  it("tells a spec gate and a review and merge apart by the request's phase, and keys a task as GOC-41", () => {
    expect(needsLook({ kind: 'review', phase: 'spec' })).toBe('spec');
    expect(needsLook({ kind: 'review', phase: 'merge' })).toBe('merge');
    expect(needsLook({ kind: 'review', phase: 'plan' })).toBe('plan');
    expect(needsLook({ kind: 'review' })).toBe('review');
    expect(needsLook({ kind: 'approval' })).toBe('approval');
    expect(needsLook({ kind: 'watchdog_pause' })).toBe('watchdog');
    expect(needsLook({ kind: 'chat_permission' })).toBe('chat');
    expect(taskKey('GOC', 41)).toBe('GOC-41');
    expect(taskKey('', 41)).toBe('');
    expect(taskKey('GOC', 0)).toBe('');
  });

  it('says times in plain words and as a clock', () => {
    expect(untilWords(now + 58 * 60_000 + 5000, now)).toBe('in 58 min');
    expect(untilWords(now + 10_000, now)).toBe('in 1 min');
    expect(untilWords(now + 2 * 3_600_000, now)).toBe('in 2 h');
    expect(untilWords(now - 1, now)).toBe('now');
    expect(clock(58_000)).toBe('0:58');
    expect(clock(252_000)).toBe('4:12');
    expect(clock(3_723_000)).toBe('1:02:03');
    expect(clock(-5)).toBe('0:00');
    expect(msOf('2026-10-22T12:00:00Z')).toBe(now);
    expect(msOf(1_700_000_000)).toBe(1_700_000_000_000); // seconds
    expect(msOf(undefined)).toBe(0);
    expect(msOf('not a time')).toBe(0);
    const midnight = new Date(startOfToday(now));
    expect([midnight.getHours(), midnight.getMinutes(), midnight.getSeconds()]).toEqual([0, 0, 0]);
    expect(startOfToday(now) <= now && now - startOfToday(now) < 86_400_000).toBe(true);
    expect(shortHash('f041205c9d0e1b2a3c4d5e6f7a8b9c0da247a')).toBe('f041205c…a247a');
    expect(shortHash('abc')).toBe('abc');
  });

  it('titles a row with what is decided, and says where it comes from, what it is and when it was asked', () => {
    const perm = { ...base, title: 'Permission request: Bash: curl https://example.com', agentName: 'Free hand', projectId: 'p1', createdAt: new Date(now - 120_000).toISOString() };
    expect(needsRowTitle(perm)).toBe('Free hand asks to use Bash: curl https://example.com');
    expect(needsRowTitle({ ...perm, agentName: undefined })).toBe('An agent asks to use Bash: curl https://example.com');
    expect(needsSubtitle(perm, 'gocalc', undefined, now)).toBe(['gocalc', 'approval', 'asked 2 min ago'].join(dot));
    const spec = { ...base, kind: 'review', phase: 'spec', title: 'Accept the spec: x', agentName: 'Planner', createdAt: new Date(now - 6 * 60_000).toISOString() };
    expect(needsRowTitle(spec, { title: 'Add a changelog entry' })).toBe('Add a changelog entry: the spec is ready');
    expect(needsRowTitle(spec)).toBe('Accept the spec: x');
    expect(needsSubtitle(spec, 'gocalc', { template: 'feature' }, now)).toBe(['gocalc', 'spec gate', 'Feature template', '6 min ago'].join(dot));
    const merge = { ...spec, phase: 'merge', agentName: 'Builder', context: { hasDiff: true, files: 2, insertions: 3, deletions: 1, checksPassed: 7, checksTotal: 7 } };
    expect(needsRowTitle(merge, { title: 'Cover the empty case' })).toBe('Cover the empty case: 7 of 7 checks passed');
    expect(needsRowTitle({ ...merge, context: undefined }, { title: 'Cover the empty case' })).toBe('Cover the empty case: ready to merge');
    expect(needsSubtitle(merge, 'gocalc', undefined, now)).toBe(['gocalc', 'ready to merge', 'Builder', '6 min ago'].join(dot));
    // no project known: the subtitle leaves it out
    expect(needsSubtitle(perm, undefined, undefined, now)).toBe(['approval', 'asked 2 min ago'].join(dot));
  });

  it('marks done only what has no state to answer: a failed check, a watchdog pause, a question in a room', () => {
    expect(canAck({ key: 'check:m1' })).toBe(true);
    expect(canAck({ key: 'watchdog:3' })).toBe(true);
    expect(canAck({ key: 'question:m2' })).toBe(true);
    expect(canAck({ key: 'approval:apr1' })).toBe(false);
    expect(canAck({ key: 'chat-ask:a1' })).toBe(false);
  });

  it("reads a running task's step and a scan's engines as they are", () => {
    expect(doingWords('Editing limiter.go')).toBe('editing limiter.go');
    expect(doingWords(undefined)).toBe('');
    expect(stepFraction({ stepIndex: 3, stepCount: 5 })).toBe(0.6);
    expect(stepFraction({ stepIndex: 0, stepCount: 0 })).toBe(undefined);
    expect(stepFraction({ stepIndex: 7, stepCount: 5 })).toBe(1);
    const engines = [
      { id: 'secrets', name: 'Secrets', status: 'done' },
      { id: 'sast', name: 'Code', status: 'running' },
      { id: 'deps', name: 'Dependencies', status: 'pending' },
    ] as never;
    expect(scanProgress({ engines })).toEqual({ engine: 'Code running', done: 1, total: 3 });
    expect(scanProgress({ engines: [] })).toEqual({ engine: '', done: 0, total: 0 });
  });

  it('answers from the inbox only what the core lets it: a permission request, a plain approval, a spec gate, a review and merge', () => {
    const d = (over: Partial<ApprovalDetail>): ApprovalDetail => ({
      id: 'a', kind: 'approval', title: '', description: '', shell: false, targets: [], shortened: false, editable: false, projectId: 'p', createdAt: 0, status: 'REQUESTED',
      approveForTask: false, rules: [], runId: 'run1', ...over,
    });
    expect(answerOf(d({ gate: 'permission_prompt', approveForTask: true }))).toEqual({ kind: 'permission', runId: 'run1', forTask: true, note: undefined });
    expect(answerOf(d({ gate: 'permission_prompt', approveForTaskNote: 'it belongs to no task' }))).toEqual({ kind: 'permission', runId: 'run1', forTask: false, note: 'it belongs to no task' });
    expect(answerOf(d({}))).toEqual({ kind: 'approval', runId: 'run1' });
    expect(answerOf(d({ kind: 'review', phase: 'spec', payload: { phase: 'spec' } }))).toEqual({ kind: 'spec', runId: 'run1' });
    expect(answerOf(d({ kind: 'review', phase: 'merge' }))).toEqual({ kind: 'merge', runId: 'run1' });
    // the task's page answers these: a budget pause, a CI gate, the limit question, a watchdog pause, a plan, a question
    for (const payload of [{ gate: 'budget' }, { gate: 'ci' }, { phase: 'limit' }, { signal: 'tool_spin', stage: 2 }]) expect(answerOf(d({ payload })).kind).toBe('open');
    expect(answerOf(d({ kind: 'review', phase: 'plan' })).kind).toBe('open');
    expect(answerOf(d({ kind: 'question' })).kind).toBe('open');
    // answered already, or not answered on a run (an outbound write, a mode change recorded decided)
    expect(answerOf(d({ status: 'APPROVED' })).kind).toBe('open');
    expect(answerOf(d({ runId: undefined })).kind).toBe('open');
  });

  it('reads the running tasks again when a task, its run or its agent moves on', () => {
    const ev = (type: string): DrydockEvent => ({ type, seq: 1, time: '' });
    for (const t of ['task.updated', 'run.status', 'room.presence_changed']) expect(summariesChanged(ev(t))).toBe(true);
    for (const t of ['agent.output', 'chat.message']) expect(summariesChanged(ev(t))).toBe(false);
  });
});

describe('verdictWords', () => {
  it("says the reviewer's verdict in words, not the core's constant", () => {
    expect(verdictWords('SHIP')).toBe('approved');
    expect(verdictWords('REVISE')).toBe('asked for changes');
    expect(verdictWords('NEEDS_ATTENTION')).toBe('needs attention');
  });
});
