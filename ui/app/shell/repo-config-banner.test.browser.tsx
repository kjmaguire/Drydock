import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../api/client.ts';
import type { GitConfigStatus, Project } from '../api/types.ts';
import { expiredLicense, lic } from '../test/fixtures.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { RepoConfigBanner } from './repo-config-banner.tsx';
import { Shell } from './shell.tsx';

afterEach(cleanup);

const trusted: GitConfigStatus = { trusted: true, changed: [], fingerprint: 'fp-0' };
const changed: GitConfigStatus = { trusted: false, changed: ['filter.evil.clean', 'hooks/post-commit'], fingerprint: 'fp-1' };

/**
 * TEST DOUBLE (labelled): project.gitConfig and project.trustGitConfig as the core answers them. `state` is the repository's
 * current configuration; trusting with its fingerprint trusts it, a stale fingerprint is refused.
 */
function guard(initial: GitConfigStatus) {
  const g = {
    state: initial,
    reads: 0,
    trusts: [] as [string, string][],
    client: {} as Partial<Client>,
  };
  g.client = {
    projectGitConfig: async () => (g.reads++, g.state),
    projectTrustGitConfig: async (id: string, fingerprint: string) => {
      g.trusts.push([id, fingerprint]);
      if (fingerprint !== g.state.fingerprint) throw new Error('git: the configuration changed again since it was shown');
      g.state = { trusted: true, changed: [], fingerprint };
      return g.state;
    },
  };
  return g;
}

function svc(client: Partial<Client>, license = lic()) {
  const s = testServices({
    projectList: async () => [{ id: 'p1', name: 'gocalc', hasRepo: true } as never],
    licenseStatus: async () => license,
    appInfo: async () => ({}) as never,
    ...client,
  });
  s.workspace.start();
  return s;
}

const banner = (r: { $: (s: string) => Element | null }) => r.$('[data-testid="repo-config-banner"]');
const items = (r: { $$: (s: string) => NodeListOf<Element> }) => Array.from(r.$$('[data-testid="repo-config-items"] li')).map((li) => li.getAttribute('data-item'));
const settle = () => new Promise((res) => setTimeout(res, 30));

describe('the repository configuration banner', () => {
  it('shows nothing while the configuration is the trusted one', async () => {
    const g = guard(trusted);
    const r = mount(<RepoConfigBanner projectId="p1" />, svc(g.client));
    await waitFor(() => expect(g.reads).toBe(1), r);
    await settle();
    r.root.flush();
    expect(banner(r)).toBeNull();
  });

  it('names the changed items and explains them; only a click on Trust this configuration trusts, with the fingerprint shown', async () => {
    const g = guard(changed);
    const s = svc(g.client);
    const r = mount(<RepoConfigBanner projectId="p1" />, s);
    await waitFor(() => expect(banner(r)).not.toBeNull(), r);
    const b = banner(r)!;
    expect(b.tagName).toBe('SECTION');
    expect(b.getAttribute('aria-label')).toBe('Repository git configuration changed');
    expect(b.textContent).toContain("The repository's git configuration changed (filter.evil.clean, hooks/post-commit).");
    expect(b.textContent).toContain('Drydock does not run git there until you review it: a hook, filter or command planted there would run in Drydock\'s own git calls.');
    expect(b.textContent).toContain('Trust it only if you made or expect this change.');
    expect(items(r)).toEqual(['filter.evil.clean', 'hooks/post-commit']);
    const btn = byText<HTMLButtonElement>(r.container, 'button', 'Trust this configuration');
    expect(btn.type).toBe('button');
    expect(btn.disabled).toBe(false);
    // showing it, re-reading it, focusing the window: none of that trusts anything
    await r.act(() => window.dispatchEvent(new Event('focus')));
    s.emit({ type: 'approval.requested', projectId: 'p1' });
    await settle();
    expect(g.trusts).toEqual([]);
    await r.act(() => btn.click());
    await waitFor(() => expect(g.trusts).toEqual([['p1', 'fp-1']]), r);
    await waitFor(() => expect(banner(r)).toBeNull(), r);
  });

  it('re-reads when the window gets focus and when the configuration is trusted elsewhere (a run approval)', async () => {
    const g = guard(trusted);
    const s = svc(g.client);
    const r = mount(<RepoConfigBanner projectId="p1" />, s);
    await waitFor(() => expect(g.reads).toBe(1), r);
    g.state = changed; // changed in a terminal: no event
    await r.act(() => window.dispatchEvent(new Event('focus')));
    await waitFor(() => expect(banner(r)).not.toBeNull(), r);
    g.state = { ...changed, trusted: true, changed: [] }; // approved in a run
    s.emit({ type: 'security.repo_config_trusted', projectId: 'p1' });
    await waitFor(() => expect(banner(r)).toBeNull(), r);
    expect(g.trusts).toEqual([]);
  });

  it('a configuration that changed again since it was shown is not trusted: the newer items are shown to review again', async () => {
    const g = guard(changed);
    const r = mount(<RepoConfigBanner projectId="p1" />, svc(g.client));
    await waitFor(() => expect(banner(r)).not.toBeNull(), r);
    g.state = { trusted: false, changed: ['core.sshCommand', 'filter.evil.clean', 'hooks/post-commit'], fingerprint: 'fp-2' };
    await r.act(() => byText(r.container, 'button', 'Trust this configuration').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('changed again since it was shown'), r);
    await waitFor(() => expect(items(r)).toEqual(['core.sshCommand', 'filter.evil.clean', 'hooks/post-commit']), r);
    expect(g.trusts).toEqual([['p1', 'fp-1']]);
    expect(banner(r)!.textContent).toContain('review them again');
    await r.act(() => byText(r.container, 'button', 'Trust this configuration').click());
    await waitFor(() => expect(g.trusts).toEqual([['p1', 'fp-1'], ['p1', 'fp-2']]), r);
    await waitFor(() => expect(banner(r)).toBeNull(), r);
  });

  it('read-only: the change is shown, trusting is turned off', async () => {
    const g = guard(changed);
    const r = mount(<RepoConfigBanner projectId="p1" />, svc(g.client, expiredLicense));
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).not.toBeNull(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Trust this configuration').disabled).toBe(true);
  });

  it('a read that fails shows no banner', async () => {
    const r = mount(
      <RepoConfigBanner projectId="p1" />,
      svc({
        projectGitConfig: async () => {
          throw new Error('project p1 has no repository');
        },
      }),
    );
    await settle();
    r.root.flush();
    expect(banner(r)).toBeNull();
  });
});

describe('the banner in the shell', () => {
  const project = (over: Partial<Project>): Project => ({ id: 'p1', name: 'gocalc', repoPath: '/r', hasRepo: true, defaultBranch: 'main', mergeStrategy: 'merge', mode: 'local_merge', created: 0, ...over });
  const shell = (p: Project, g: ReturnType<typeof guard>) => {
    const s = testServices({ projectList: async () => [p], licenseStatus: async () => lic(), appInfo: async () => ({}) as never, ...g.client });
    s.workspace.start();
    s.project.start();
    return mount(
      <Shell url={new URL('http://localhost/settings')}>
        <p data-testid="route-content">a page</p>
      </Shell>,
      s,
    );
  };

  it('sits above the page of a project whose repository configuration changed', async () => {
    const r = shell(project({}), guard(changed));
    await waitFor(() => expect(banner(r)).not.toBeNull(), r);
    // before the page, outside it: every screen of the project shows it
    const main = r.$('main#main')!;
    expect(main.contains(banner(r))).toBe(false);
    expect(banner(r)!.compareDocumentPosition(main) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('is not asked for a project without a repository', async () => {
    const g = guard(changed);
    const r = shell(project({ hasRepo: false, repoPath: '' }), g);
    await waitFor(() => expect(r.$('[data-testid="route-content"]')).not.toBeNull(), r);
    await settle();
    expect(g.reads).toBe(0);
    expect(banner(r)).toBeNull();
  });
});
