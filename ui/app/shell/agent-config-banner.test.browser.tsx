import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../api/client.ts';
import type { AgentConfigFinding, Project } from '../api/types.ts';
import { expiredLicense, lic } from '../test/fixtures.ts';
import { cleanup, drain, mount, testServices, waitFor } from '../test/render.tsx';
import { AgentConfigBanner, BANNER_FINDINGS } from './agent-config-banner.tsx';
import { Shell } from './shell.tsx';

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
 * TEST DOUBLE (labelled): project.agentConfig and project.trustAgentConfig as the core answers them (see the settings group's
 * test): a fingerprint that is no finding's current one is refused, a matching one trusts every finding with that adapter and
 * fingerprint.
 */
function guard(initial: AgentConfigFinding[]) {
  const g = { state: initial, reads: 0, trusts: [] as [string, string, string][], client: {} as Partial<Client> };
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

function svc(client: Partial<Client>, license = lic()) {
  const s = testServices({ projectList: async () => [{ id: 'p1', name: 'gocalc', hasRepo: true } as never], licenseStatus: async () => license, appInfo: async () => ({}) as never, ...client });
  s.workspace.start();
  return s;
}

const banner = (r: { $: (s: string) => Element | null }) => r.$('[data-testid="agent-config-banner"]') as HTMLElement | null;
const findings = (r: { $$: (s: string) => NodeListOf<Element> }) => Array.from(r.$$('[data-testid="agent-config-banner"] [data-testid="agent-config-finding"]')) as HTMLElement[];
const need = <T,>(x: T | null | undefined): T => {
  if (x === null || x === undefined) throw new Error('not there yet');
  return x;
};
const trustBtn = (item: HTMLElement) => need(item.querySelector<HTMLButtonElement>('[data-testid="agent-config-trust"]'));

describe('the agent configuration banner (M3)', () => {
  it('shows nothing while nothing is found, and nothing while everything found is trusted', async () => {
    const none = guard([]);
    const r = mount(<AgentConfigBanner projectId="p1" />, svc(none.client));
    await waitFor(() => expect(none.reads).toBe(1), r);
    await drain();
    r.root.flush();
    expect(banner(r)).toBeNull();
    cleanup();
    const done = guard([finding({ trusted: true })]);
    const r2 = mount(<AgentConfigBanner projectId="p1" />, svc(done.client));
    await waitFor(() => expect(done.reads).toBe(1), r2);
    await drain();
    r2.root.flush();
    expect(banner(r2)).toBeNull();
  });

  it('names the CLI, the place and the files, says why Drydock does not start the CLI there, and only a click on Trust trusts what was shown', async () => {
    const g = guard([finding(), finding({ adapter: 'opencode', files: ['.opencode/plugins/guard.js'], fingerprint: 'b'.repeat(64), trusted: true })]);
    const s = svc(g.client);
    const r = mount(<AgentConfigBanner projectId="p1" />, s);
    const b = await waitFor(() => need(banner(r)), r);
    expect(b.tagName).toBe('SECTION');
    expect(b.getAttribute('aria-label')).toBe('Agent CLI configuration needs review');
    expect(b.textContent).toContain('Review the configuration the repository brought for agent CLIs.');
    expect(b.textContent).toContain('1 place has configuration that an agent CLI would act on and nobody trusted (gemini)');
    expect(b.textContent).toContain('These files can run programs or send prompts elsewhere when the CLI starts, so Drydock does not start it there until you trust them.');
    // only what needs a decision is listed
    expect(findings(r)).toHaveLength(1);
    expect(findings(r)[0]!.textContent).toContain('the repository');
    expect(Array.from(findings(r)[0]!.querySelectorAll('li code')).map((c) => c.textContent)).toEqual(['.gemini/settings.json', '.gemini/hooks/pre-tool.sh']);
    // showing it, re-reading it and focusing the window trust nothing
    await r.act(() => window.dispatchEvent(new Event('focus')));
    s.emit({ type: 'agent.failed', projectId: 'p1' });
    await waitFor(() => expect(g.reads).toBeGreaterThanOrEqual(3), r);
    await drain();
    expect(g.trusts).toEqual([]);
    await r.act(() => trustBtn(findings(r)[0]!).click());
    await waitFor(() => expect(g.trusts).toEqual([['p1', 'gemini', 'a'.repeat(64)]]), r);
    await waitFor(() => expect(banner(r)).toBeNull(), r);
  });

  it('lists the first three that need a decision and points to Project settings for the rest', async () => {
    const many = Array.from({ length: BANNER_FINDINGS + 2 }, (_, i) => finding({ where: `the worktree of task ${i + 1}, Task ${i + 1}`, taskId: `tsk_${i + 1}`, fingerprint: String(i).repeat(64).slice(0, 64) }));
    const r = mount(<AgentConfigBanner projectId="p1" />, svc(guard(many).client));
    await waitFor(() => need(banner(r)), r);
    expect(findings(r)).toHaveLength(3);
    expect(banner(r)!.textContent).toContain('5 places have configuration that an agent CLI would act on');
    expect(banner(r)!.textContent).toContain('2 more: see Project settings, Agent configuration.');
    expect(banner(r)!.querySelector('a')!.getAttribute('href')).toBe('/project');
  });

  it('a state that changed since it was shown is refused with the core\'s words and the newer files are shown to review again', async () => {
    const g = guard([finding()]);
    const r = mount(<AgentConfigBanner projectId="p1" />, svc(g.client));
    await waitFor(() => need(banner(r)), r);
    g.state = [finding({ files: ['.gemini/settings.json', '.gemini/commands/deploy.toml'], fingerprint: 'd'.repeat(64) })];
    await r.act(() => trustBtn(findings(r)[0]!).click());
    await waitFor(() => expect(banner(r)!.querySelector('[role="alert"]')!.textContent).toContain('the configuration changed since you looked at it'), r);
    await waitFor(() => expect(Array.from(findings(r)[0]!.querySelectorAll('li code')).map((c) => c.textContent)).toContain('.gemini/commands/deploy.toml'), r);
    expect(banner(r)!.textContent).toContain('the list above is the newer state');
    await r.act(() => trustBtn(findings(r)[0]!).click());
    await waitFor(() => expect(g.trusts).toEqual([['p1', 'gemini', 'a'.repeat(64)], ['p1', 'gemini', 'd'.repeat(64)]]), r);
    await waitFor(() => expect(banner(r)).toBeNull(), r);
  });

  it('read-only: the change is shown, trusting is turned off', async () => {
    const r = mount(<AgentConfigBanner projectId="p1" />, svc(guard([finding()]).client, expiredLicense));
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).not.toBeNull(), r);
    expect(trustBtn(findings(r)[0]!).disabled).toBe(true);
  });

  it('a read that fails shows no banner', async () => {
    let reads = 0;
    const r = mount(
      <AgentConfigBanner projectId="p1" />,
      svc({
        projectAgentConfig: async () => {
          reads++;
          throw new Error('project p1 has no repository');
        },
      }),
    );
    await waitFor(() => expect(reads).toBe(1), r);
    await drain();
    r.root.flush();
    expect(banner(r)).toBeNull();
  });
});

describe('the agent configuration banner in the shell', () => {
  const project = (over: Partial<Project>): Project => ({ id: 'p1', name: 'gocalc', repoPath: '/r', hasRepo: true, defaultBranch: 'main', mergeStrategy: 'merge', mode: 'local_merge', created: 0, ...over });
  const shell = (p: Project, g: ReturnType<typeof guard>) => {
    const s = testServices({ projectList: async () => [p], licenseStatus: async () => lic(), appInfo: async () => ({}) as never, ...g.client });
    s.workspace.start();
    s.project.start();
    return mount(
      <Shell url={new URL('http://localhost/tasks')}>
        <p data-testid="route-content">a page</p>
      </Shell>,
      s,
    );
  };

  it('sits above the page of a project whose repository brought configuration nobody trusted', async () => {
    const r = shell(project({}), guard([finding()]));
    await waitFor(() => need(banner(r)), r);
    const main = r.$('main#main')!;
    expect(main.contains(banner(r))).toBe(false);
    expect(banner(r)!.compareDocumentPosition(main) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('sits beside the repository banner without a key the two share (the runtime warned "Duplicate keys")', async () => {
    const warned: string[] = [];
    const warn = console.warn;
    console.warn = (...a: unknown[]) => void warned.push(a.map(String).join(' '));
    try {
      const r = shell(project({}), guard([finding()]));
      await waitFor(() => need(banner(r)), r);
      await drain();
    } finally {
      console.warn = warn;
    }
    expect(warned.filter((w) => w.includes('Duplicate keys'))).toEqual([]);
  });

  it('is not asked for a project without a repository', async () => {
    const g = guard([finding()]);
    const r = shell(project({ hasRepo: false, repoPath: '' }), g);
    await waitFor(() => expect(r.$('[data-testid="route-content"]')).not.toBeNull(), r);
    await drain();
    expect(g.reads).toBe(0);
    expect(banner(r)).toBeNull();
  });
});
