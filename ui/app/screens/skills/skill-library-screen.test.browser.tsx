import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { LibrarySkill, SkillRegistry, SkillRegistryStatus } from '../../api/extTypes.ts';
import { byLabel, byText, choose, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { SkillLibraryScreen } from './skill-library-screen.tsx';

afterEach(cleanup);

const lib = (over: Partial<LibrarySkill>): LibrarySkill => ({
  id: 'l-art',
  registryId: 'anthropic-skills',
  repo: 'anthropics/skills',
  name: 'algorithmic-art',
  description: 'Creating algorithmic art using p5.js with seeded randomness and interactive parameter exploration. Use this when users request creating art using code, generative art, flow fields, or particle systems.',
  license: 'Apache-2.0',
  globs: [],
  roles: [],
  sourcePath: 'skills/algorithmic-art',
  pinnedCommit: 'abcdef1234567890',
  contentHash: 'h',
  hasScripts: false,
  status: 'available',
  blockReason: '',
  risk: 0,
  flags: [],
  gone: false,
  updated: '',
  ...over,
});
const regs: SkillRegistry[] = [
  { id: 'anthropic-skills', repo: 'anthropics/skills', trust: 'vendor_official', enabled: true, curated: true },
  { id: 'msdocs-agent-skills', repo: 'MicrosoftDocs/Agent-Skills', trust: 'vendor_official', enabled: true, curated: true },
];
const statuses: SkillRegistryStatus[] = [
  { registryId: 'anthropic-skills', repo: 'anthropics/skills', enabled: true, lastCommit: 'abc', checkedAt: '2026-10-14T08:00:00Z', skills: 20, blocked: 1, added: [{ name: 'docx', description: '' }], changed: [], projects: {} },
  { registryId: 'msdocs-agent-skills', repo: 'MicrosoftDocs/Agent-Skills', enabled: true, lastCommit: '', checkedAt: '2026-10-14T08:00:00Z', lastError: 'resolve MicrosoftDocs/Agent-Skills@HEAD: page exceeds crawl.max_page_bytes', skills: 0, blocked: 0, added: [], changed: [], projects: {} },
];

function show(client: Partial<Client> = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as never, { id: 'prj_2', name: 'shop' } as never],
    skillRegistries: async () => regs,
    skillRegistryStatus: async () => statuses,
    skillLibraryList: async () => [
      lib({}),
      lib({ id: 'l-docx', name: 'docx', description: 'Word documents. Create and edit them.' }),
      lib({ id: 'l-pdf', name: 'pdf', description: 'PDF tools.' }),
      lib({ id: 'l-api', name: 'claude-api', description: 'Claude API.', status: 'blocked', blockReason: 'SKILL.md is larger than 64 KiB' }),
    ],
    skillList: async (pid: string) => (pid === 'prj_1' ? [{ name: 'pdf' } as never] : []),
    ...client,
  });
  s.workspace.start();
  const went: string[] = [];
  const r = mount(<SkillLibraryScreen onNavigate={(h) => went.push(h)} />, s);
  return { r, s, went };
}
const rows = (r: ReturnType<typeof show>['r']) => Array.from(r.$$('[data-testid="lib-skill-tile"]')) as HTMLElement[];
/** Wait for one element, failing the wait while it is not there. */
const one = (r: ReturnType<typeof show>['r'], sel: string) =>
  waitFor(() => {
    const el = r.$(sel);
    if (!el) throw new Error(`no ${sel}`);
    return el as HTMLElement;
  }, r);

describe('Library > Skills', () => {
  it('shows a small tile per registry: counts, blocked, new, or that the sync failed; the panel has the details', async () => {
    const { r } = show();
    const tiles = await waitFor(() => {
      const xs = Array.from(r.$$('[data-testid="lib-skill-registry-tile"]')) as HTMLElement[];
      if (xs.length !== 2 || !xs[0]!.textContent!.includes('skills')) throw new Error('not yet');
      return xs;
    }, r);
    expect(tiles[0]!.textContent).toBe('anthropics/skills20 skills · 1 blocked · 1 new');
    expect(tiles[1]!.textContent).toBe('MicrosoftDocs/Agent-SkillsSync failed');
    await r.act(() => tiles[1]!.click());
    const panel = await one(r, '[data-testid="lib-skill-registry"]');
    expect(panel.textContent).toContain('Last sync failed: resolve MicrosoftDocs/Agent-Skills@HEAD: page exceeds crawl.max_page_bytes');
  });

  it('lists skills as one-line rows; a name opens what the skill does in full', async () => {
    const { r } = show();
    const tile = await one(r, '[data-testid="lib-skill-registry-tile"][data-registry="anthropic-skills"]');
    await r.act(() => tile.click());
    await waitFor(() => expect(rows(r).length).toBe(4), r);
    const art = r.$('[data-testid="lib-skill-tile"][data-name="algorithmic-art"]') as HTMLElement;
    expect(art.querySelector('.short')!.textContent).toBe('Creating algorithmic art using p5.js with seeded randomness and interactive parameter exploration.');
    expect(art.querySelector('[data-testid="lib-skill-tile-detail"]')).toBeNull();
    expect(r.$('[data-testid="lib-skill-tile"][data-name="docx"]')!.textContent).toContain('new');
    expect(r.$('[data-testid="lib-skill-tile"][data-name="pdf"]')!.textContent).toContain('In gocalc ✓');
    // the name starts at the row's left, not where the app's button rule would centre it
    expect(getComputedStyle(art.querySelector('button.what')!).justifyContent).toBe('flex-start');
    await r.act(() => (art.querySelector('button.what') as HTMLButtonElement).click());
    const detail = await one(r, '[data-testid="lib-skill-tile"][data-name="algorithmic-art"] [data-testid="lib-skill-tile-detail"]');
    expect(detail.textContent).toContain('Use this when users request creating art using code');
    expect(detail.textContent).toContain('Apache-2.0');
    expect((art.querySelector('button.what') as HTMLElement).getAttribute('aria-expanded')).toBe('true');
    // a blocked skill and one the project has cannot be picked
    expect((r.$('[data-testid="lib-skill-tile"][data-name="claude-api"] input') as HTMLInputElement).disabled).toBe(true);
    expect((r.$('[data-testid="lib-skill-tile"][data-name="pdf"] input') as HTMLInputElement).disabled).toBe(true);
    // each box is hit through a 28 px label around it (the density test's target rule: a bare box was 16 px)
    const pick = art.querySelector('[data-testid="skill-pick"]') as HTMLElement;
    const box = pick.getBoundingClientRect();
    expect(box.width >= 28 && box.height >= 28).toBe(true);
    expect(pick.querySelector('input')!.getAttribute('aria-label')).toBe('Pick algorithmic-art');
    await r.act(() => pick.click());
    expect((pick.querySelector('input') as HTMLInputElement).checked).toBe(true);
  });

  it('takes several picked skills to the chosen project: selects it, hands the picks over and opens its Skills page', async () => {
    const { r, s, went } = show();
    const tile = await one(r, '[data-testid="lib-skill-registry-tile"][data-registry="anthropic-skills"]');
    await r.act(() => tile.click());
    await waitFor(() => expect(rows(r).length).toBe(4), r);
    const add = r.$('[data-testid="lib-skill-add-picked"]') as HTMLButtonElement;
    expect(add.disabled).toBe(true);
    // Pick all takes the addable ones only (not the blocked one, not the one the project has)
    await r.act(() => byText(r.container, 'button', 'Pick all 2').click());
    await waitFor(() => expect((r.$('[data-testid="lib-skill-add-picked"]') as HTMLElement).textContent).toBe('Add 2 skills to gocalc'), r);
    // the bar's buttons sit on the line of the Project dropdown, not on the middle of its label and dropdown together
    // (the owner's screenshot of 2026-10-20: "Pick all 16" sat below "Project" and above the dropdown)
    const sel = byLabel<HTMLElement>(r.container, 'Project to add skills to');
    const mid = (el: Element) => {
      const b = el.getBoundingClientRect();
      return (b.top + b.bottom) / 2;
    };
    for (const b of [add, byText(r.container, 'button', 'Pick all 2')]) expect(Math.abs(mid(b) - mid(sel)) <= 3).toBe(true);
    // another project
    await r.act(() => choose(sel, 'prj_2'));
    await waitFor(() => expect((r.$('[data-testid="lib-skill-add-picked"]') as HTMLElement).textContent).toBe('Add 2 skills to shop'), r);
    await r.act(() => (r.$('[data-testid="lib-skill-add-picked"]') as HTMLButtonElement).click());
    expect(went).toEqual(['/skills']);
    expect(s.workspace.projectId).toBe('prj_2');
    expect(s.ui.takeSkillPicks('prj_1')).toEqual([]);
    expect(s.ui.takeSkillPicks('prj_2')).toEqual([
      { registryId: 'anthropic-skills', name: 'algorithmic-art' },
      { registryId: 'anthropic-skills', name: 'docx' },
    ]);
    expect(s.ui.takeSkillPicks('prj_2')).toEqual([]); // once
  });

  it('says to create a project first when there is none', async () => {
    const { r } = show({ projectList: async () => [] });
    await waitFor(() => expect(r.$('[data-testid="skill-library-no-project"]')).not.toBeNull(), r);
  });
});
