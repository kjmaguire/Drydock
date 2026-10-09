import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { TaskDetail } from '../../api/types.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, renderPlain, type, waitFor } from '../../test/render.tsx';
import { ReviewPanel } from '../tasks/review-panel.tsx';
import { detail as baseDetail, recorder, svc, task } from '../tasks/testing.ts';
import { LimitPanel, limitPayload, WatchdogPanel, watchdogPayload, type LimitPayload, type WatchdogPausePayload } from './escalation.tsx';

afterEach(cleanup);

// TEST DOUBLES (labelled): payloads shaped like pipeline.LimitPayload and a watchdog pause (watchdog.Evidence + note).
const limit = (over: Partial<LimitPayload> = {}): LimitPayload => ({
  phase: 'limit',
  taskId: 't1',
  title: 'Add Multiply',
  trigger: 'iteration_limit',
  origin: 'review',
  evidence: 'REVISE: the test for 0 still fails',
  iteration: 5,
  maxIterations: 5,
  note: 'Approve (optionally with data.text: guidance for the team) to continue...',
  ...over,
});
const pause = (over: Partial<WatchdogPausePayload> = {}): WatchdogPausePayload => ({
  signal: 'tool_spin',
  stage: 2,
  evidence: 'Bash(go test ./...) repeated 6 times with the same result',
  handoff: 'Was fixing Multiply; tests fail on 0 * n.',
  note: 'Answer (optionally with data.text...)',
  ...over,
});

const detailWith = (payload: unknown, nodeId = 'limit'): TaskDetail =>
  baseDetail({
    task: task({ title: 'Add Multiply', status: 'WAITING_HUMAN' }),
    pending: [{ id: 'req-9', nodeId, step: 4, spec: { kind: 'question', title: 'Iteration limit reached: a human needs to decide', payload } }],
    run: { id: 'run-3', workflow: 'iterate-until-verified', status: 'WAITING_HUMAN', topology: 'x', created: 0, updated: 0, version: 1 },
  });

describe('the limit question', () => {
  it('is recognized by its phase; a watchdog pause by its signal and stage', () => {
    const req = (payload: unknown) => ({ id: 'r', nodeId: 'n', step: 1, spec: { kind: 'question', title: 't', payload } });
    expect(limitPayload(req(limit()))?.trigger).toBe('iteration_limit');
    expect(limitPayload(req(pause()))).toBeUndefined();
    expect(watchdogPayload(req(pause()))?.signal).toBe('tool_spin');
    expect(watchdogPayload(req({ text: 'x' }))).toBeUndefined();
    expect(watchdogPayload(req(limit()))).toBeUndefined();
  });

  it('shows why, the evidence and the team lead recommendation; Continue with guidance answers approved with data.text', async () => {
    const sent: unknown[][] = [];
    const lead = { agent: 'agt_l', name: 'Team Lead', action: 'escalate', message: 'The reviewer and worker disagree on 0 * n; please decide.', reason: 'Two rounds, same feedback.', exhausted: true };
    const r = renderPlain(<LimitPanel payload={limit({ trigger: 'repeated_revise', lead })} busy={false} readOnly={false} onRespond={async (d, x) => void sent.push([d, x])} />);
    const box = r.container.querySelector('[data-testid="limit-question"]')!;
    expect(box.textContent).toContain('The reviewer keeps asking for the same change.');
    expect(box.textContent).toContain('Iteration 5 of 5');
    expect(r.container.querySelector('[data-testid="limit-evidence"]')!.textContent).toBe('REVISE: the test for 0 still fails');
    const rec = r.container.querySelector('[data-testid="lead-recommendation"]')!;
    expect(rec.textContent).toContain('Team Lead');
    expect(rec.textContent).toContain('recommends: escalate');
    expect(rec.textContent).toContain('please decide');
    expect(rec.textContent).toContain('Why: Two rounds, same feedback.');
    expect(rec.textContent).toContain('out of interventions');
    byText(r.container, 'button', 'Continue').click();
    type(byLabel<HTMLTextAreaElement>(r.container, 'Guidance for the team (optional)'), ' 0 * n is 0; fix the test. ');
    r.root.flush();
    byText(r.container, 'button', 'Continue with guidance').click();
    byText(r.container, 'button', 'Stop the task').click();
    await waitFor(() => expect(sent).toEqual([['approved', undefined], ['approved', { text: '0 * n is 0; fix the test.' }], ['rejected', { action: 'stop' }]]));
  });

  it('read-only turns Continue off; Stop stays', () => {
    const r = renderPlain(<LimitPanel payload={limit()} busy={false} readOnly onRespond={async () => undefined} />);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Continue').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Stop the task').disabled).toBe(false);
  });

  it('on the task page: answers the request of the run through run.respond', async () => {
    const runRespond = recorder(() => undefined);
    const r = mount(<ReviewPanel detail={detailWith(limit())} onChanged={() => undefined} />, svc({ runRespond }));
    expect(r.$('[data-testid="limit-question"]')).not.toBeNull();
    expect(() => byText(r.container, 'button', 'Send answer')).toThrow();
    await r.act(() => byText(r.container, 'button', 'Stop the task').click());
    await waitFor(() => expect(runRespond.calls).toEqual([['run-3', 'req-9', 'rejected', { action: 'stop' }]]), r);
  });

  it('on the task page with an expired license: Continue is off', async () => {
    const r = mount(<ReviewPanel detail={detailWith(limit())} onChanged={() => undefined} />, svc({}, { license: expiredLicense }));
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Continue').disabled).toBe(true), r);
  });
});

describe('a watchdog pause', () => {
  it('shows the signal, the evidence and where the agent left off; guidance answers approved with data.text, Stop rejects', async () => {
    const sent: unknown[][] = [];
    const r = renderPlain(<WatchdogPanel payload={pause()} busy={false} readOnly={false} onRespond={async (d, x) => void sent.push([d, x])} />);
    const box = r.container.querySelector('[data-testid="watchdog-pause"]')!;
    expect(box.textContent).toContain('The agent kept repeating the same tool call.');
    expect(box.textContent).toContain('stage 2');
    expect(r.container.querySelector('[data-testid="watchdog-evidence"]')!.textContent).toContain('repeated 6 times');
    expect(box.querySelector('details')!.textContent).toContain('tests fail on 0 * n');
    expect(r.container.querySelector('[data-testid="lead-recommendation"]')).toBeNull();
    type(byLabel<HTMLTextAreaElement>(r.container, 'Guidance for the agent'), 'Read the failing assertion first.');
    r.root.flush();
    byText(r.container, 'button', 'Send guidance and continue').click();
    byText(r.container, 'button', 'Stop').click();
    await waitFor(() => expect(sent).toEqual([['approved', { text: 'Read the failing assertion first.' }], ['rejected', undefined]]));
  });

  it('a pause the team lead escalated shows its message prominently', () => {
    const r = renderPlain(<WatchdogPanel payload={pause({ signal: 'lead_escalation', lead: 'Ada keeps retrying the same migration; it needs a database decision.' })} busy={false} readOnly={false} onRespond={async () => undefined} />);
    expect(r.container.textContent).toContain('The team lead asks for a human decision.');
    expect(r.container.querySelector('[data-testid="lead-recommendation"]')!.textContent).toContain('it needs a database decision');
  });

  it('on the task page: a watchdog pause answers with guidance through run.respond', async () => {
    const runRespond = recorder(() => undefined);
    const r = mount(<ReviewPanel detail={detailWith(pause(), 'work')} onChanged={() => undefined} />, svc({ runRespond }));
    await r.act(() => type(byLabel<HTMLTextAreaElement>(r.container, 'Guidance for the agent'), 'try a smaller step'));
    await r.act(() => byText(r.container, 'button', 'Send guidance and continue').click());
    await waitFor(() => expect(runRespond.calls).toEqual([['run-3', 'req-9', 'approved', { text: 'try a smaller step' }]]), r);
  });
});
