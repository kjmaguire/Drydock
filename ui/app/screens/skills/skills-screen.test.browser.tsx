import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { LibrarySkill, Skill, SkillRegistry, SkillRegistryStatus, SkillUsage } from '../../api/extTypes.ts';
import type { Agent } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { SkillsScreen } from './skills-screen.tsx';

afterEach(cleanup);

const skill = (over: Partial<Skill>): Skill => ({
  id: 's',
  projectId: 'prj_1',
  name: 'go-style',
  description: 'Go style',
  globs: ['**/*.go'],
  roles: ['Backend'],
  origin: 'repository',
  contentHash: 'h',
  status: 'offered',
  audience: 'team',
  agents: [],
  created: '',
  updated: '',
  body: 'Keep functions small. <script>window.__xss=1</script>',
  ...over,
});

const lib = (over: Partial<LibrarySkill>): LibrarySkill => ({
  id: 'l-pdf',
  registryId: 'anthropic-skills',
  repo: 'anthropics/skills',
  name: 'pdf',
  description: 'PDF tools',
  license: 'MIT',
  globs: [],
  roles: [],
  sourcePath: 'skills/pdf',
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

const anthropic: SkillRegistry = { id: 'anthropic-skills', repo: 'anthropics/skills', trust: 'curated', enabled: true, curated: true };
const status = (over: Partial<SkillRegistryStatus>): SkillRegistryStatus => ({
  registryId: 'anthropic-skills',
  repo: 'anthropics/skills',
  enabled: true,
  lastCommit: 'def4567890ab',
  checkedAt: '2026-10-13T08:00:00Z',
  skills: 12,
  blocked: 1,
  added: [],
  changed: [],
  projects: {},
  ...over,
});
const usage = (over: Partial<SkillUsage>): SkillUsage => ({
  name: 'go-style',
  registryId: '',
  origin: 'repo',
  status: 'accepted',
  loaded: 0,
  listed: 0,
  opened: 0,
  tasks: 0,
  tokensLoaded: 0,
  tokensListed: 0,
  recommendation: '',
  reason: '',
  ...over,
});

function servicesWith(client: Partial<Client>) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as never],
    skillRegistries: async () => [],
    skillRegistryStatus: async () => [],
    skillLibraryList: async () => [],
    skillUsage: async () => [],
    skillDeliveries: async () => [],
    skillCoverage: async () => ({ files: 0, tool: 0 }),
    ...client,
  });
  s.workspace.start();
  return s;
}

type R = ReturnType<typeof mount>;
const exists = (root: ParentNode, sel: string, text: string) => Array.from(root.querySelectorAll(sel)).some((e) => e.textContent?.trim() === text);
const tile = (r: R, name: string) => r.$(`[data-testid="skill-tile"][data-name="${name}"]`) as HTMLButtonElement | null;
/** Wait for one element, failing the wait while it is not there. */
const one = (r: R, sel: string) =>
  waitFor(() => {
    const el = r.$(sel);
    if (!el) throw new Error(`no ${sel}`);
    return el as HTMLElement;
  }, r);
/** Click a project skill's tile and return the panel that opens below the grid. */
async function open(r: R, name: string): Promise<HTMLElement> {
  const b = await waitFor(() => {
    const x = tile(r, name);
    if (!x) throw new Error(`no tile ${name}`);
    return x;
  }, r);
  await r.act(() => b.click());
  return waitFor(() => {
    const p = r.$(`[data-testid="skill"][data-skill="${name}"]`);
    if (!p) throw new Error('no panel');
    return p as HTMLElement;
  }, r);
}
const stateOf = (b: Element) => b.querySelector('small[data-tone], small:last-child')?.textContent ?? '';

describe('SkillsScreen', () => {
  it('shows project skills as tiles with their state and a check on accepted ones; a repository skill has no brand, a registry one its logo', async () => {
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => [
          skill({ status: 'accepted' }),
          skill({ id: 'w', name: 'wrangler', origin: 'registry', registryId: 'cloudflare-skills', status: 'accepted', audience: 'agents', agents: ['a', 'b'], description: 'Cloudflare Workers' }),
          skill({ id: 'o', name: 'pdf', origin: 'registry', registryId: 'anthropic-skills', status: 'offered' }),
          skill({ id: 'b', name: 'danger', status: 'blocked', blockReason: 'contains a secret' }),
          skill({ id: 'x', name: 'old', status: 'rejected' }),
        ],
      }),
    );
    await waitFor(() => expect(r.$$('[data-testid="skill-tile"]').length).toBe(5), r);
    const go = tile(r, 'go-style')!;
    expect(go.getAttribute('data-status')).toBe('accepted');
    expect(go.getAttribute('data-check')).toBe('true');
    expect(go.querySelector('[data-brand]')).toBeNull(); // a repository skill: the file icon
    expect(go.querySelector('svg')).not.toBeNull();
    expect(stateOf(go)).toBe('Accepted ✓ · whole team');
    expect(go.querySelector('small[data-tone="ok"]')).not.toBeNull();
    const w = tile(r, 'wrangler')!;
    expect(w.querySelector('[data-brand="cloudflare"]')).not.toBeNull();
    expect(stateOf(w)).toBe('Accepted ✓ · 2 agents');
    expect(w.textContent).toContain('Cloudflare Workers');
    expect(tile(r, 'pdf')!.querySelector('[data-brand="anthropic"]')).not.toBeNull();
    expect(stateOf(tile(r, 'pdf')!)).toBe('Offered');
    expect(tile(r, 'pdf')!.getAttribute('data-check')).toBe('false');
    expect(stateOf(tile(r, 'danger')!)).toBe('Blocked');
    expect(stateOf(tile(r, 'old')!)).toBe('Rejected');
    // the whole grid is in one list, nothing is open until a tile is clicked
    expect(r.$('ul[aria-label="Skills in this project"]')!.querySelectorAll('[data-testid="skill-tile"]').length).toBe(5);
    expect(r.$('[data-testid="skill"]')).toBeNull();
    expect(r.container.querySelector('script')).toBe(null);
  });

  it('repository skills are offered: scan reports counts, the panel accepts and rejects by name', async () => {
    const accepted: unknown[][] = [];
    const rejected: unknown[][] = [];
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => [
          skill({}),
          skill({
            id: 'b',
            name: 'danger',
            status: 'blocked',
            blockReason: 'contains a secret',
            hasScripts: true,
          }),
        ],
        skillScan: async () => ({
          found: 2,
          accepted: 0,
          offered: 1,
          blocked: 1,
          removed: 0,
          errors: null,
        }),
        skillAccept: async (...a: unknown[]) => (accepted.push(a), skill({ status: 'accepted' })),
        skillReject: async (...a: unknown[]) => (rejected.push(a), skill({ status: 'rejected' })),
        skillRelease: async () => skill({}),
      }),
    );
    await waitFor(() => expect(r.$$('[data-testid="skill-tile"]').length).toBe(2), r);
    await r.act(() => byText(r.container, 'button', 'Scan the repository for skills').click());
    await waitFor(() => expect(r.$('[data-testid="skill-scan"]')!.textContent).toContain('1 offered, 0 accepted, 1 blocked'), r);
    expect(r.$('[data-testid="skill-scan"]')!.getAttribute('role')).toBe('status');
    const danger = await open(r, 'danger');
    expect(danger.getAttribute('data-status')).toBe('blocked');
    byText(danger, 'button', 'Release danger anyway');
    expect(exists(danger, 'button', 'Accept danger')).toBe(false);
    expect(danger.textContent).toContain('contains a secret');
    const go = await open(r, 'go-style');
    expect(go.getAttribute('data-status')).toBe('offered');
    expect(r.$$('[data-testid="skill"]').length).toBe(1); // one panel at a time
    expect(r.container.querySelector('script')).toBe(null);
    await r.act(() => byText(go, 'button', 'Accept go-style').click());
    await waitFor(() => expect(accepted).toEqual([['prj_1', 'go-style']]), r);
    await r.act(() => byText(go, 'button', 'Reject go-style').click());
    await waitFor(() => expect(rejected).toEqual([['prj_1', 'go-style']]), r);
  });

  it('an accepted skill is stopped after a confirmation (reject), a rejected one accepted again', async () => {
    const rejected: unknown[][] = [];
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => [skill({ status: 'accepted' }), skill({ id: 'c', name: 'old', status: 'rejected' })],
        skillReject: async (...a: unknown[]) => (rejected.push(a), skill({ status: 'rejected' })),
      }),
    );
    const go = await open(r, 'go-style');
    expect(exists(go, 'button', 'Accept go-style')).toBe(false);
    await r.act(() => byText(go, 'button', 'Stop using go-style').click());
    expect(rejected).toEqual([]); // not yet: the confirmation is open
    const confirm = go.querySelector('[data-testid="skill-stop-confirm"]')!;
    expect(confirm.textContent).toContain('Stop using go-style?');
    await r.act(() => byText(confirm, 'button', 'Keep').click());
    expect(go.querySelector('[data-testid="skill-stop-confirm"]')).toBeNull();
    expect(rejected).toEqual([]);
    await r.act(() => byText(go, 'button', 'Stop using go-style').click());
    await r.act(() => byText(go, 'button', 'Yes, stop using it').click());
    await waitFor(() => expect(rejected).toEqual([['prj_1', 'go-style']]), r);
    const old = await open(r, 'old');
    byText(old, 'button', 'Accept old');
  });

  it('the Library section lists the skills not in the project yet by registry, Add to project adds one accepted and says so', async () => {
    const added: unknown[][] = [];
    let skills: Skill[] = [skill({ status: 'accepted' })];
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => skills,
        skillLibraryList: async () => [
          lib({}),
          lib({ id: 'l-xlsx', name: 'xlsx', description: 'Spreadsheets' }),
          lib({ id: 'l-go', name: 'go-style', description: 'Also upstream' }), // already in the project: not offered again
          lib({ id: 'l-w', name: 'wrangler', registryId: 'cloudflare-skills', repo: 'cloudflare/skills', description: 'Workers', license: 'Apache-2.0' }),
          lib({ id: 'l-evil', name: 'evil', registryId: 'cloudflare-skills', repo: 'cloudflare/skills', status: 'blocked', blockReason: 'contains a secret' }),
        ],
        skillLibraryAdd: async (...a: unknown[]) => {
          added.push(a);
          const s = skill({ id: 'new', name: String(a[2]), origin: 'registry', registryId: String(a[1]), status: 'accepted' });
          skills = [...skills, s];
          return s;
        },
      }),
    );
    const grid = await waitFor(() => {
      const g = r.$('ul[aria-label="Skills in the Library"]');
      if (!g || g.querySelectorAll('[data-testid="library-skill-tile"]').length !== 4) throw new Error('not yet');
      return g;
    }, r);
    const tiles = Array.from(grid.querySelectorAll<HTMLElement>('[data-testid="library-skill-tile"]'));
    expect(tiles.map((x) => x.getAttribute('data-name'))).toEqual(['pdf', 'xlsx', 'wrangler', 'evil']);
    expect(tiles[0]!.textContent).toBe('pdfPDF tools · MITAvailable');
    expect(tiles[0]!.querySelector('[data-brand="anthropic"]')).not.toBeNull();
    expect(tiles[2]!.textContent).toBe('wranglerWorkers · Apache-2.0Available');
    expect(tiles[3]!.textContent).toBe('evilPDF tools · MITBlocked: contains a secret');
    expect(tiles[3]!.querySelector('small[data-tone="bad"]')).not.toBeNull();
    // the registry chooser: All, then one chip per registry with its count
    const chips = r.$('[role="group"][aria-label="Registry"]')!;
    expect(Array.from(chips.querySelectorAll('button')).map((b) => b.textContent)).toEqual(['All (4)', 'anthropics/skills (2)', 'cloudflare/skills (2)']);
    await r.act(() => byText(chips, 'button', 'cloudflare/skills (2)').click());
    await waitFor(() => expect(r.$$('[data-testid="library-skill-tile"]').length).toBe(2), r);
    expect(byText(chips, 'button', 'cloudflare/skills (2)').getAttribute('aria-pressed')).toBe('true');
    // a blocked skill opens but has no Add button
    await r.act(() => r.$('[data-testid="library-skill-tile"][data-name="evil"]')!.click());
    const evil = await one(r, '[data-testid="library-skill"][data-name="evil"]');
    expect(evil.textContent).toContain('A blocked skill cannot be added');
    expect(evil.querySelector('[data-testid="library-add"]')).toBeNull();
    // Add to project: (projectId, registryId, name), then the success status and the tile moves to the project with its check
    await r.act(() => r.$('[data-testid="library-skill-tile"][data-name="wrangler"]')!.click());
    const w = await one(r, '[data-testid="library-skill"][data-name="wrangler"]');
    expect(w.textContent).toContain('abcdef123456');
    await r.act(() => byText(w, 'button', 'Add to project').click());
    await waitFor(() => expect(added).toEqual([['prj_1', 'cloudflare-skills', 'wrangler']]), r);
    const note = await one(r, '[data-testid="skill-added"]');
    expect(note.getAttribute('role')).toBe('status');
    expect(note.textContent).toContain('wrangler was added to gocalc and is accepted ✓');
    await waitFor(() => expect(tile(r, 'wrangler')?.getAttribute('data-check')).toBe('true'), r);
    expect(stateOf(tile(r, 'wrangler')!)).toBe('Accepted ✓ · whole team');
    expect(r.$('[data-testid="library-skill-tile"][data-name="wrangler"]')).toBeNull();
    // the added skill's panel opens: it says accepted
    expect(r.$('[data-testid="skill"][data-skill="wrangler"]')!.querySelector('[data-testid="skill-status"]')!.textContent).toBe('accepted ✓');
  });

  it('Add all from a registry calls libraryAddAll and reports what happened with the reasons', async () => {
    const calls: unknown[][] = [];
    let lists = 0;
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => (lists++, []),
        skillLibraryList: async () => [lib({}), lib({ id: 'l-w', name: 'wrangler', registryId: 'cloudflare-skills', repo: 'cloudflare/skills' })],
        skillLibraryAddAll: async (...a: unknown[]) => (
          calls.push(a),
          {
            added: ['pdf', 'xlsx'],
            unchanged: ['docx'],
            blocked: [{ name: 'evil', reason: 'contains a secret' }],
            skipped: [{ name: 'bad', reason: 'no SKILL.md' }],
          }
        ),
      }),
    );
    const buttons = await waitFor(() => {
      const b = Array.from(r.$$('[data-testid="library-add-all"]')) as HTMLButtonElement[];
      if (b.length !== 2) throw new Error('not yet');
      return b;
    }, r);
    expect(buttons.map((b) => b.textContent?.trim())).toEqual(['Add all from anthropics/skills', 'Add all from cloudflare/skills']);
    const before = lists;
    await r.act(() => buttons[0]!.click());
    await waitFor(() => expect(calls).toEqual([['prj_1', 'anthropic-skills']]), r);
    const note = await one(r, '[data-testid="skill-added-all"]');
    expect(note.getAttribute('role')).toBe('status');
    expect(note.textContent).toContain('From anthropics/skills:');
    expect(note.textContent).toContain('2 added, 1 unchanged, 1 blocked, 1 skipped');
    expect(note.textContent).toContain('evil blocked: contains a secret');
    expect(note.textContent).toContain('bad skipped: no SKILL.md');
    await waitFor(() => expect(lists).toBeGreaterThan(before), r); // the project's skills are re-read
  });

  it('an empty Library says it is filling and offers Sync now; a full one in the project says everything is in', async () => {
    const checks: unknown[][] = [];
    let library: LibrarySkill[] = [];
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => [skill({ id: 'p', name: 'pdf', status: 'accepted', origin: 'registry', registryId: 'anthropic-skills' })],
        skillLibraryList: async () => library,
        skillRegistryCheck: async (...a: unknown[]) => ((checks.push(a), (library = [lib({})])), []),
      }),
    );
    const empty = await one(r, '[data-testid="library-empty"]');
    expect(empty.textContent).toContain('The Library is filling from your registries...');
    await r.act(() => byText(empty, 'button', 'Sync now').click());
    await waitFor(() => expect(checks).toEqual([[]]), r); // a sync of every registry
    await waitFor(() => expect(r.$('[data-testid="library-all-added"]')!.textContent).toContain('Everything in the Library is in this project.'), r);
    expect(r.$('ul[aria-label="Skills in the Library"]')).toBeNull();
  });

  it('Update from the Library appears only for a skill the registry status names as behind, and adds it again', async () => {
    const added: unknown[][] = [];
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => [
          skill({ id: 'p', name: 'pdf', status: 'accepted', origin: 'registry', registryId: 'anthropic-skills' }),
          skill({ id: 'x', name: 'xlsx', status: 'accepted', origin: 'registry', registryId: 'anthropic-skills' }),
        ],
        skillRegistryStatus: async () => [
          status({ projects: { prj_1: { updates: [{ name: 'pdf', description: 'PDF tools' }] }, prj_other: { updates: [{ name: 'xlsx', description: '' }] } } }),
        ],
        skillLibraryAdd: async (...a: unknown[]) => (added.push(a), skill({ id: 'p', name: 'pdf', status: 'accepted' })),
      }),
    );
    await waitFor(() => expect(tile(r, 'pdf')?.getAttribute('data-update')).toBe('true'), r);
    expect(stateOf(tile(r, 'pdf')!)).toBe('Update in the Library');
    expect(tile(r, 'xlsx')!.getAttribute('data-update')).toBe('false'); // another project's update is not ours
    expect(stateOf(tile(r, 'xlsx')!)).toBe('Accepted ✓ · whole team');
    const x = await open(r, 'xlsx');
    expect(exists(x, 'button', 'Update from the Library')).toBe(false);
    const p = await open(r, 'pdf');
    expect(p.querySelector('[data-testid="skill-update"]')!.textContent).toBe('update in the Library');
    await r.act(() => byText(p, 'button', 'Update from the Library').click());
    await waitFor(() => expect(added).toEqual([['prj_1', 'anthropic-skills', 'pdf']]), r);
  });

  it('the usage table sorts remove first then by tokens, badges each recommendation, and Stop using rejects the skill', async () => {
    const rejected: unknown[][] = [];
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => [],
        skillUsage: async () => [
          usage({ name: 'keep-me', loaded: 12, opened: 3, tasks: 5, tokensLoaded: 48000, tokensListed: 120, lastUsed: '2026-10-12T10:00:00Z', recommendation: 'keep', reason: 'read in 3 of 5 tasks' }),
          usage({ name: 'waste', listed: 40, tokensListed: 1600, recommendation: 'remove', reason: 'listed 40 times, never opened' }),
          usage({ name: 'wide', loaded: 9, tokensLoaded: 90000, recommendation: 'narrow', reason: 'loaded for every task, read in one' }),
          usage({ name: 'fresh' }),
        ],
        skillReject: async (...a: unknown[]) => (rejected.push(a), skill({ name: 'waste', status: 'rejected' })),
      }),
    );
    const rows = await waitFor(() => {
      const l = Array.from(r.$$('[data-testid="skill-usage-row"]')) as HTMLElement[];
      if (l.length !== 4) throw new Error('not yet');
      return l;
    }, r);
    expect(rows.map((x) => x.getAttribute('data-skill'))).toEqual(['waste', 'wide', 'keep-me', 'fresh']);
    expect(rows.map((x) => x.getAttribute('data-recommendation'))).toEqual(['remove', 'narrow', 'keep', '']);
    expect(rows.map((x) => x.querySelector('[data-testid="skill-recommendation"]')!.textContent)).toEqual(['remove', 'narrow', 'keep', 'no data yet']);
    expect(rows[2]!.querySelector('[data-testid="skill-usage-tokens"]')!.textContent).toBe('48,120');
    expect(rows[2]!.textContent).toContain('read in 3 of 5 tasks');
    expect(rows[3]!.textContent).toContain('never');
    expect(r.$('table[aria-label="Skill usage"]')!.querySelectorAll('th').length).toBe(8);
    expect(exists(rows[2]!, 'button', 'Stop using keep-me')).toBe(false);
    await r.act(() => byText(rows[0]!, 'button', 'Stop using waste').click());
    await waitFor(() => expect(rejected).toEqual([['prj_1', 'waste']]), r);
  });

  it('registry tiles show the sync state; the panel lists what the last sync brought, Sync now syncs that registry, the status re-reads on skill.library_synced', async () => {
    const checks: unknown[][] = [];
    let reads = 0;
    let statuses = [
      status({ added: [{ name: 'pptx', description: 'Slide decks' }], changed: [{ name: 'pdf', description: 'PDF tools' }] }),
      status({ registryId: 'off', repo: 'me/off', enabled: false, checkedAt: undefined }),
      status({ registryId: 'broken', repo: 'me/broken', lastError: 'GitHub rate limit' }),
    ];
    const s = servicesWith({
      skillList: async () => [],
      skillRegistries: async () => [anthropic, { ...anthropic, id: 'off', repo: 'me/off', enabled: false, curated: false }, { ...anthropic, id: 'broken', repo: 'me/broken', curated: false }],
      skillRegistryStatus: async () => (reads++, statuses),
      skillRegistryCheck: async (...a: unknown[]) => {
        checks.push(a);
        statuses = statuses.map((x) => (x.registryId === 'anthropic-skills' ? { ...x, added: [], changed: [], skills: 13 } : x));
        return statuses;
      },
    });
    const r = mount(<SkillsScreen />, s);
    const tiles = await waitFor(() => {
      const l = Array.from(r.$$('[data-testid="registry-tile"]')) as HTMLElement[];
      if (l.length !== 3) throw new Error('not yet');
      return l;
    }, r);
    expect(tiles[0]!.querySelector('[data-brand="anthropic"]')).not.toBeNull();
    expect(stateOf(tiles[0]!)).toBe(`12 skills in the Library (1 blocked) · synced ${new Date('2026-10-13T08:00:00Z').toLocaleString()}`);
    expect(tiles[0]!.querySelector('small[data-tone="ok"]')).not.toBeNull();
    expect(stateOf(tiles[1]!)).toBe('Disabled: not synced');
    expect(stateOf(tiles[2]!)).toBe('Last sync failed: GitHub rate limit');
    expect(tiles[2]!.querySelector('small[data-tone="bad"]')).not.toBeNull();
    expect(tiles[0]!.textContent).toContain('Curated · trust curated');
    expect(r.$('[data-testid="registry"]')).toBeNull();
    expect(r.$$('[data-testid="skill-candidate"]').length).toBe(0); // the candidates list is gone
    expect(exists(r.container, 'button', 'Import all (they will be offered)')).toBe(false);

    await r.act(() => tiles[0]!.click());
    const panel = await one(r, '[data-testid="registry"][data-registry="anthropic-skills"]');
    expect(exists(panel, 'button', 'Remove')).toBe(false); // curated registries stay
    expect(panel.querySelector('[data-testid="registry-status"]')!.textContent).toContain('12 skills in the Library (1 blocked)');
    expect(panel.querySelector('[data-testid="registry-status"]')!.textContent).toContain('def4567890ab');
    expect(panel.querySelector('[data-testid="registry-added"]')!.textContent).toContain('pptx');
    expect(panel.querySelector('[data-testid="registry-added"]')!.textContent).toContain('Slide decks');
    expect(panel.querySelector('[data-testid="registry-changed"]')!.textContent).toContain('pdf');
    const sync = byText<HTMLButtonElement>(panel, 'button', 'Sync now');
    expect(sync.getAttribute('data-testid')).toBe('registry-check-now');
    await r.act(() => sync.click());
    await waitFor(() => expect(checks).toEqual([['anthropic-skills']]), r);
    await waitFor(() => expect(panel.querySelector('[data-testid="registry-status"]')!.textContent).toContain('13 skills in the Library'), r);
    expect(panel.querySelector('[data-testid="registry-added"]')).toBeNull();
    expect(panel.textContent).toContain('The last sync found nothing new or changed.');

    await r.act(() => tiles[1]!.click());
    const off = await one(r, '[data-testid="registry"][data-registry="off"]');
    expect(byText<HTMLButtonElement>(off, 'button', 'Sync now').disabled).toBe(true); // a disabled registry is not synced
    byText(off, 'button', 'Enable');
    byText(off, 'button', 'Remove');
    await r.act(() => tiles[2]!.click());
    const broken = await one(r, '[data-testid="registry"][data-registry="broken"]');
    expect(broken.querySelector('[role="alert"]')!.textContent).toContain('Last sync failed: GitHub rate limit');

    const before = reads;
    s.emit({ type: 'skill.library_synced', data: { registry: 'anthropic-skills', commit: 'ghi', added: 1, changed: 0 } });
    await waitFor(() => expect(reads).toBeGreaterThan(before), r);
  });

  it('a registry is added only with the acknowledgement, and opens once added', async () => {
    const added: unknown[][] = [];
    let regs: SkillRegistry[] = [anthropic];
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => [],
        skillRegistries: async () => regs,
        skillRegistryAdd: async (...a: unknown[]) => {
          added.push(a);
          regs = [...regs, { id: 'mine', repo: 'me/skills', trust: 'user', enabled: true, curated: false }];
          return regs[1]!;
        },
      }),
    );
    await waitFor(() => expect(r.$$('[data-testid="registry-tile"]').length).toBe(1), r);
    await r.act(() => byText(r.container, 'button', 'Allow another repository').click());
    type(byLabel(r.container, 'Registry id'), 'mine');
    type(byLabel(r.container, 'GitHub repository (owner/name)'), 'me/skills');
    r.root.flush();
    const add = byText<HTMLButtonElement>(r.container, 'button', 'Add to the allowlist');
    expect(add.disabled).toBe(true);
    await r.act(() => byLabel(r.container, /I understand skills from this repository/).click());
    expect(add.disabled).toBe(false);
    await r.act(() => add.click());
    await waitFor(
      () =>
        expect(added).toEqual([
          [
            {
              id: 'mine',
              repo: 'me/skills',
              license: undefined,
              trust: 'user',
            },
            true,
          ],
        ]),
      r,
    );
    await waitFor(() => expect(r.$$('[data-testid="registry-tile"]').length).toBe(2), r);
    await waitFor(() => expect(r.$('[data-testid="registry"]')!.getAttribute('data-registry')).toBe('mine'), r);
  });

  it('lists deliveries with their coverage, and the open skill says when it was last delivered', async () => {
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => [skill({ status: 'accepted' })],
        skillCoverage: async () => ({ files: 2, tool: 1 }),
        skillDeliveries: async () => [
          {
            ts: 1_700_000_000_000,
            taskId: 't1',
            agentId: 'a1',
            skill: 'go-style',
            method: 'rule_file',
            fidelity: 'full',
          },
        ],
      }),
    );
    await waitFor(() => expect(r.$('[data-testid="skill-coverage"]')!.textContent).toBe('2 delivered as rule files, 1 through a tool.'), r);
    await waitFor(() => byText(r.container, 'a', 't1'), r);
    expect(byText(r.container, 'a', 't1').getAttribute('href')).toBe('/task/t1');
    expect(r.$('table[aria-label="Skill deliveries"]')!.textContent).toContain('rule_file, full');
    const go = await open(r, 'go-style');
    expect(go.querySelector('[data-testid="skill-deliveries"]')!.textContent).toContain('Delivered 1 time');
  });

  const member = (id: string, name: string, role: string) => ({ id, projectId: 'prj_1', name, role, adapter: 'claude', model: '', effort: '', providerId: '', status: 'STOPPED' }) as Agent;
  const team = [member('agt_a', 'Ada', 'backend'), member('agt_b', 'Bob', 'frontend'), member('agt_q', 'Quinn', 'qa')];
  function withTeam(client: Partial<Client>) {
    const s = servicesWith({ agentList: async () => team, roomList: async () => [], roomUnread: async () => [], needsYouList: async () => [], presenceList: async () => [], taskList: async () => [], ...client });
    s.project.start();
    return s;
  }
  const audienceOf = (panel: Element) => panel.querySelector<HTMLElement>('[data-testid="skill-audience-text"]')?.textContent ?? '';

  it('says who gets each skill: the whole team or its roles, agents given it on top, or only chosen agents', async () => {
    const r = mount(
      <SkillsScreen />,
      withTeam({
        skillList: async () => [
          skill({ status: 'accepted', roles: ['Backend'], agents: ['agt_q'] }),
          skill({ id: 'e', name: 'everywhere', status: 'accepted', roles: [] }),
          skill({ id: 'w', name: 'wrangler', status: 'accepted', audience: 'agents', agents: ['agt_b', 'agt_removed'] }),
          skill({ id: 'n', name: 'new-one', status: 'offered', roles: [] }),
        ],
      }),
    );
    const go = await open(r, 'go-style');
    await waitFor(() => expect(audienceOf(go)).toBe('Every Backend agent, and Quinn'), r);
    expect(audienceOf(await open(r, 'everywhere'))).toBe('The whole team');
    expect(audienceOf(await open(r, 'wrangler'))).toBe('Only Bob'); // a removed agent is not named
    expect(audienceOf(await open(r, 'new-one'))).toBe('The whole team, once accepted');
  });

  it('gives a team skill to one more agent, or narrows it to chosen agents starting from those who have it', async () => {
    const calls: unknown[][] = [];
    const r = mount(
      <SkillsScreen />,
      withTeam({
        skillList: async () => [skill({ status: 'accepted', roles: ['Backend'] })],
        skillAudience: async (...a: unknown[]) => (calls.push(a), skill({ status: 'accepted' })),
      }),
    );
    const go = await open(r, 'go-style');
    await waitFor(() => expect(audienceOf(go)).toBe('Every Backend agent'), r);
    await r.act(() => byText(go, 'button', 'Change who gets go-style').click());
    const ed = await waitFor(() => go.querySelector<HTMLElement>('[data-testid="skill-audience"]')!, r);
    // the whole team: Ada (backend) gets it with the team and cannot be unticked; Quinn is given it on top
    expect(byLabel<HTMLInputElement>(ed, /^Ada/).disabled).toBe(true);
    await r.act(() => byLabel<HTMLInputElement>(ed, /^Quinn/).click());
    await r.act(() => byText(ed, 'button', 'Save who gets go-style').click());
    await waitFor(() => expect(calls).toEqual([['prj_1', 'go-style', 'team', ['agt_q']]]), r);
    await waitFor(() => expect(go.querySelector('[data-testid="skill-audience"]')).toBe(null), r);

    // only chosen agents: starts from Ada, who has it now; Bob is added, Ada taken off
    await r.act(() => byText(go, 'button', 'Change who gets go-style').click());
    const ed2 = await waitFor(() => go.querySelector<HTMLElement>('[data-testid="skill-audience"]')!, r);
    await r.act(() => byLabel<HTMLInputElement>(ed2, /^Only the agents I choose/).click());
    expect(byLabel<HTMLInputElement>(ed2, /^Ada/).checked).toBe(true);
    expect(byLabel<HTMLInputElement>(ed2, /^Ada/).disabled).toBe(false);
    await r.act(() => byLabel<HTMLInputElement>(ed2, /^Bob/).click());
    await r.act(() => byLabel<HTMLInputElement>(ed2, /^Ada/).click());
    await r.act(() => byText(ed2, 'button', 'Save who gets go-style').click());
    await waitFor(() => expect(calls[1]).toEqual(['prj_1', 'go-style', 'agents', ['agt_b']]), r);
  });

  it('warns before saving a skill for chosen agents with nobody chosen, and shows the core refusal', async () => {
    const r = mount(
      <SkillsScreen />,
      withTeam({
        skillList: async () => [skill({ status: 'accepted', audience: 'agents', agents: ['agt_a'] })],
        skillAudience: async () => {
          throw new Error("skills: agent agt_x is not on this project's team");
        },
      }),
    );
    const go = await open(r, 'go-style');
    await waitFor(() => expect(audienceOf(go)).toBe('Only Ada'), r);
    await r.act(() => byText(go, 'button', 'Change who gets go-style').click());
    const ed = await waitFor(() => go.querySelector<HTMLElement>('[data-testid="skill-audience"]')!, r);
    await r.act(() => byLabel<HTMLInputElement>(ed, /^Ada/).click());
    expect(ed.textContent).toContain('No agent is chosen: nobody will get it.');
    await r.act(() => byText(ed, 'button', 'Save who gets go-style').click());
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('not on this project'), r);
    expect(go.querySelector('[data-testid="skill-audience"]')).toBeTruthy(); // still open to fix
  });
});
