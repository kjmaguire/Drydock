import { expect } from 'remix/assert';
import { describe, it, mock } from 'remix/test';
import { createClient } from './client.ts';
import { HttpTransport } from './transport.ts';

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
const sent = (f: { mock: { calls: { arguments: unknown[] }[] } }) =>
  f.mock.calls.map((c) => c.arguments as unknown as [string, RequestInit]).map(([url, init]) => [url.replace('/rpc/', ''), JSON.parse(init.body as string)]);

describe('the task data client (P97)', () => {
  it('reads task summaries with empty lists for Go nil and a total of zero when none is sent', async () => {
    const row = { taskId: 't1', projectId: 'p', projectKey: 'GOC', seq: 41, labels: null, checks: [{ kind: 'test', status: 'failed', failing: null }, { kind: 'lint', status: 'not_run' }] };
    const answers: unknown[] = [{ summaries: [row], total: 612, partial: true }, null];
    const f = mock.fn(async () => json({ result: answers.shift() }));
    const c = createClient(new HttpTransport({ token: 'tok', fetchImpl: f as unknown as typeof fetch }));
    const s = await c.taskSummaries({ projectId: '' });
    expect(s.total).toBe(612);
    expect(s.partial).toBe(true);
    expect(s.summaries[0]!.labels).toEqual([]);
    expect(s.summaries[0]!.checks).toEqual([{ kind: 'test', status: 'failed', failing: [] }, { kind: 'lint', status: 'not_run' }]);
    expect(await c.taskSummaries({ projectId: 'p', ids: ['t1'], finishedSince: 5, limit: 10 })).toEqual({ summaries: [], total: 0, partial: false });
    expect(sent(f)).toEqual([
      ['task.summaries', { projectId: '', ids: [], finishedSince: 0, limit: 0 }],
      ['task.summaries', { projectId: 'p', ids: ['t1'], finishedSince: 5, limit: 10 }],
    ]);
  });

  it('sets labels and a project key, and narrows the task list and count', async () => {
    const f = mock.fn(async (url: string) => json({ result: url.endsWith('task.labels.set') ? { id: 't1', labels: null } : url.endsWith('task.count') ? { total: 2 } : url.endsWith('task.list') ? null : { id: 'p', key: 'GX' } }));
    const c = createClient(new HttpTransport({ token: 'tok', fetchImpl: f as unknown as typeof fetch }));
    expect((await c.taskLabelsSet('t1', [])).labels).toEqual([]);
    expect((await c.projectSetKey('p', 'gx')).key).toBe('GX');
    expect(await c.taskList('', [], { assignee: 'a1', label: 'auth', completedSince: 1000 })).toEqual([]);
    expect(await c.taskCount('p', ['COMPLETED'], { completedSince: 1000 })).toEqual({ total: 2 });
    expect(await c.taskList('p')).toEqual([]);
    expect(sent(f)).toEqual([
      ['task.labels.set', { id: 't1', labels: [] }],
      ['project.update', { id: 'p', key: 'gx' }],
      ['task.list', { projectId: '', statuses: [], assignee: 'a1', label: 'auth', completedSince: 1000 }],
      ['task.count', { projectId: 'p', statuses: ['COMPLETED'], completedSince: 1000 }],
      // without a filter the call is what it always was
      ['task.list', { projectId: 'p', statuses: [] }],
    ]);
  });
});
