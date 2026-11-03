import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { DrydockEvent, NeedsYouItem } from '../api/types.ts';
import { chatToast, hereWithWork, NeedsDiff, needsToast, taskToast } from './toasts.ts';

const item = (key: string, extra: Partial<NeedsYouItem> = {}): NeedsYouItem =>
  ({ key, kind: 'approval', severity: 0, title: 'Permission request: Bash rm -rf build', createdAt: '2026-11-03T10:00:00Z', ...extra }) as NeedsYouItem;
const ev = (type: string, extra: Partial<DrydockEvent> = {}): DrydockEvent => ({ seq: 1, time: '2026-11-03T10:00:00Z', type, ...extra }) as DrydockEvent;
const away = { inbox: false };

describe('NeedsDiff (G2)', () => {
  it('seeds on the first list, then tells the new keys once', () => {
    const d = new NeedsDiff();
    expect(d.next([item('a'), item('b')], 0)).toEqual([]);
    expect(d.next([item('a'), item('b'), item('c')], 0).map((x) => x.key)).toEqual(['c']);
    expect(d.next([item('a'), item('b'), item('c')], 0)).toEqual([]);
  });

  it('says nothing for the first list after the stream reopens (what came while away is in the count)', () => {
    const d = new NeedsDiff();
    d.next([item('a')], 0);
    expect(d.next([item('a'), item('b'), item('c')], 1)).toEqual([]);
    expect(d.next([item('a'), item('b'), item('c'), item('d')], 1).map((x) => x.key)).toEqual(['d']);
  });

  it('an item answered and asked again is new again', () => {
    const d = new NeedsDiff();
    d.next([item('a')], 0);
    d.next([], 0);
    expect(d.next([item('a')], 0).map((x) => x.key)).toEqual(['a']);
  });
});

describe('what a toast says (G2)', () => {
  it('a new item: its kind, project and task, never the request text', () => {
    const t = needsToast(item('a', { taskId: 't1', projectId: 'p1' }), away, { projectName: 'gocalc', task: { projectKey: 'GOC', seq: 41, title: 'Add subtract' } })!;
    expect(t.text).toBe('Needs you: approval');
    expect(t.detail).toBe('gocalc · GOC-41 Add subtract');
    expect(JSON.stringify(t)).not.toContain('rm -rf');
    expect(t.action!.label).toBe('Open');
  });

  it('keeps the names in a kind: only its first letter is lowered', () => {
    expect(needsToast(item('a', { kind: 'connection' }), away)!.text).toBe('Needs you: agent CLI');
    expect(needsToast(item('a', { kind: 'pr_review' }), away)!.text).toBe('Needs you: changes asked on GitHub');
  });

  it("on Work, the item it has open is here, named or opened by default", () => {
    const work = { inbox: false, work: true };
    expect(hereWithWork(work, { kind: 'task', id: 't1' }).taskId).toBe('t1');
    expect(hereWithWork(work, { kind: 'chat', id: 'c1' }).chatId).toBe('c1');
    expect(taskToast(ev('task.completed', { taskId: 't1' }), hereWithWork(work, { kind: 'task', id: 't1' }))).toBe(undefined);
    expect(needsToast(item('a', { taskId: 't1' }), hereWithWork(work, { kind: 'task', id: 't1' }))).toBe(undefined);
    expect(chatToast(ev('chat.turn', { data: { chatId: 'c1', status: 'done' } }), hereWithWork(work, { kind: 'chat', id: 'c1' }))).toBe(undefined);
    expect(needsToast(item('a', { chatId: 'c1' }), hereWithWork(work, { kind: 'chat', id: 'c1' }))).toBe(undefined);
    // not on Work: what Work had open last is not here
    expect(hereWithWork(away, { kind: 'task', id: 't1' }).taskId).toBe(undefined);
  });

  it('is not shown where the person already looks', () => {
    expect(needsToast(item('a'), { inbox: true })).toBe(undefined);
    expect(needsToast(item('a', { taskId: 't1' }), { inbox: false, taskId: 't1' })).toBe(undefined);
    expect(needsToast(item('a', { chatId: 'c1' }), { inbox: false, chatId: 'c1' })).toBe(undefined);
  });

  it('a task that ended elsewhere, with Open to its page', () => {
    const t = taskToast(ev('task.completed', { taskId: 't1' }), away, { projectKey: 'GOC', seq: 7, title: 'Fix it' })!;
    expect([t.text, t.detail, t.tone, t.action!.href]).toEqual(['Task finished', 'GOC-7 Fix it', 'ok', '/task/t1']);
    expect(taskToast(ev('task.failed', { taskId: 't1' }), away)!.text).toBe('Task failed');
    expect(taskToast(ev('task.cancelled', { taskId: 't1' }), away)!.text).toBe('Task cancelled');
    expect(taskToast(ev('task.completed', { taskId: 't1' }), { inbox: false, taskId: 't1' })).toBe(undefined);
    expect(taskToast(ev('task.started', { taskId: 't1' }), away)).toBe(undefined);
  });

  it('a chat that answered elsewhere, not its title; a stopped turn says nothing', () => {
    const t = chatToast(ev('chat.turn', { data: { chatId: 'c1', status: 'done' } }), away, 'gocalc')!;
    expect([t.text, t.detail, t.action!.href]).toEqual(['A chat answered', 'gocalc', '/chats/c1']);
    expect(chatToast(ev('chat.turn', { data: { chatId: 'c1', status: 'error' } }), away)!.text).toBe('A chat turn failed');
    expect(chatToast(ev('chat.turn', { data: { chatId: 'c1', status: 'stopped' } }), away)).toBe(undefined);
    expect(chatToast(ev('chat.turn', { data: { chatId: 'c1', status: 'done' } }), { inbox: false, chatId: 'c1' })).toBe(undefined);
  });
});
