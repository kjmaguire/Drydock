import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { AgentConfigFinding, Project } from '../../api/types.ts';
import { expiredLicense, lic } from '../../test/fixtures.ts';
import { cleanup, drain, mount, testServices, waitFor } from '../../test/render.tsx';
import { AGENT_CONFIG_WHY } from '../shared/agent-config.ts';
import { AgentConfigPanel } from './project-agent-config.tsx';
import { ProjectSettingsScreen } from './project-settings-screen.tsx';

afterEach(cleanup);

const finding = (over: Partial<AgentConfigFinding> = {}): AgentConfigFinding => ({
  adapter: 'gemini',
  where: 'the repository',
  taskId: '',
  files: ['.gemini/settings.json', '.gemini/hooks/pre-tool.sh'],
  fingerprint: 'a'.repeat(64),
  trusted: false,
  ...over,
});

/**
 * TEST DOUBLE (labelled): project.agentConfig and project.trustAgentConfig as the core answers them. `state` is what the CLIs
 * would find now; trusting an adapter with a fingerprint marks every finding with that adapter and fingerprint trusted, a
 * fingerprint that is not the current one of any finding is refused.
 */
function guard(initial: AgentConfigFinding[]) {
  const g = {
    state: initial,
    reads: 0,
    trusts: [] as [string, string, string][],
    client: {} as Partial<Client>,
  };
  g.client = {
    projectAgentConfig: async () => (g.reads++, g.state.map((f) => ({ ...f }))),
    projectTrustAgentConfig: async (id: string, adapter: string, fingerprint: string) => {
      g.trusts.push([id, adapter, fingerprint]);
      if (!g.state.some((f) => f.adapter === adapter && f.fingerprint === fingerprint)) throw new Error('the configuration changed since you looked at it: read it again before you trust it');
      g.state = g.state.map((f) => (f.adapter === adapter && f.fingerprint === fingerprint ? { ...f, trusted: true } : f));
      return g.state.map((f) => ({ ...f }));
    },
  };
  return g;
}

function panel(g: ReturnType<typeof guard>, license = lic(), extra: Partial<Client> = {}) {
  const s = testServices({ projectList: async () => [{ id: 'p1', name: 'gocalc', hasRepo: true } as Project], licenseStatus: async () => license, appInfo: async () => ({}) as never, ...g.client, ...extra });
  s.workspace.start();
  return { r: mount(<AgentConfigPanel projectId="p1" />, s), s };
}

const group = (r: { $: (s: string) => Element | null }) => r.$('[data-testid="agent-config-panel"]') as HTMLElement | null;
const items = (r: { $$: (s: string) => NodeListOf<Element> }) => Array.from(r.$$('[data-testid="agent-config-finding"]')) as HTMLElement[];
const trustBtn = (item: HTMLElement) => need(item.querySelector<HTMLButtonElement>('[data-testid="agent-config-trust"]'));
const need = <T,>(x: T | null | undefined): T => {
  if (x === null || x === undefined) throw new Error('not there yet');
  return x;
};

describe('the Agent configuration group of Project settings (M3)', () => {
  it('explains in two sentences why this matters, and says plainly when nothing is found', async () => {
    const { r } = panel(guard([]));
    const g = await waitFor(() => need(group(r)), r);
    expect(g.querySelector('h2')!.textContent).toBe('Agent configuration');
    expect(g.getAttribute('aria-label')).toBe('Agent configuration');
    const why = g.querySelector('header p')!.textContent!;
    expect(why).toBe(AGENT_CONFIG_WHY);
    expect(why).toContain('run programs or send prompts elsewhere');
    expect(why).toContain('Drydock does not start the CLI where they are not trusted');
    await waitFor(() => need(g.querySelector('[data-testid="agent-config-none"]')), r);
    expect(g.querySelector('[data-testid="agent-config-none"]')!.textContent).toContain('Nothing found');
    expect(items(r)).toHaveLength(0);
  });

  it('lists each finding with its CLI, where it is and its files, a Trust button on each that is not trusted and "trusted" on the others', async () => {
    const g = guard([
      finding(),
      finding({ adapter: 'opencode', files: ['.opencode/plugins/guard.js'], fingerprint: 'b'.repeat(64), trusted: true }),
      finding({ where: 'the worktree of task 3, Fix login', taskId: 'tsk_3', files: ['.env'], fingerprint: 'c'.repeat(64) }),
    ]);
    const { r } = panel(g);
    await waitFor(() => expect(items(r)).toHaveLength(3), r);
    const [repo, opencode, worktree] = items(r);
    expect(repo!.getAttribute('data-adapter')).toBe('gemini');
    expect(repo!.textContent).toContain('the repository');
    expect(Array.from(repo!.querySelectorAll('li code')).map((c) => c.textContent)).toEqual(['.gemini/settings.json', '.gemini/hooks/pre-tool.sh']);
    expect(repo!.getAttribute('data-trusted')).toBe('false');
    expect(trustBtn(repo!).getAttribute('aria-label')).toBe('Trust gemini configuration: the repository');
    expect(trustBtn(repo!).textContent).toBe('Trust');
    expect(opencode!.getAttribute('data-trusted')).toBe('true');
    expect(opencode!.querySelector('[data-testid="agent-config-trusted"]')!.textContent).toBe('trusted');
    expect(opencode!.querySelector('button')).toBeNull();
    expect(worktree!.textContent).toContain('the worktree of task 3, Fix login');
    expect(r.$('[data-testid="agent-config-summary"]')!.textContent).toBe('2 of 3 places need your decision before its CLI starts.');
    expect(g.trusts).toEqual([]);
  });

  it('says everything is trusted when it is', async () => {
    const { r } = panel(guard([finding({ trusted: true })]));
    await waitFor(() => expect(r.$('[data-testid="agent-config-summary"]')!.textContent).toBe('Everything found is trusted.'), r);
    expect(r.$('[data-testid="agent-config-trust"]')).toBeNull();
  });

  it('shows a file name as the text it is, with the characters that are not drawn written out', async () => {
    const name = '<img src=x onerror="window.__agentcfgxss=1">.json';
    const sneaky = 'settings‮gnp.json';
    const { r } = panel(guard([finding({ files: [name, sneaky] })]));
    await waitFor(() => expect(items(r)).toHaveLength(1), r);
    const codes = Array.from(items(r)[0]!.querySelectorAll('li code')).map((c) => c.textContent);
    expect(codes).toEqual([name, 'settings\\u{202e}gnp.json']);
    expect(items(r)[0]!.querySelector('img, script')).toBeNull();
    expect((window as unknown as Record<string, unknown>).__agentcfgxss).toBeUndefined();
    expect(group(r)!.textContent).toContain('has characters that are not drawn');
  });

  it('trusts exactly what was shown: Trust sends the CLI and the fingerprint on screen, nothing else trusts, and the refreshed list shows it trusted', async () => {
    const g = guard([finding(), finding({ where: 'the worktree of task 3, Fix login', taskId: 'tsk_3' })]);
    const { r, s } = panel(g);
    await waitFor(() => expect(items(r)).toHaveLength(2), r);
    // showing, re-reading and focusing the window trust nothing
    await r.act(() => window.dispatchEvent(new Event('focus')));
    s.emit({ type: 'task.updated', projectId: 'p1' });
    await waitFor(() => expect(g.reads).toBeGreaterThanOrEqual(3), r);
    await drain();
    expect(g.trusts).toEqual([]);
    const readsBefore = g.reads;
    await r.act(() => trustBtn(items(r)[0]!).click());
    await waitFor(() => expect(g.trusts).toEqual([['p1', 'gemini', 'a'.repeat(64)]]), r);
    // both places hold the same files (the same fingerprint): trusting one trusts both, as the core answers
    await waitFor(() => expect(items(r).map((i) => i.getAttribute('data-trusted'))).toEqual(['true', 'true']), r);
    expect(r.$('[data-testid="agent-config-summary"]')!.textContent).toBe('Everything found is trusted.');
    // the list the core answered the trust with is the one shown: it is not read a second time
    await drain();
    expect(g.reads).toBe(readsBefore);
  });

  it('a state that changed since it was shown is refused with the core\'s words, the newer files are shown, and the next click trusts those', async () => {
    const g = guard([finding()]);
    const { r } = panel(g);
    await waitFor(() => expect(items(r)).toHaveLength(1), r);
    g.state = [finding({ files: ['.gemini/settings.json', '.gemini/hooks/pre-tool.sh', '.gemini/commands/deploy.toml'], fingerprint: 'd'.repeat(64) })];
    await r.act(() => trustBtn(items(r)[0]!).click());
    await waitFor(() => expect(group(r)!.querySelector('[role="alert"]')!.textContent).toContain('the configuration changed since you looked at it'), r);
    expect(group(r)!.querySelector('[role="alert"] strong')!.textContent).toBe('Not trusted: ');
    await waitFor(() => expect(Array.from(items(r)[0]!.querySelectorAll('li code')).map((c) => c.textContent)).toContain('.gemini/commands/deploy.toml'), r);
    expect(g.trusts).toEqual([['p1', 'gemini', 'a'.repeat(64)]]);
    await r.act(() => trustBtn(items(r)[0]!).click());
    await waitFor(() => expect(g.trusts[1]).toEqual(['p1', 'gemini', 'd'.repeat(64)]), r);
    await waitFor(() => expect(items(r)[0]!.getAttribute('data-trusted')).toBe('true'), r);
  });

  it('reads again when a refused start is published, and when a worktree appears', async () => {
    const g = guard([]);
    const { r, s } = panel(g);
    await waitFor(() => expect(g.reads).toBe(1), r);
    g.state = [finding()];
    s.emit({ type: 'agent.failed', projectId: 'p1' });
    await waitFor(() => expect(items(r)).toHaveLength(1), r);
    g.state = [finding(), finding({ where: 'the worktree of task 4, Add tests', taskId: 'tsk_4' })];
    s.emit({ type: 'task.updated', projectId: 'p1' });
    await waitFor(() => expect(items(r)).toHaveLength(2), r);
    // another project's events do not matter
    const before = g.reads;
    s.emit({ type: 'task.updated', projectId: 'other' });
    await drain();
    expect(g.reads).toBe(before);
  });

  it('read-only: the findings are shown, trusting is turned off', async () => {
    const { r } = panel(guard([finding()]), expiredLicense);
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).not.toBeNull(), r);
    expect(trustBtn(items(r)[0]!).disabled).toBe(true);
  });

  it('says why it cannot show the findings', async () => {
    const { r } = panel(guard([]), lic(), {
      projectAgentConfig: async () => {
        throw new Error('project p1 has no repository');
      },
    });
    await waitFor(() => expect(group(r)!.querySelector('[role="alert"]')!.textContent).toContain('Could not read the agent configuration: project p1 has no repository'), r);
  });
});

describe('Project settings carries the group', () => {
  it('places Agent configuration after the sandbox and before deleting the project, for a project with a repository only', async () => {
    const project: Project = { id: 'p1', name: 'gocalc', repoPath: '/r', defaultBranch: 'main', mergeStrategy: 'merge', mode: 'local_merge', created: 0 };
    const make = (p: Project) => {
      const s = testServices({
        projectList: async () => [p],
        licenseStatus: async () => lic(),
        appInfo: async () => ({}) as never,
        routeLevelGet: async () => ({ workspace: 'balanced', project: '', effective: 'balanced', source: 'workspace', levels: [] }),
        checksSettingsGet: async () => ({ secrets: true, deps: false, depsOffline: false }),
        projectdevGet: async () => ({ maxParallel: 2, autoSchedule: false, respond: true, e2e: { enabled: false }, preview: { enabled: false } }),
        projectAgentConfig: async () => [],
      });
      s.workspace.start();
      return mount(<ProjectSettingsScreen />, s);
    };
    const r = make(project);
    const g = await waitFor(() => need(group(r)), r);
    const sandbox = r.$('[data-testid="sandbox-panel"]')!;
    const del = r.$('[data-testid="delete-project"]')!;
    expect(sandbox.compareDocumentPosition(g) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(g.compareDocumentPosition(del) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    cleanup();
    const bare = make({ ...project, repoPath: '', hasRepo: false, defaultBranch: '' });
    await waitFor(() => need(bare.$('[data-testid="no-repo"]')), bare);
    expect(group(bare)).toBeNull();
  });
});
