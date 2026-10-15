import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { PrebuiltApplyReq } from '../../api/types.ts';
import { byText, cleanup, mount, renderPlain, testServices, waitFor } from '../../test/render.tsx';
import { CATALOG, prebuilt } from '../project/wizard-fixtures.ts';
import { AddPrebuilt, appliedLines } from './add-prebuilt.tsx';
import { AgentGallery, byCategory, serversOf, skillsOf } from './agent-gallery.tsx';

afterEach(cleanup);

/** The element, or a throw so waitFor keeps waiting. */
const must = <T,>(x: T | null | undefined): T => {
  if (x == null) throw new Error('not there yet');
  return x;
};

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
    expect(cf.textContent).toContain('Cloudflare Documentation');
    expect(cf.textContent).not.toContain('cloudflare-docs');
    expect(cf.querySelector('[data-testid="prebuilt-skills-chip"]')!.getAttribute('title')).toBe('Skills: skill-0, skill-1, skill-2, skill-3');
    expect(cf.querySelector('[data-testid="prebuilt-server-chip"]')!.getAttribute('title')).toBe('MCP server Cloudflare Documentation by Cloudflare: Search the Cloudflare developer documentation.');
    expect(cf.textContent).toContain('claude · sonnet');
    expect(cf.textContent).toContain('(installed)');
    expect(r.container.querySelector('[data-id="lead-2"]')!.textContent).toContain('(not installed)');
    byText<HTMLButtonElement>(r.container, 'button', 'Add UI Engineer').click();
    expect(picked).toEqual(['ui-engineer']);
  });

  it('shows Claude Code installed only in a WSL distro as installed there (P58)', () => {
    const e = prebuilt('team-lead', { claude: false });
    e.availability.adapters[0] = { adapter: 'claude', preferred: true, installed: true, usable: true, status: 'degraded', wslDistro: 'Ubuntu-26.04' };
    e.availability.preferredUsable = true;
    const r = renderPlain(<AgentGallery entries={[e]} />);
    const card = r.container.querySelector('[data-id="team-lead"]')!;
    expect(card.textContent).toContain('(installed in WSL Ubuntu-26.04)');
    expect(card.querySelector('.cli')!.getAttribute('title')).toBe('claude is installed in WSL Ubuntu-26.04');
  });

  it('is read-only without onPick (the Library page)', () => {
    const r = renderPlain(<AgentGallery entries={CATALOG} />);
    expect(r.container.querySelectorAll('[data-testid="prebuilt"]').length).toBe(CATALOG.length);
    const buttons = Array.from(r.container.querySelectorAll('button')).map((b) => (b.getAttribute('aria-label') ?? b.textContent ?? '').trim());
    expect(buttons.filter((b) => b.startsWith('Add')).length).toBe(0);
  });

  it('opens an agent to show its model and CLI, each skill with what it does, where it comes from and its license, and its MCP servers by name', async () => {
    const picked: string[] = [];
    const r = mount(<AgentGallery entries={CATALOG} onPick={(e) => picked.push(e.id)} />, testServices({}));
    const cf = r.container.querySelector('[data-id="cloudflare-engineer"]')!;
    await r.act(() => byText(cf, 'button', 'What Cloudflare Engineer comes with').click());
    const d = await waitFor(() => must(document.querySelector('[data-testid="prebuilt-details"]')) as HTMLElement, r);
    expect(d.querySelector('[data-testid="prebuilt-cli"]')!.textContent).toContain('claude');
    expect(d.querySelector('[data-testid="prebuilt-model"]')!.textContent).toBe('sonnet');
    const skills = Array.from(d.querySelectorAll('[data-testid="prebuilt-skill"]'));
    expect(skills.length).toBe(4);
    const first = skills[0] as HTMLElement;
    expect(first.textContent).toContain('skill-0');
    expect(first.textContent).toContain('What skill 0 does, from its SKILL.md.');
    expect(first.textContent).toContain('MIT');
    expect(first.textContent).toContain('pinned at a18ffe2a9944');
    const link = first.querySelector('a')!;
    expect(link.textContent).toContain('acme/skills');
    expect(link.getAttribute('href')).toBe('https://github.com/acme/skills/tree/a18ffe2a99448ebff262fc87915b0fd14bab9b70/skills/skill-0');
    expect(link.getAttribute('target')).toBe('_blank');
    const server = d.querySelector('[data-testid="prebuilt-server"]') as HTMLElement;
    expect(server.textContent).toContain('Cloudflare Documentation');
    expect(server.textContent).toContain('by Cloudflare');
    expect(server.querySelector('[data-brand="cloudflare"]')).not.toBe(null);
    // the details dialog can add the agent too
    await r.act(() => byText(document.body, 'button', 'Add Cloudflare Engineer').click());
    expect(picked).toEqual(['cloudflare-engineer']);
    // the skills badge opens the same details
    await r.act(() => byText(cf, 'button', /^4 skills: skill-0, skill-1, skill-2, skill-3/).click());
    await waitFor(() => expect(document.querySelectorAll('[data-testid="prebuilt-skill"]').length).toBe(4), r);
  });

  it('falls back to the references when the core sent no details', () => {
    const e = { ...CATALOG.find((x) => x.id === 'cloudflare-engineer')!, availability: { ...CATALOG[0]!.availability, mcp: [], skills: [] } };
    expect(serversOf(e)).toEqual([{ id: 'cloudflare-docs', display: 'cloudflare-docs', publisher: '', brand: 'cloudflare-docs', summary: '' }]);
    expect(skillsOf(e)[0]).toMatchObject({ name: 'skill-0', description: '', repo: 'r' });
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
