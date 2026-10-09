import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Client } from '../api/client';
import type { ExecView, GraphView, RunExplanation } from '../api/runClient';
import { layout, nodeStates } from '../lib/graphLayout';
import { parseRoute } from '../lib/route';
import { AppServicesProvider } from '../state/context';
import { services } from '../test/fakes';
import { RunView } from './RunView';

vi.mock('../components/WorkflowGraph', () => ({ default: () => <div data-testid="graph-stub" /> }));

const withServices = (ui: React.ReactElement, client: Partial<Client>) => render(<AppServicesProvider services={services(client)}>{ui}</AppServicesProvider>);

const g: GraphView = {
  name: 'iuv',
  entry: ['prepare'],
  nodes: ['prepare', 'work', 'verify', 'review', 'approve', 'done'].map((id) => ({ id, kind: id === 'work' || id === 'review' ? 'agent_task' : id === 'approve' ? 'request' : 'check' })),
  edges: [
    { from: 'prepare', to: 'work', kind: 'direct' },
    { from: 'work', to: 'verify', kind: 'direct' },
    { from: 'verify', to: 'work', kind: 'loop', when: 'fail', maxIter: 5 },
    { from: 'verify', to: 'review', kind: 'conditional', when: 'pass' },
    { from: 'review', to: 'approve', kind: 'direct' },
    { from: 'review', to: 'work', kind: 'loop', when: 'revise' },
    { from: 'approve', to: 'done', kind: 'direct' },
  ],
};

const ex = (nodeId: string, step: number, status = 'done', attempt = 1, extra: Partial<ExecView> = {}): ExecView => ({ nodeId, step, attempt, status, started: 1000 * step, finished: 1000 * step + 500, ...extra });

describe('the workflow layout', () => {
  it('puts each node in the column of its longest path from the entry, ignoring loops, and is stable', () => {
    const p = layout(g);
    const col = Object.fromEntries(p.map((n) => [n.id, n.column]));
    expect(col).toEqual({ prepare: 0, work: 1, verify: 2, review: 3, approve: 4, done: 5 });
    expect(layout(g)).toEqual(p);
    // a node nothing reaches goes after the rest
    const lone = layout({ ...g, nodes: [...g.nodes, { id: 'orphan', kind: 'check' }] }).find((n) => n.id === 'orphan');
    expect(lone?.column).toBe(6);
  });

  it('derives node states from executions, as of a step, and marks the nodes waiting on a human', () => {
    const execs = [ex('prepare', 1), ex('work', 2), ex('verify', 3, 'failed'), ex('work', 4, 'done', 2), ex('verify', 5)];
    const s = nodeStates(execs);
    expect(s.get('work')).toMatchObject({ status: 'done', runs: 2, attempts: 2 });
    expect(s.get('verify')?.status).toBe('done');
    const early = nodeStates(execs, 3, ['approve']);
    expect(early.get('verify')?.status).toBe('failed');
    expect(early.get('work')?.runs).toBe(1);
    expect(early.get('approve')?.status).toBe('waiting');
  });
});

describe('the run route', () => {
  it('parses a task run, with and without a run id', () => {
    expect(parseRoute('#/task/t1/run')).toEqual({ name: 'run', taskId: 't1', runId: undefined });
    expect(parseRoute('#/task/t1/run/run_2')).toEqual({ name: 'run', taskId: 't1', runId: 'run_2' });
    expect(parseRoute('#/task/t1')).toEqual({ name: 'task', id: 't1' });
  });
});

const explanation = (): RunExplanation => ({
  run: { id: 'run_1', workflow: 'iuv', status: 'WAITING_HUMAN', created: 1, updated: 2 },
  runs: [{ id: 'run_1', workflow: 'iuv', status: 'WAITING_HUMAN', created: 1 }],
  graph: g,
  executions: [
    ex('prepare', 1),
    ex('work', 2, 'done', 1, {
      agentId: 'agt_w',
      hasPrompt: true,
      logId: 'nx:run_1|work|2|1',
      includedBytes: 2048,
      manifest: [
        { source: 'prompt (work)', included: true, bytes: 2048, reason: 'task spec', priority: 1 },
        { source: 'repo map', included: false, bytes: 0, reason: 'not built', priority: 9 },
      ],
      decision: { id: 'dec_1', decidedBy: 'static', model: 'fake-1', reason: 'team setup' },
      usage: [{ ts: 2500, taskId: 't1', agentId: 'agt_w', provider: 'anthropic', model: 'fake-1', tier: 'premium', inputTokens: 10, outputTokens: 5, cachedTokens: 0, cacheWriteTokens: 0, reasoningTokens: 0, costUsd: 0.02, source: 'reported', cacheHit: false }],
    }),
  ],
  checkpoints: [
    { id: 'cp1', step: 0, kind: 'entry', created: 1, status: 'RUNNING' },
    { id: 'cp2', step: 2, kind: 'step', created: 2, status: 'WAITING_HUMAN', latest: true, waiting: ['approve'] },
  ],
  mcpCalls: [{ seq: 9, ts: 3, type: 'mcp.denied', agentId: 'agt_w', data: { tool: 'search_pack' } }],
  events: [{ seq: 1, ts: 1, type: 'workflow.started' }],
});

describe('the run page', () => {
  it('explains each turn: manifest, the exact prompt, why this model, usage; and lists checkpoints, MCP calls and events', async () => {
    const runPrompt = vi.fn(async () => 'Implement Subtract.\n<script>x</script>');
    const { container } = withServices(<RunView taskId="t1" agents={[{ id: 'agt_w', name: 'Worker One' } as never]} />, {
      runExplain: async () => explanation(),
      runPrompt,
      taskGet: async () => ({ task: { id: 't1', seq: 3, title: 'Subtract' } }) as never,
    });
    expect(await screen.findByTestId('graph-stub')).toBeTruthy();
    fireEvent.click(screen.getByRole('tab', { name: /Turns/ }));
    const work = screen.getAllByTestId('turn').find((t) => t.getAttribute('data-node') === 'work')!;
    expect(work.textContent).toContain('Worker One');
    expect(work.textContent).toContain('fake-1');
    fireEvent.click(work.querySelector('button')!);
    expect(screen.getAllByTestId('manifest-item').map((r) => r.getAttribute('data-included'))).toEqual(['true', 'false']);
    expect(screen.getByText('team setup')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Show the exact prompt' }));
    await waitFor(() => expect(screen.getByTestId('prompt-text').textContent).toContain('Implement Subtract.'));
    expect(runPrompt).toHaveBeenCalledWith('t1', 'run_1', 'work', 2, 1);
    expect(container.querySelector('script')).toBeNull();

    fireEvent.click(screen.getByRole('tab', { name: /Checkpoints/ }));
    expect(screen.getAllByTestId('checkpoint').map((c) => c.getAttribute('data-latest'))).toEqual(['false', 'true']);
    fireEvent.click(screen.getByRole('tab', { name: /MCP calls/ }));
    expect(screen.getByText('denied')).toBeTruthy();
    fireEvent.click(screen.getByRole('tab', { name: /Events/ }));
    expect(screen.getByText('workflow.started')).toBeTruthy();
  });

  it('says so when the task never ran', async () => {
    withServices(<RunView taskId="t1" agents={[]} />, { runExplain: async () => ({ runs: [], executions: [], checkpoints: [], mcpCalls: [], events: [] }), taskGet: async () => ({ task: { id: 't1', seq: 1, title: 'x' } }) as never });
    expect(await screen.findByText('This task has not run yet.')).toBeTruthy();
  });
});
