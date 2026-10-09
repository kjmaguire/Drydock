import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { PrebuiltApplyReq } from '../../api/types.ts';
import { byText, cleanup, mount, renderPlain, testServices, waitFor } from '../../test/render.tsx';
import { CATALOG, prebuilt } from '../project/wizard-fixtures.ts';
import { AddPrebuilt, appliedLines } from './add-prebuilt.tsx';
import { AgentGallery, byCategory } from './agent-gallery.tsx';

afterEach(cleanup);

describe('the prebuilt agents gallery', () => {
  it('groups the agents by category in the catalog order, with role, skills, connectors and CLI availability', () => {
    const picked: string[] = [];
    const entries = [...CATALOG, prebuilt('odd', {}), { ...prebuilt('team-lead', { claude: false }), id: 'lead-2' }];
    const r = renderPlain(<AgentGallery entries={entries} onPick={(e) => picked.push(e.id)} />);
    const heads = Array.from(r.container.querySelectorAll('section h3')).map((h) => h.textContent);
    expect(heads).toEqual(['Management', 'Engineering', 'Design', 'Docs', 'Quality']);
    const cf = r.container.querySelector('[data-id="cloudflare-engineer"]')!;
    expect(cf.textContent).toContain('Cloudflare Engineer');
    expect(cf.textContent).toContain('4 skills');
    expect(cf.textContent).toContain('cloudflare-docs');
    expect(cf.textContent).toContain('claude · sonnet');
    expect(cf.textContent).toContain('(installed)');
    expect(r.container.querySelector('[data-id="lead-2"]')!.textContent).toContain('(not installed)');
    byText<HTMLButtonElement>(r.container, 'button', 'Add UI Engineer').click();
    expect(picked).toEqual(['ui-engineer']);
  });

  it('is read-only without onPick (the Library page)', () => {
    const r = renderPlain(<AgentGallery entries={CATALOG} />);
    expect(r.container.querySelectorAll('[data-testid="prebuilt"]').length).toBe(CATALOG.length);
    expect(r.container.querySelectorAll('button').length).toBe(0);
  });

  it('puts unknown categories last', () => {
    expect(byCategory([prebuilt('x'), { ...prebuilt('y'), category: 'research' }, prebuilt('team-lead')]).map(([c]) => c)).toEqual(['management', 'engineering', 'research']);
  });
});

describe('adding a prebuilt agent on the Team page', () => {
  it('applies the agent with the chosen options and reports skills and connectors', async () => {
    const sent: PrebuiltApplyReq[] = [];
    const added: string[] = [];
    const s = testServices({
      agentCatalogList: async () => CATALOG,
      agentCatalogApply: async (req) => (
        sent.push(req),
        {
          id: req.id,
          agent: { id: 'a9', name: 'Cloudflare Engineer' } as never,
          mcp: { granted: [], skipped: [{ id: 'cloudflare-docs', reason: 'needs sign-in on the Library page' }] },
          skills: { assigned: ['cloudflare', 'wrangler'], shared: [], pending: [{ name: 'durable-objects', reason: 'a newer pin is offered' }], failed: [] },
          notes: [],
        }
      ),
    });
    const r = mount(<AddPrebuilt projectId="p1" onAdded={(x) => added.push(x.agent.id)} />, s);
    await waitFor(() => byText(r.container, 'button', 'Add Cloudflare Engineer'), r);
    await r.act(() => (Array.from(r.container.querySelectorAll('label')).find((l) => l.textContent!.startsWith('Connect its MCP connectors'))!.querySelector('input') as HTMLInputElement).click());
    await r.act(() => byText(r.container, 'button', 'Add Cloudflare Engineer').click());
    await waitFor(() => expect(r.container.querySelector('[data-testid="prebuilt-added"]')!.textContent).toContain('Cloudflare Engineer joined the team.'), r);
    expect(sent).toEqual([{ id: 'cloudflare-engineer', projectId: 'p1', installSkills: true, connectMcp: false }]);
    expect(added).toEqual(['a9']);
    const text = r.container.querySelector('[data-testid="prebuilt-added"]')!.textContent!;
    expect(text).toContain('Skills: cloudflare, wrangler.');
    expect(text).toContain('Skill durable-objects waits for your decision: a newer pin is offered');
    expect(text).toContain('Connector cloudflare-docs not connected: needs sign-in on the Library page');
  });

  it('shows the core refusal', async () => {
    const s = testServices({
      agentCatalogList: async () => CATALOG,
      agentCatalogApply: async () => {
        throw new Error('the agent CLI claude is not installed');
      },
    });
    const r = mount(<AddPrebuilt projectId="p1" onAdded={() => undefined} />, s);
    await waitFor(() => byText(r.container, 'button', 'Add Team Lead'), r);
    await r.act(() => byText(r.container, 'button', 'Add Team Lead').click());
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('claude is not installed'), r);
  });

  it('turns an apply result into sentences', () => {
    expect(
      appliedLines({
        id: 'x',
        agent: { name: 'X' } as never,
        mcp: { granted: ['ms-learn'], skipped: [] },
        skills: { assigned: [], shared: [], pending: [], failed: [{ name: 's', reason: 'blocked by policy' }] },
        notes: ['note'],
      }),
    ).toEqual(['Skill s not installed: blocked by policy', 'Connectors: ms-learn.', 'note']);
  });
});
