// The clone picker's sign-in (F4), its "Already a project" (B7) and the check of the folder a clone would make (B7).

import { expect } from 'remix/assert';
import type { Handle } from 'remix/component';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { GitHubRepo } from '../../api/types.ts';
import { byLabel, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { RepoPicker } from './repo-picker.tsx';
import { repo } from './wizard-fixtures.ts';

afterEach(cleanup);

interface State {
  selected: GitHubRepo | undefined;
  dir: string;
  onUseFolder?: (p: string) => void;
  onOpenProject?: (id: string) => void;
}

function Holder(handle: Handle<{ state: State }>) {
  return () => {
    const { state } = handle.props;
    return (
      <RepoPicker
        selected={state.selected}
        cloneDir={state.dir}
        onSelect={(x) => ((state.selected = x), void handle.update())}
        onCloneDir={(d) => ((state.dir = d), void handle.update())}
        onUseFolder={state.onUseFolder}
        onOpenProject={state.onOpenProject}
      />
    );
  };
}

function setup(client: Partial<Client>, extra: Partial<State> = {}) {
  const state: State = { selected: undefined, dir: '', ...extra };
  const s = testServices(client);
  const r = mount(<Holder state={state} />, s);
  return { r, s, state };
}

const signedIn = { githubStatus: async () => ({ installed: true, loggedIn: true, login: 'kyle' }) };

describe('the clone picker', () => {
  it('runs gh auth login from Sign in when gh is not signed in (F4)', async () => {
    const signIns: number[] = [];
    const { r } = setup({
      githubStatus: async () => ({ installed: true, loggedIn: false, login: '', error: 'gh is not signed in: run `gh auth login`' }),
      githubSignIn: async () => (signIns.push(1), { id: 'term-gh' }) as never,
      terminalRead: () => new Promise(() => {}),
      terminalClose: async () => {},
    });
    const btn = await waitFor(() => {
      const b = r.$('[data-testid="gh-sign-in"]');
      if (!b) throw new Error('no Sign in yet');
      return b as HTMLButtonElement;
    }, r);
    expect(btn.textContent).toBe('Sign in');
    await r.act(() => btn.click());
    await waitFor(() => expect(r.$('[data-testid="gh-sign-in-terminal"]')).toBeTruthy(), r);
    expect(signIns.length).toBe(1);
    expect(r.$('[data-testid="gh-sign-in-state"]')!.textContent).toBe('gh auth login is running: answer it here.');
  });

  it('offers how to install gh when it is missing, and no Sign in (F4)', async () => {
    const { r } = setup({ githubStatus: async () => ({ installed: false, loggedIn: false, login: '' }) });
    await waitFor(() => expect(r.$('[data-testid="gh-install-link"]')!.getAttribute('href')).toBe('https://cli.github.com'), r);
    expect(r.$('[data-testid="gh-sign-in"]')).toBeNull();
  });

  it('reads gh again when a sign-in finishes, and lists the repositories (F4)', async () => {
    let now = false;
    const { r, s } = setup({
      githubStatus: async () => (now ? { installed: true, loggedIn: true, login: 'kyle' } : { installed: true, loggedIn: false, login: '' }),
      githubOwners: async () => ['kyle'],
      githubRepos: async () => [repo('kyle/pay')],
      githubCloneDir: async () => 'D:\\src',
    });
    await waitFor(() => expect(r.$('[data-testid="github-unavailable"]')).toBeTruthy(), r);
    now = true;
    s.emit({ type: 'github.sign_in_finished', data: { exitCode: 0, signedIn: true } });
    await waitFor(() => expect(r.$$('[data-testid="gh-repo"]').length).toBe(1), r);
  });

  it('marks a repository that is already a project and opens that project (B7)', async () => {
    const opened: string[] = [];
    const { r } = setup(
      {
        ...signedIn,
        githubOwners: async () => ['acme'],
        githubRepos: async () => [repo('acme/gocalc', { projectId: 'p1', project: 'calc' }), repo('acme/other')],
        githubCloneDir: async () => 'D:\\src',
      },
      { onOpenProject: (id) => opened.push(id) },
    );
    await waitFor(() => expect(r.$$('[data-testid="gh-repo"]').length).toBe(2), r);
    const marks = r.$$('[data-testid="gh-repo-project"]');
    expect(marks.length).toBe(1);
    expect(marks[0]!.textContent).toBe('Already a project: calc');
    await r.act(() => (r.$('[data-repo="acme/gocalc"] input') as HTMLInputElement).click());
    const open = await waitFor(() => {
      const b = r.$('[data-testid="repo-open-project"]');
      if (!b) throw new Error('no Open yet');
      return b as HTMLButtonElement;
    }, r);
    expect(r.$('[data-testid="repo-is-project"]')!.textContent).toContain('acme/gocalc is already a project: calc.');
    await r.act(() => open.click());
    expect(opened).toEqual(['p1']);
  });

  it('checks the folder a clone would make: a repository there offers Use this folder, a folder with files says the clone would be refused (B7)', async () => {
    const used: string[] = [];
    const checked: string[] = [];
    const answers: Record<string, unknown> = {
      'D:\\src\\pay': { path: 'D:\\src\\pay', state: 'repository', root: 'D:\\src\\pay', branch: 'main', entries: 3, canInit: false, message: '' },
      'E:\\src\\pay': { path: 'E:\\src\\pay', state: 'folder', entries: 2, canInit: true, message: '' },
    };
    const { r } = setup(
      {
        ...signedIn,
        githubOwners: async () => ['kyle'],
        githubRepos: async () => [repo('kyle/pay')],
        githubCloneDir: async () => 'D:\\src',
        repoCheck: async (p: string) => (checked.push(p), answers[p] ?? { path: p, state: 'missing', entries: 0, canInit: true, message: '' }) as never,
      },
      { onUseFolder: (p) => used.push(p) },
    );
    await waitFor(() => expect(r.$$('[data-testid="gh-repo"]').length).toBe(1), r);
    await r.act(() => (r.$('[data-testid="gh-repo"] input') as HTMLInputElement).click());
    const use = await waitFor(() => {
      const b = r.$('[data-testid="clone-use-folder"]');
      if (!b) throw new Error('no Use this folder yet');
      return b as HTMLButtonElement;
    }, r);
    expect(r.$('[data-testid="clone-target-repo"]')!.textContent).toContain('Already cloned here (branch main).');
    await r.act(() => use.click());
    expect(used).toEqual(['D:\\src\\pay']);
    type(byLabel<HTMLInputElement>(r.container, 'Clone into'), 'E:\\src');
    await waitFor(() => expect(r.$('[data-testid="clone-target-busy"]')!.textContent).toContain('the clone would be refused'), r);
    expect(r.$('[data-testid="clone-target-repo"]')).toBeNull();
    expect(checked).toContain('E:\\src\\pay');
  });
});
