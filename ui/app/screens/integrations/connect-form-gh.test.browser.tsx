// The GitHub connect form on the gh sign-in (F4): while gh is not signed in the notice offers Sign in (gh auth login here),
// not a pointer to another page; without gh it links to how to install it.

import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { IntegrationKindInfo } from '../../api/extTypes.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { ConnectForm } from './service-detail.tsx';

afterEach(cleanup);

const info = {
  kind: 'github',
  label: 'GitHub',
  blurb: '',
  items: '',
  api: '',
  apiVersion: '',
  sdk: '',
  rateLimit: '',
  docs: null,
  auth: [{ id: 'gh', label: 'Use the GitHub CLI sign-in', available: true }],
  settings: null,
  ops: null,
} as IntegrationKindInfo;

describe('the GitHub connect form on the gh sign-in', () => {
  it('offers Sign in when gh is not signed in', async () => {
    const signIns: number[] = [];
    const s = testServices({
      githubStatus: async () => ({ installed: true, loggedIn: false, login: '', error: 'gh is not signed in: run `gh auth login`' }),
      githubSignIn: async () => (signIns.push(1), { id: 'term-gh' }) as never,
      terminalRead: () => new Promise(() => {}),
      terminalResize: async () => {},
      terminalClose: async () => {},
    });
    const r = mount(<ConnectForm kind="github" info={info} onDone={() => {}} />, s);
    const note = await waitFor(() => {
      const n = r.$('[data-testid="gh-not-signed-in"]');
      if (!n) throw new Error('no notice yet');
      return n as HTMLElement;
    }, r);
    expect(note.textContent).not.toContain('The Agents page');
    expect(note.querySelector('[data-testid="gh-install-link"]')).toBeNull();
    await r.act(() => (note.querySelector('[data-testid="gh-sign-in"]') as HTMLButtonElement).click());
    await waitFor(() => expect(signIns.length).toBe(1), r);
  });

  it('links to how to install gh when it is missing, with no Sign in', async () => {
    const s = testServices({ githubStatus: async () => ({ installed: false, loggedIn: false, login: '', error: 'the GitHub CLI (gh) is not installed' }) });
    const r = mount(<ConnectForm kind="github" info={info} onDone={() => {}} />, s);
    const note = await waitFor(() => {
      const n = r.$('[data-testid="gh-not-signed-in"]');
      if (!n) throw new Error('no notice yet');
      return n as HTMLElement;
    }, r);
    expect(note.querySelector('[data-testid="gh-install-link"]')!.getAttribute('href')).toBe('https://cli.github.com');
    expect(note.querySelector('[data-testid="gh-sign-in"]')).toBeNull();
  });
});
