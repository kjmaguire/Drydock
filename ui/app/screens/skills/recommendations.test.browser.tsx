import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Recommendation, Recommendations as Recs, SkillPick } from '../../api/extTypes.ts';
import type { Agent } from '../../api/types.ts';
import { byLabel, byText, choose, cleanup, mount, testServices, valueOf, waitFor } from '../../test/render.tsx';
import { Recommendations } from './recommendations.tsx';

afterEach(cleanup);

const agents = [
  { id: 'agt_dev', name: 'Backend #1', role: 'backend' },
  { id: 'agt_lead', name: 'Lead', role: 'lead' },
] as Agent[];

const done = (over: Partial<Recommendation> = {}): Recommendation => ({
  id: 'rec_1',
  projectId: 'prj_1',
  agentId: 'agt_lead',
  trigger: 'manual',
  status: 'done',
  candidates: [],
  summary: 'Give the backend agent Wrangler.',
  items: [
    { id: 'r1', kind: 'skill', action: 'add', name: 'wrangler', label: 'wrangler', registryId: 'cloudflare-skills', agents: ['agt_dev'], reason: 'It deploys Workers.', status: 'open' },
    { id: 'r2', kind: 'integration', action: 'add', name: 'linear', label: 'Linear', agents: [], reason: 'The plan tracks issues there.', status: 'open' },
  ],
  dropped: ['recommendation 3 (skill nope): not an available skill of the Library'],
  costUsd: 0.012,
  createdAt: Date.parse('2026-10-14T08:00:00Z'),
  finishedAt: Date.parse('2026-10-14T08:01:00Z'),
  ...over,
});

function show(client: Partial<Client>, picks: SkillPick[] = []) {
  const s = testServices(client);
  const changed: string[] = [];
  let picksDone = 0;
  const r = mount(
    <Recommendations projectId="prj_1" projectName="gocalc" agents={agents} picks={picks} onPicksDone={() => picksDone++} onChanged={() => changed.push('x')} />,
    s,
  );
  return { r, changed, picksDone: () => picksDone };
}

describe('Recommendations', () => {
  it('asks the team lead by default (or the agent chosen) and says the advisor is looking at the project', async () => {
    let list: Recs = { auto: true, runs: [] };
    const started: unknown[][] = [];
    const { r } = show({
      recommendList: async () => list,
      recommendStart: async (...a: unknown[]) => {
        started.push(a);
        list = { auto: true, runs: [done({ status: 'running', agentId: String(a[1]), items: [], dropped: [] })] };
        return list.runs[0]!;
      },
    });
    const ask = await waitFor(() => r.$('[data-testid="recommend-ask"]') as HTMLButtonElement, r);
    const pick = byLabel<HTMLElement>(r.container, 'Agent to ask');
    expect(valueOf(pick)).toBe('agt_lead');
    await r.act(() => choose(pick, 'agt_dev'));
    await r.act(() => ask.click());
    await waitFor(() => expect(started).toEqual([['prj_1', 'agt_dev', []]]), r);
    await waitFor(() => expect(r.$('[data-testid="recommend-running"]')!.textContent).toBe('Backend #1 is looking at the project (when you asked)...'), r);
    expect((r.$('[data-testid="recommend-ask"]') as HTMLButtonElement).disabled).toBe(true);
  });

  it('lists a finished turn: what to do for whom, why, apply and dismiss, an integration pointing to the Integrations page, what was left out', async () => {
    let list: Recs = { auto: true, runs: [done()] };
    const applied: unknown[][] = [];
    const dismissed: unknown[][] = [];
    const { r, changed } = show({
      recommendList: async () => list,
      recommendApply: async (...a: unknown[]) => {
        applied.push(a);
        const d = done();
        d.items[0]!.status = 'applied';
        list = { auto: true, runs: [d] };
        return d;
      },
      recommendDismiss: async (...a: unknown[]) => {
        dismissed.push(a);
        const d = done();
        d.items[0]!.status = 'applied';
        d.items[1]!.status = 'dismissed';
        list = { auto: true, runs: [d] };
        return d;
      },
    });
    const items = await waitFor(() => {
      const xs = r.$$('[data-testid="recommend-item"]');
      if (xs.length !== 2) throw new Error('not yet');
      return Array.from(xs) as HTMLElement[];
    }, r);
    expect(r.$('[data-testid="recommend-summary"]')!.textContent).toBe('Give the backend agent Wrangler.');
    expect(items[0]!.querySelector('.what')!.textContent).toBe('Add the skill wrangler for Backend #1');
    expect(items[0]!.textContent).toContain('It deploys Workers.');
    expect(items[1]!.querySelector('.what')!.textContent).toBe('Assign Linear to this project');
    expect(items[1]!.querySelector('[data-testid="recommend-apply"]')).toBeNull();
    expect(items[1]!.textContent).toContain("Integrations page");
    expect(r.$('[data-testid="recommend-latest"]')!.textContent).toContain('1 suggestion was left out');
    expect(r.$('[data-testid="recommend-latest"]')!.textContent).toContain('$0.012');

    await r.act(() => (items[0]!.querySelector('[data-testid="recommend-apply"]') as HTMLButtonElement).click());
    await waitFor(() => expect(applied).toEqual([['rec_1', 'r1']]), r);
    await waitFor(() => expect(r.$('[data-testid="recommend-item"][data-kind="skill"]')!.getAttribute('data-status')).toBe('applied'), r);
    expect(changed.length).toBe(1);

    await r.act(() => (r.$('[data-testid="recommend-item"][data-kind="integration"] [data-testid="recommend-dismiss"]') as HTMLButtonElement).click());
    await waitFor(() => expect(dismissed).toEqual([['rec_1', 'r2']]), r);
    await waitFor(() => expect(r.$('[data-testid="recommend-latest"]')!.textContent).toContain('Every recommendation was applied or dismissed.'), r);
  });

  it('offers the skills picked in the Library: ask which to use (with the picks), or add them all for the agents ticked (never the whole team)', async () => {
    const picks = [
      { registryId: 'cloudflare-skills', name: 'wrangler' },
      { registryId: 'cloudflare-skills', name: 'durable-objects' },
    ];
    const started: unknown[][] = [];
    const added: unknown[][] = [];
    const one = show(
      {
        recommendList: async () => ({ auto: true, runs: [] }),
        recommendStart: async (...a: unknown[]) => (started.push(a), done({ status: 'running', items: [] })),
      },
      picks,
    );
    const box = await waitFor(() => one.r.$('[data-testid="skill-picks"]') as HTMLElement, one.r);
    expect(box.textContent).toContain('You picked 2 skills in the Library for gocalc');
    expect(box.textContent).toContain('wrangler, durable-objects');
    await one.r.act(() => byText(box, 'button', 'Ask Lead which to use').click());
    await waitFor(() => expect(started).toEqual([['prj_1', 'agt_lead', picks]]), one.r);
    expect(one.picksDone()).toBe(1);
    cleanup();

    const two = show(
      {
        recommendList: async () => ({ auto: true, runs: [] }),
        skillLibraryAdd: async (...a: unknown[]) => (added.push(a), {} as never),
      },
      picks,
    );
    const box2 = await waitFor(() => two.r.$('[data-testid="skill-picks"]') as HTMLElement, two.r);
    // nothing is added until agents are ticked
    expect((byText(box2, 'button', 'Tick the agents to add them for') as HTMLButtonElement).disabled).toBe(true);
    const dev = box2.querySelector('[data-testid="picks-for"] input') as HTMLInputElement; // the first agent: Backend #1
    await two.r.act(() => dev.click());
    await two.r.act(() => byText(box2, 'button', 'Add all 2 for them').click());
    await waitFor(
      () =>
        expect(added).toEqual([
          ['prj_1', 'cloudflare-skills', 'wrangler', 'agents', ['agt_dev']],
          ['prj_1', 'cloudflare-skills', 'durable-objects', 'agents', ['agt_dev']],
        ]),
      two.r,
    );
    await waitFor(() => expect(two.r.$('[data-testid="picks-added"]')!.textContent).toBe('wrangler, durable-objects were added to gocalc for Backend #1.'), two.r);
    expect(two.changed.length).toBe(1);
    expect(two.picksDone()).toBe(1);
  });

  it('says why a turn failed, and switches asking-again-by-itself off', async () => {
    let list: Recs = { auto: true, runs: [done({ status: 'failed', items: [], error: 'budget limit reached: project budget' })] };
    const autos: unknown[][] = [];
    const { r } = show({
      recommendList: async () => list,
      recommendSetAuto: async (...a: unknown[]) => {
        autos.push(a);
        list = { ...list, auto: false };
        return list;
      },
    });
    await waitFor(() => expect(r.container.textContent).toContain('The last recommendation failed: budget limit reached: project budget'), r);
    const box = r.$('[data-testid="recommendations"] input[type="checkbox"]') as HTMLInputElement;
    expect(box.checked).toBe(true);
    await r.act(() => {
      box.checked = false;
      box.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await waitFor(() => expect(autos).toEqual([['prj_1', false]]), r);
  });
});
