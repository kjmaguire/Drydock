import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { LeadIntervention } from '../../api/leadClient.ts';
import type { Agent, Room } from '../../api/types.ts';
import { lic } from '../../test/fixtures.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { Interventions, leadOf, RunStandup } from './lead.tsx';

afterEach(cleanup);

const agent = (over: Partial<Agent>): Agent => ({ id: 'agt_1', projectId: 'p1', name: 'Ada', adapter: 'claude', role: 'backend', model: '', effort: '', providerId: '', permissionMode: '', status: '', ...over });
const lead = agent({ id: 'agt_l', name: 'Team Lead', role: 'lead' });
const ada = agent({});
const general: Room = { id: 'room_g', projectId: 'p1', kind: 'channel', name: 'general', mode: 'auto', archived: false, createdAt: '', version: 1 };

// TEST DOUBLES (labelled): interventions shaped like pipeline.Intervention.
const iv = (over: Partial<LeadIntervention>): LeadIntervention => ({
  id: 'i1',
  projectId: 'p1',
  taskId: 't1',
  runId: 'run1',
  leadAgentId: 'agt_l',
  trigger: 'stalled',
  action: 'redirect',
  message: 'Ada: go back to the failing test before touching the parser.',
  reason: 'No progress for 20 minutes.',
  targetAgent: 'agt_1',
  createdTasks: [],
  invalid: false,
  createdAt: 1_760_000_000_000,
  ...over,
});

function svc(client: Partial<Client> = {}, agents: Agent[] = [lead, ada]) {
  const s = testServices({
    projectList: async () => [{ id: 'p1', name: 'gocalc' }] as never,
    licenseStatus: async () => lic(),
    appInfo: async () => ({}) as never,
    agentList: async () => agents,
    roomList: async () => [general],
    roomUnread: async () => [],
    presenceList: async () => [],
    needsYouList: async () => [],
    taskList: async () => [{ id: 't1', seq: 3, title: 'Add Multiply' } as never, { id: 't9', seq: 9, title: 'Split parser' } as never],
    adapterList: async () => [],
    agentTemplateList: async () => [],
    leadInterventions: async () => [],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

describe('the team lead', () => {
  it('is the agent whose role is lead', () => {
    expect(leadOf([ada, agent({ id: 'x', role: ' Lead ' })])?.id).toBe('x');
    expect(leadOf([ada])).toBeUndefined();
  });

  it('runs a stand-up: busy while it runs, then says it posted in general with a link', async () => {
    let release!: () => void;
    const calls: string[] = [];
    const s = svc({ leadStandup: (p) => (calls.push(p), new Promise((res) => (release = () => res({ messageId: 'm1', text: 'stand-up', tasks: 2 })))) });
    const r = mount(<RunStandup projectId="p1" lead={lead} />, s);
    await waitFor(() => expect(s.project.roomList).toHaveLength(1), r);
    await r.act(() => byText(r.container, 'button', 'Run stand-up').click());
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Running stand-up...').disabled).toBe(true), r);
    release();
    await waitFor(() => expect(r.$('[role="status"]')!.textContent).toContain('Posted in general (2 tasks).'), r);
    expect(r.$('[role="status"] a')!.getAttribute('href')).toBe('/room/room_g');
    expect(calls).toEqual(['p1']);
  });

  it('shows the core refusal of a stand-up', async () => {
    const r = mount(<RunStandup projectId="p1" lead={lead} />, svc({ leadStandup: async () => Promise.reject(new Error('the team lead is already busy')) }));
    await r.act(() => byText(r.container, 'button', 'Run stand-up').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('already busy'), r);
  });

  it('lists interventions newest first, with the trigger, the action, the target, created tasks and invalid ones; re-reads on lead.intervened', async () => {
    let list = [iv({ id: 'old', createdAt: 1_760_000_000_000 }), iv({ id: 'new', trigger: 'iteration_limit', action: 'split', createdTasks: ['t9'], invalid: true, createdAt: 1_760_000_100_000 })];
    const asked: [string, string | undefined][] = [];
    const s = svc({ leadInterventions: async (p, t) => (asked.push([p, t]), list) });
    const r = mount(<Interventions projectId="p1" />, s);
    await waitFor(() => expect(r.$$('[data-testid="intervention"]').length).toBe(2), r);
    const [first, second] = Array.from(r.$$('[data-testid="intervention"]'));
    expect(first!.getAttribute('data-action')).toBe('split');
    expect(first!.textContent).toContain('loop budget used up');
    expect(first!.textContent).toContain('not applied');
    await waitFor(() => expect(first!.textContent).toContain('#9 Split parser'), r);
    expect(second!.textContent).toContain('no progress');
    expect(second!.textContent).toContain('to Ada');
    expect(second!.textContent).toContain('go back to the failing test');
    expect(second!.textContent).toContain('Why: No progress for 20 minutes.');
    expect(second!.querySelector('a')!.getAttribute('href')).toBe('/task/t1');
    list = [...list, iv({ id: 'newest', action: 'escalate', createdAt: 1_760_000_200_000 })];
    s.emit({ type: 'lead.intervened', projectId: 'p1', taskId: 't1' });
    await waitFor(() => expect(r.$$('[data-testid="intervention"]').length).toBe(3), r);
    // a turn that failed is listed too, and says it counts toward the lead's limit
    list = [...list, iv({ id: 'failed', action: 'failed', message: "The team lead's turn failed: claude exited", reason: '', targetAgent: '', createdAt: 1_760_000_300_000 })];
    s.emit({ type: 'lead.intervened', projectId: 'p1', taskId: 't1' });
    await waitFor(() => expect(r.$$('[data-testid="intervention"]').length).toBe(4), r);
    const failed = r.$('[data-testid="intervention"][data-action="failed"]')!;
    expect(failed.textContent).toContain('turn failed: claude exited');
    expect(failed.querySelector('[title*="counts toward"]')).not.toBeNull();
    expect(asked[0]).toEqual(['p1', undefined]);
  });

  it('on a task page lists only that task and says so when there are none', async () => {
    const asked: [string, string | undefined][] = [];
    const r = mount(<Interventions projectId="p1" taskId="t1" />, svc({ leadInterventions: async (p, t) => (asked.push([p, t]), []) }));
    await waitFor(() => expect(r.container.textContent).toContain('has not stepped in on this task'), r);
    expect(asked).toEqual([['p1', 't1']]);
  });
});
