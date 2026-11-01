import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { NeedsYouItem, Task } from '../../api/types.ts';
import { editAgentHref, newAgentHref } from './edit-agent.ts';
import { filterWork, groupWork, modelEffort, needLine, openItem, workRows, type WorkChat } from './work.ts';

const now = new Date(2026, 10, 1, 15, 0, 0).getTime();
const MIN = 60_000;
const task = (over: Partial<Task>): Task => ({ id: 't1', projectId: 'p', seq: 1, title: 'Task', description: '', status: 'IN_PROGRESS', priority: 0, iteration: 0, maxIterations: 3, costUsd: 0, created: now, updated: now, version: 1, ...over });
const chat = (over: Partial<WorkChat>): WorkChat => ({ id: 'c1', title: 'Chat', status: 'idle', updatedAt: now, createdAt: now, ...over });
const need = (over: Partial<NeedsYouItem>): NeedsYouItem => ({ key: 'n', kind: 'review', severity: 2, title: '', createdAt: '', ...over });

describe('Work (decision P141)', () => {
  const rows = workRows({
    now,
    agentName: (id) => (id === 'agt1' ? 'Backend Engineer' : ''),
    tasks: [
      task({ id: 'run', title: 'Currency converter page', assignee: 'agt1', updated: now - 4 * MIN }),
      task({ id: 'run2', title: 'Newer run', status: 'REVIEW', updated: now - 1 * MIN }),
      task({ id: 'merge', title: 'Add Multiply function', status: 'REVIEW', updated: now - 30 * MIN }),
      task({ id: 'wait', title: 'Waits on a person', status: 'WAITING_HUMAN', updated: now - 2 * MIN }),
      task({ id: 'back', title: 'Someday', status: 'BACKLOG', updated: now - 90 * MIN }),
      task({ id: 'done', title: 'Fix divide by zero', status: 'COMPLETED', completed: now - 26 * 60 * MIN }),
      task({ id: 'fail', title: 'Broken one', status: 'FAILED', updated: now - 10 * MIN }),
    ],
    chats: [chat({ id: 'c-old', title: 'How is rounding done?', updatedAt: now - 20 * MIN }), chat({ id: 'c-run', title: 'Talking', status: 'running', updatedAt: now - 3 * MIN }), chat({ id: 'c-ask', title: 'Asks', updatedAt: now - 50 * MIN })],
    needs: [
      need({ taskId: 'merge', phase: 'merge', severity: 1, context: { hasDiff: true, files: 2, insertions: 42, deletions: 3, checksPassed: 0, checksTotal: 0 } }),
      need({ taskId: 'merge', kind: 'question', severity: 3 }),
      need({ chatId: 'c-ask', kind: 'chat_permission', severity: 2 }),
      // an item of no task or chat of the list is not a row
      need({ kind: 'budget', severity: 0 }),
    ],
  });

  it('groups tasks and chats into Needs you, Running and Earlier, Needs you by urgency, each group newest first', () => {
    const g = groupWork(rows);
    expect(g.needs.map((r) => r.key)).toEqual(['task:merge', 'chat:c-ask', 'task:wait']);
    expect(g.running.map((r) => r.key)).toEqual(['task:run2', 'chat:c-run', 'task:run', 'task:back']);
    expect(g.earlier.map((r) => r.key)).toEqual(['task:fail', 'chat:c-old', 'task:done']);
    expect(rows.map((r) => r.group)).toEqual(['needs', 'needs', 'needs', 'running', 'running', 'running', 'running', 'earlier', 'earlier', 'earlier']);
  });

  it('says each row in one faint line', () => {
    const line = Object.fromEntries(rows.map((r) => [r.key, r.line]));
    expect(line['task:merge']).toBe('Ready to merge · +42 −3');
    expect(line['chat:c-ask']).toBe('Asks to use a tool · Chat');
    expect(line['task:wait']).toBe('Waiting for you · 2 min');
    expect(line['task:run']).toBe('Backend Engineer · 4 min');
    expect(line['task:run2']).toBe('Running · 1 min');
    expect(line['task:back']).toBe('Not started · 1 h');
    expect(line['chat:c-run']).toBe('Chat · answering');
    expect(line['task:done']).toBe('Merged · yesterday');
    expect(line['task:fail']).toBe('Failed · 10 min');
    expect(line['chat:c-old']).toBe('Chat · 20 min');
    const tone = Object.fromEntries(rows.map((r) => [r.key, r.tone]));
    expect([tone['task:merge'], tone['task:run'], tone['task:back'], tone['task:done'], tone['task:fail'], tone['chat:c-old']]).toEqual(['needs', 'running', 'idle', 'done', 'failed', 'quiet']);
  });

  it('a need line names the gate and the size of the change', () => {
    expect(needLine(need({ phase: 'spec' }))).toBe('Accept the spec');
    expect(needLine(need({ kind: 'approval' }))).toBe('Approval');
    expect(needLine(need({ kind: 'something new' }))).toBe('Needs you');
  });

  it('opens the item the URL names, else the first that needs you, else the first running, else none', () => {
    expect(openItem(rows, { kind: 'chat', id: 'c-old' })).toEqual({ kind: 'chat', id: 'c-old' });
    // a task the list does not hold (an older one) still opens
    expect(openItem(rows, { kind: 'task', id: 'gone' })).toEqual({ kind: 'task', id: 'gone' });
    expect(openItem(rows, undefined)).toEqual({ kind: 'task', id: 'merge' });
    const running = rows.filter((r) => r.group !== 'needs');
    expect(openItem(running, undefined)).toEqual({ kind: 'task', id: 'run2' });
    // a task that has not started is not "running": nothing opens by itself
    expect(openItem(rows.filter((r) => r.key === 'task:back' || r.group === 'earlier'), undefined)).toBeUndefined();
    expect(openItem([], undefined)).toBeUndefined();
  });

  it('searches titles and lines, every word, ignoring case', () => {
    expect(filterWork(rows, '').length).toBe(rows.length);
    expect(filterWork(rows, 'MULTIPLY').map((r) => r.key)).toEqual(['task:merge']);
    expect(filterWork(rows, 'chat 20').map((r) => r.key)).toEqual(['chat:c-old']);
    expect(filterWork(rows, 'backend').map((r) => r.key)).toEqual(['task:run']);
    expect(filterWork(rows, 'nothing like it')).toEqual([]);
  });

  it("says an agent's model and effort in a few words, and where its editor and a new agent are", () => {
    const name = (id: string) => (id === 'claude-opus-5-5' ? 'Opus 5.5' : id);
    expect(modelEffort('claude-opus-5-5', 'high', name)).toBe('Opus 5.5 · high');
    expect(modelEffort('', 'medium', name)).toBe('Default model · medium');
    expect(modelEffort('sonnet', '', name)).toBe('sonnet');
    expect(editAgentHref('agt 1')).toBe('/team?edit=agt%201');
    expect(newAgentHref()).toBe('/team?new=agent');
  });
});
