// GitHub's Sign in box (F4): after gh auth login exits with 0, the line follows github.sign_in_finished's answer, and a sign-in
// that ended without signing gh in to github.com (another host chosen) offers Try again.

import { expect } from 'remix/assert';
import { afterEach, beforeEach, describe, it } from 'remix/test';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { setTerminalLoader, type TerminalFactory } from '../dock/widgets.ts';
import { GitHubSignIn, ghSignInLine, ghSignInRetry } from './github-sign-in.tsx';

/** TEST DOUBLE for xterm.js: takes what is written and draws nothing. */
const testTerminal: TerminalFactory = () => ({ cols: 80, rows: 24, write: () => {}, onData: () => {}, fit: () => {}, focus: () => {}, dispose: () => {} });

beforeEach(() => setTerminalLoader(async () => testTerminal));
afterEach(() => {
  setTerminalLoader(undefined);
  cleanup();
});

describe('the GitHub sign-in box', () => {
  it('says what the end of the sign-in means', () => {
    expect(ghSignInLine(undefined)).toBe('gh auth login is running: answer it here.');
    expect(ghSignInLine(0)).toBe('The sign-in finished. Checking gh again...');
    expect(ghSignInLine(0, true)).toBe('Signed in to GitHub.');
    expect(ghSignInLine(0, false)).toBe('gh finished, but it is still not signed in to github.com. Try again and choose GitHub.com.');
    expect(ghSignInLine(1)).toContain('ended with code 1');
    expect(ghSignInRetry(undefined)).toBe(false);
    expect(ghSignInRetry(0)).toBe(false);
    expect(ghSignInRetry(0, true)).toBe(false);
    expect(ghSignInRetry(0, false)).toBe(true);
    expect(ghSignInRetry(1)).toBe(true);
  });

  it('offers Try again when gh exits 0 but is still not signed in to github.com', async () => {
    const starts: number[] = [];
    let done = 0;
    const s = testServices({
      githubSignIn: async () => (starts.push(1), { id: `term-${starts.length}` }) as never,
      terminalRead: async () => ({ data: new Uint8Array(0), next: 0, dropped: 0, exited: true, exitCode: 0 }),
      terminalWrite: async () => {},
      terminalResize: async () => {},
      terminalClose: async () => {},
    });
    const r = mount(<GitHubSignIn onDone={() => void done++} />, s);
    await r.act(() => (r.$('[data-testid="gh-sign-in"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$('[data-testid="gh-sign-in-state"]')!.textContent).toBe('The sign-in finished. Checking gh again...'), r);
    expect(done).toBe(1);
    expect(r.$('[data-testid="gh-sign-in-again"]')).toBeNull();
    s.emit({ type: 'github.sign_in_finished', data: { exitCode: 0, signedIn: false } });
    await waitFor(() => expect(r.$('[data-testid="gh-sign-in-state"]')!.textContent).toContain('still not signed in to github.com'), r);
    await r.act(() => (r.$('[data-testid="gh-sign-in-again"]') as HTMLButtonElement).click());
    await waitFor(() => expect(starts.length).toBe(2), r);
  });
});
