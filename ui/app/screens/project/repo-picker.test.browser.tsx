import { expect } from 'remix/assert';
import type { Handle } from 'remix/component';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { GitHubRepo } from '../../api/types.ts';
import { byLabel, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { RepoPicker } from './repo-picker.tsx';
import { repo } from './wizard-fixtures.ts';

afterEach(cleanup);

/** The picker inside a tiny holder that keeps the selection and folder like the wizard does. */
function setup(client: Partial<Client>) {
  const picked: GitHubRepo[] = [];
  const state = { selected: undefined as GitHubRepo | undefined, dir: '' };
  const s = testServices(client);
  const r = mount(<Holder state={state} picked={picked} />, s);
  return { r, picked, state };
}

function Holder(handle: Handle<{ state: { selected: GitHubRepo | undefined; dir: string }; picked: GitHubRepo[] }>) {
  return () => {
    const { state, picked } = handle.props;
    return (
      <RepoPicker
        selected={state.selected}
        cloneDir={state.dir}
        onSelect={(x) => (picked.push(x), (state.selected = x), void handle.update())}
        onCloneDir={(d) => ((state.dir = d), void handle.update())}
        unavailableHint="Choose Local folder or Later instead."
      />
    );
  };
}

const signedIn = { githubStatus: async () => ({ installed: true, loggedIn: true, login: 'kyle' }) };

describe('the GitHub repository picker', () => {
  it('explains, in the core words, when gh is not installed', async () => {
    const { r } = setup({ githubStatus: async () => ({ installed: false, loggedIn: false, login: '', error: 'the GitHub CLI (gh) is not installed: install it from https://cli.github.com and run `gh auth login`' }) });
    const note = await waitFor(() => { const n = r.$('[data-testid="github-unavailable"]'); if (!n) throw new Error('not yet'); return n; }, r);
    expect(note.textContent).toContain('GitHub is not available.');
    expect(note.textContent).toContain('install it from https://cli.github.com');
    expect(note.textContent).toContain('stores no GitHub token');
    expect(note.textContent).toContain('Choose Local folder or Later instead.');
  });

  it('says when gh is installed but not signed in', async () => {
    const { r } = setup({ githubStatus: async () => ({ installed: true, loggedIn: false, login: '', error: 'gh is not signed in: run `gh auth login`' }) });
    const note = await waitFor(() => { const n = r.$('[data-testid="github-unavailable"]'); if (!n) throw new Error('not yet'); return n; }, r);
    expect(note.textContent).toContain('GitHub is not signed in.');
    expect(note.textContent).toContain('run `gh auth login`');
  });

  it('says GitHub did not answer (not "not signed in") when it timed out', async () => {
    const { r } = setup({ githubStatus: async () => ({ installed: true, loggedIn: false, unreachable: true, login: '', error: 'GitHub did not answer within 20 seconds (the network or GitHub may be down); try again later' }) });
    const note = await waitFor(() => { const n = r.$('[data-testid="github-unavailable"]'); if (!n) throw new Error('not yet'); return n; }, r);
    expect(note.textContent).toContain('GitHub did not answer.');
    expect(note.textContent).not.toContain('not signed in');
  });

  it('lists the repositories of the first owner, with private and archived marks, and picks one; the clone folder defaults from the core', async () => {
    const calls: [string, string][] = [];
    const { r, picked } = setup({
      ...signedIn,
      githubOwners: async () => ['kyle', 'acme'],
      githubRepos: async (owner = '', q = '') => (calls.push([owner, q]), [repo(`${owner}/pay`, { private: true }), repo(`${owner}/old`, { archived: true, description: '' })]),
      githubCloneDir: async () => 'C:\\Users\\kyle\\source\\repos',
    });
    await waitFor(() => expect(r.$$('[data-testid="gh-repo"]').length).toBe(2), r);
    expect(calls).toEqual([['kyle', '']]);
    expect(r.$('[data-testid="repo-picker"]')!.textContent).toContain('Signed in to GitHub as kyle');
    const [pay, old] = Array.from(r.$$('[data-testid="gh-repo"]'));
    expect(pay!.textContent).toContain('private');
    expect(pay!.textContent).toContain('pay description');
    expect(old!.textContent).toContain('archived');
    await waitFor(() => expect(byLabel<HTMLInputElement>(r.container, 'Clone into').value).toBe('C:\\Users\\kyle\\source\\repos'), r);
    await r.act(() => byLabel<HTMLInputElement>(r.container, 'kyle/pay').click());
    expect(picked.map((x) => x.fullName)).toEqual(['kyle/pay']);
    await waitFor(() => expect(r.$('[data-testid="clone-target"]')!.textContent).toBe('C:\\Users\\kyle\\source\\repos\\pay'), r);
  });

  it('searches after a pause and switches owner', async () => {
    const calls: [string, string][] = [];
    const { r } = setup({
      ...signedIn,
      githubOwners: async () => ['kyle', 'acme'],
      githubRepos: async (owner = '', q = '') => (calls.push([owner, q]), q === 'zzz' ? [] : [repo(`${owner}/pay`)]),
      githubCloneDir: async () => '/home/kyle/src',
    });
    await waitFor(() => expect(r.$$('[data-testid="gh-repo"]').length).toBe(1), r);
    type(byLabel(r.container, 'Search repositories'), 'pa');
    type(byLabel(r.container, 'Search repositories'), 'pay');
    await waitFor(() => expect(calls).toEqual([['kyle', ''], ['kyle', 'pay']]), r, 3000);
    type(byLabel<HTMLSelectElement>(r.container, 'Owner'), 'acme');
    await waitFor(() => expect(calls.at(-1)).toEqual(['acme', 'pay']), r);
    type(byLabel(r.container, 'Search repositories'), 'zzz');
    await waitFor(() => expect(r.container.textContent).toContain('Nothing matches "zzz" for acme.'), r, 3000);
  });
});
