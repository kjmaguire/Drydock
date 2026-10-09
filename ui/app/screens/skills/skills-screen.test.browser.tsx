import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Skill, SkillRegistry } from '../../api/extTypes.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { SkillsScreen } from './skills-screen.tsx';

afterEach(cleanup);

const skill = (over: Partial<Skill>): Skill => ({ id: 's', projectId: 'prj_1', name: 'go-style', description: 'Go style', globs: ['**/*.go'], roles: ['Backend'], origin: 'repository', contentHash: 'h', status: 'offered', created: '', updated: '', body: 'Keep functions small. <script>window.__xss=1</script>', ...over });

function servicesWith(client: Partial<Client>) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as never],
    skillRegistries: async () => [],
    skillDeliveries: async () => [],
    skillCoverage: async () => ({ files: 0, tool: 0 }),
    ...client,
  });
  s.workspace.start();
  return s;
}

const exists = (root: ParentNode, sel: string, text: string) => Array.from(root.querySelectorAll(sel)).some((e) => e.textContent?.trim() === text);

describe('SkillsScreen', () => {
  it('repository skills are offered: scan reports counts, accept and reject name the skill', async () => {
    const accepted: unknown[][] = [];
    const rejected: unknown[][] = [];
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => [skill({}), skill({ id: 'b', name: 'danger', status: 'blocked', blockReason: 'contains a secret', hasScripts: true })],
        skillScan: async () => ({ found: 2, accepted: 0, offered: 1, blocked: 1, removed: 0, errors: null }),
        skillAccept: async (...a) => (accepted.push(a), skill({ status: 'accepted' })),
        skillReject: async (...a) => (rejected.push(a), skill({ status: 'rejected' })),
        skillRelease: async () => skill({}),
      }),
    );
    await waitFor(() => expect(r.$$('[data-testid="skill"]').length).toBe(2), r);
    const go = r.$('[data-testid="skill"][data-skill="go-style"]')!;
    expect(go.getAttribute('data-status')).toBe('offered');
    byText(r.container, 'button', 'Release danger anyway');
    expect(exists(r.container, 'button', 'Accept danger')).toBe(false);
    expect(r.container.textContent).toContain('contains a secret');
    expect(r.container.querySelector('script')).toBe(null);
    await r.act(() => byText(r.container, 'button', 'Scan the repository for skills').click());
    await waitFor(() => expect(r.$('[data-testid="skill-scan"]')!.textContent).toContain('1 offered, 0 accepted, 1 blocked'), r);
    expect(r.$('[data-testid="skill-scan"]')!.getAttribute('role')).toBe('status');
    await r.act(() => byText(r.container, 'button', 'Accept go-style').click());
    await waitFor(() => expect(accepted).toEqual([['prj_1', 'go-style']]), r);
    await r.act(() => byText(r.container, 'button', 'Reject go-style').click());
    await waitFor(() => expect(rejected).toEqual([['prj_1', 'go-style']]), r);
  });

  it('accepted skills can be withdrawn and rejected ones accepted again', async () => {
    const r = mount(<SkillsScreen />, servicesWith({ skillList: async () => [skill({ status: 'accepted' }), skill({ id: 'c', name: 'old', status: 'rejected' })] }));
    await waitFor(() => byText(r.container, 'button', 'Withdraw (reject) go-style'), r);
    byText(r.container, 'button', 'Accept old');
    expect(exists(r.container, 'button', 'Accept go-style')).toBe(false);
  });

  it('a registry is added only with the acknowledgement, and its skills are imported to be offered', async () => {
    const added: unknown[][] = [];
    const imported: unknown[][] = [];
    let regs: SkillRegistry[] = [{ id: 'anthropic', repo: 'anthropics/skills', trust: 'curated', enabled: true, curated: true }];
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => [],
        skillRegistries: async () => regs,
        skillCandidates: async () => ({ commit: 'abc', skills: [{ name: 'pdf', dir: 'pdf', description: 'PDF tools', valid: true }, { name: 'bad', dir: 'bad', valid: false, problem: 'no SKILL.md' }] }),
        skillImport: async (...a) => (imported.push(a), skill({ name: 'pdf' })),
        skillRegistryAdd: async (...a) => {
          added.push(a);
          regs = [...regs, { id: 'mine', repo: 'me/skills', trust: 'user', enabled: true, curated: false }];
          return regs[1]!;
        },
      }),
    );
    await waitFor(() => expect(r.$$('[data-testid="registry"]').length).toBe(1), r);
    expect(exists(r.$('[data-testid="registry"]')!, 'button', 'Remove')).toBe(false); // curated registries stay
    await r.act(() => byText(r.container, 'button', 'List skills to import').click());
    await waitFor(() => byText(r.container, 'button', 'Import pdf (it will be offered)'), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Import bad (it will be offered)').disabled).toBe(true);
    expect(r.container.textContent).toContain('no SKILL.md');
    await r.act(() => byText(r.container, 'button', 'Import pdf (it will be offered)').click());
    await waitFor(() => expect(imported).toEqual([['prj_1', 'anthropic', 'pdf']]), r);

    await r.act(() => byText(r.container, 'button', 'Allow another repository').click());
    type(byLabel(r.container, 'Registry id'), 'mine');
    type(byLabel(r.container, 'GitHub repository (owner/name)'), 'me/skills');
    r.root.flush();
    const add = byText<HTMLButtonElement>(r.container, 'button', 'Add to the allowlist');
    expect(add.disabled).toBe(true);
    await r.act(() => byLabel(r.container, /I understand skills from this repository/).click());
    expect(add.disabled).toBe(false);
    await r.act(() => add.click());
    await waitFor(() => expect(added).toEqual([[{ id: 'mine', repo: 'me/skills', license: undefined, trust: 'user' }, true]]), r);
    await waitFor(() => expect(r.$$('[data-testid="registry"]').length).toBe(2), r);
  });

  it('lists deliveries with their coverage', async () => {
    const r = mount(
      <SkillsScreen />,
      servicesWith({
        skillList: async () => [],
        skillCoverage: async () => ({ files: 2, tool: 1 }),
        skillDeliveries: async () => [{ ts: 1_700_000_000_000, taskId: 't1', agentId: 'a1', skill: 'go-style', method: 'rule_file', fidelity: 'full' }],
      }),
    );
    await waitFor(() => expect(r.$('[data-testid="skill-coverage"]')!.textContent).toBe('2 delivered as rule files, 1 through a tool.'), r);
    await waitFor(() => byText(r.container, 'a', 't1'), r);
    expect(byText(r.container, 'a', 't1').getAttribute('href')).toBe('/task/t1');
    expect(r.$('table[aria-label="Skill deliveries"]')!.textContent).toContain('rule_file, full');
  });
});
