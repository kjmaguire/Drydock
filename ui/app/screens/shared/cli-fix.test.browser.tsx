import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Connection } from '../../api/types.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { CliFix, distroOfKey, fixKind } from './cli-fix.tsx';

afterEach(cleanup);

// TEST DOUBLES of connection.list's answers
const base = { adapter: 'claude', installed: true, usable: false, platform: 'windows' } as Connection;
const signedOut = { ...base, status: 'unauthenticated', authenticated: false, signIn: 'claude auth login' } as Connection;
const notice = { ...base, status: 'ready', authenticated: true, gates: [{ id: 'terms', text: 'Read the terms of the CLI.', acked: false }] } as Connection;
const missing = { ...base, installed: false, status: 'not_installed', installHint: 'npm install -g @anthropic-ai/claude-code' } as Connection;

describe('the agent CLI fix (A9)', () => {
  it('names the first action by the state: Sign in, the install command, the notice; WSL keys name their distro', () => {
    expect([fixKind(signedOut), fixKind(notice), fixKind(missing), fixKind({ ...base, status: 'ready', usable: true } as Connection)]).toEqual(['sign-in', 'notice', 'install', 'ready']);
    expect([distroOfKey('claude@wsl:Ubuntu'), distroOfKey('claude'), distroOfKey(undefined)]).toEqual(['Ubuntu', '', '']);
  });

  it('a notice is ticked and accepted in place, then the CLI is ready', async () => {
    let conn = notice;
    const acks: string[] = [];
    const s = testServices({
      connectionList: async () => [conn],
      connectionAcknowledge: async (id) => {
        acks.push(id);
        conn = { ...notice, usable: true, gates: [{ id: 'terms', text: 'Read the terms of the CLI.', acked: true }] } as Connection;
      },
    });
    let fixed = 0;
    let other = 0;
    const r = mount(<CliFix adapter="claude" onOther={() => other++} onFixed={() => fixed++} />, s);
    await waitFor(() => expect(r.$('[data-testid="cli-fix"]')?.getAttribute('data-kind')).toBe('notice'), r);
    expect(r.$('[data-testid="cli-fix-gate"]')!.textContent).toContain('Read the terms of the CLI.');
    const accept = r.$('[data-testid="cli-fix-accept"]') as HTMLButtonElement;
    expect(accept.disabled).toBe(true); // never without the tick
    await r.act(() => (r.$('[data-testid="cli-fix-gate"] input[type="checkbox"]') as HTMLInputElement).click());
    await r.act(() => accept.click());
    await waitFor(() => expect(acks).toEqual(['terms']), r);
    await waitFor(() => expect(r.$('[data-testid="cli-fix"]')?.getAttribute('data-kind')).toBe('ready'), r);
    expect(fixed).toBe(1);
    // Use another CLI is last
    const buttons = Array.from(r.$$('[data-testid="cli-fix"] button'));
    expect(buttons.at(-1)!.textContent).toBe('Use another CLI');
    await r.act(() => byText(r.container, 'button', 'Use another CLI').click());
    expect(other).toBe(1);
  });

  it('a CLI that is not installed shows its install command with Copy and a link to its page', async () => {
    const s = testServices({ connectionList: async () => [missing] });
    const r = mount(<CliFix adapter="claude" />, s);
    await waitFor(() => expect(r.$('[data-testid="cli-fix-install"] code')?.textContent).toBe('npm install -g @anthropic-ai/claude-code'), r);
    expect(r.$('[data-testid="cli-fix-install"] button')).not.toBeNull();
    expect(r.$('[data-testid="cli-fix-page"]')!.textContent).toBe('How to install');
  });
});
