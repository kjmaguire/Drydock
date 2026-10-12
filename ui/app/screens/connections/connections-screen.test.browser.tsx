import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Connection, ConnectionReport } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, renderPlain, testServices, waitFor } from '../../test/render.tsx';
import { ConnectionsScreen, ReportView } from './connections-screen.tsx';

afterEach(cleanup);

const conn = (over: Partial<Connection> = {}): Connection => ({
  adapter: 'claude',
  installed: true,
  status: 'ready',
  usable: false,
  platform: 'windows',
  version: '2.1.0',
  gates: [{ id: 'claude-terms', text: 'Drydock uses your own unmodified claude login.', acked: false }],
  prereqs: [{ tool: 'node', min: '22', found: false, ok: false, guide: 'Install Node 22 from nodejs.org.' }],
  warnings: ['version not tested'],
  ...over,
});

const report = (over: Partial<ConnectionReport> = {}): ConnectionReport => ({
  adapter: 'claude',
  platform: 'windows',
  transport: 'headless',
  form: 'short',
  steps: [
    { id: 'T1', name: 'start', status: 'pass' },
    { id: 'T2', name: 'prompt', status: 'fail', reason: 'timeout', detail: 'no output in 30s' },
    { id: 'T3', name: 'interrupt', status: 'skipped', reason: 'T2 failed' },
  ],
  capabilities: {} as never,
  matrix: { streaming: { value: true, provenance: 'observed' }, mcp: { value: false, provenance: 'unknown' } },
  degraded: ['Interrupt could not be tested.'],
  summary: 'The CLI started but did not answer.',
  tokensUsed: 0,
  ok: false,
  elapsed: 31_500_000_000,
  ...over,
});

function show(client: Partial<Client>) {
  const r = mount(<ConnectionsScreen />, testServices(client));
  return r;
}
type R = ReturnType<typeof show>;
const card = (r: R) =>
  waitFor(() => {
    const c = r.$('[data-testid="connection"]');
    if (!c) throw new Error('not yet');
    return c as HTMLElement;
  }, r);

describe('Connections page', () => {
  it('shows the gate text and only the human can accept it: checkbox first, then Accept calls connection.acknowledge', async () => {
    const acks: string[] = [];
    let acked = false;
    const r = show({
      connectionList: async () => [conn(acked ? { gates: [{ id: 'claude-terms', text: 'Drydock uses your own unmodified claude login.', acked: true }] } : {})],
      connectionAcknowledge: async (id: string) => {
        acks.push(id);
        acked = true;
      },
    });
    const c = await card(r);
    expect(c.textContent).toContain('Drydock uses your own unmodified claude login.');
    const gate = c.querySelector('[data-testid="gate"]')!;
    expect(gate.getAttribute('data-acked')).toBe('false');
    const accept = byText<HTMLButtonElement>(gate, 'button', 'Accept');
    expect(accept.disabled).toBe(true);
    await r.act(() => accept.click());
    expect(acks).toEqual([]);
    await r.act(() => byLabel(gate, 'I have read this and accept it').click());
    expect(accept.disabled).toBe(false);
    await r.act(() => accept.click());
    await waitFor(() => expect(acks).toEqual(['claude-terms']), r);
    await waitFor(() => expect(r.$('[data-testid="gate"]')!.getAttribute('data-acked')).toBe('true'), r);
  });

  it('shows status, usable, warnings, prerequisites with guidance, and an accepted gate without a button', async () => {
    const r = show({
      connectionList: async () => [conn({ usable: true, gates: [{ id: 'g', text: 'notice', acked: true }], authenticated: null, installHint: 'winget install X' })],
    });
    const c = await card(r);
    expect(c.getAttribute('data-usable')).toBe('true');
    const text = c.textContent ?? '';
    expect(text).toContain('usable');
    expect(text).not.toContain('not usable yet');
    expect(text).toContain('accepted');
    expect(() => byText(c, 'button', 'Accept')).toThrow();
    expect(text).toContain('version not tested');
    expect(text).toContain('Install Node 22 from nodejs.org.');
    expect(text).toContain('node (needs 22 or newer): not found');
    expect(c.querySelector('dl')!.textContent).toContain('Signed innot reported');
    expect(text).toContain('never runs this for you');
    expect(text).toContain('winget install X');
  });

  it('Test connection warns about the real CLI and cost first, runs only after confirmation, and shows a plain report', async () => {
    const tests: string[] = [];
    const r = show({ connectionList: async () => [conn()], connectionTest: async (a: string, prompt?: boolean) => (tests.push(`${a}:${prompt}`), report()) });
    const c = await card(r);
    await r.act(() => byText(c, 'button', 'Test connection...').click());
    expect(tests).toEqual([]);
    expect(c.textContent).toContain('starts the real claude CLI');
    // the tiny prompt is offered with what it costs, unticked: a test spends nothing unless the person asks
    const box = byLabel<HTMLInputElement>(c, /^Also send one tiny prompt/);
    expect(box.checked).toBe(false);
    expect(c.textContent).toContain('a fraction of a cent');
    await r.act(() => byText(c, 'button', 'Run the test').click());
    await waitFor(() => {
      if (!c.querySelector('[data-testid="connection-report"]')) throw new Error('not yet');
    }, r);
    expect(tests).toEqual(['claude:false']);
    await r.act(() => byText(c, 'button', 'Test connection...').click());
    await r.act(() => byText(c, 'button', 'Cancel').click());
    expect(tests).toEqual(['claude:false']);
    await r.act(() => byText(c, 'button', 'Test connection...').click());
    await r.act(() => byLabel<HTMLInputElement>(c, /^Also send one tiny prompt/).click());
    await r.act(() => byText(c, 'button', 'Run the test').click());
    const rep = await waitFor(() => {
      const x = c.querySelector('[data-testid="connection-report"]');
      if (!x || tests.length < 2) throw new Error('not yet');
      return x;
    }, r);
    expect(tests).toEqual(['claude:false', 'claude:true']);
    expect(rep.textContent).toContain('The CLI started but did not answer.');
    expect(rep.textContent).toContain('did not pass');
  });

  it('report: steps, degraded list, matrix provenance, tokens not reported', () => {
    const r = renderPlain(<ReportView r={report()} />);
    const text = r.container.textContent ?? '';
    expect(text).toContain('T2 prompt: fail (timeout)');
    expect(text).toContain('no output in 30s');
    expect(text).toContain('Interrupt could not be tested.');
    expect(text).toContain('31.5 s');
    expect(text).toContain('Tokens used by the test: not reported');
    expect(text).toContain('observed');
    expect(text).toContain('unknown');
  });

  it('shows the core refusal when acknowledging fails', async () => {
    const r = show({
      connectionList: async () => [conn()],
      connectionAcknowledge: async () => {
        throw new Error('unknown gate');
      },
    });
    const c = await card(r);
    const gate = c.querySelector('[data-testid="gate"]')!;
    await r.act(() => byLabel(gate, 'I have read this and accept it').click());
    await r.act(() => byText(gate, 'button', 'Accept').click());
    await waitFor(() => expect(c.querySelector('[role="alert"]')!.textContent).toContain('unknown gate'), r);
  });

  it('a Claude Code install inside a WSL distro is its own card: where it runs, its own sign-in, the distro, and its own Test', async () => {
    const tests: string[] = [];
    const inWSL = conn({
      key: 'claude@wsl:Ubuntu',
      runMode: 'wsl',
      distro: 'Ubuntu',
      platform: 'linux (WSL 2)',
      path: '/home/k/.local/bin/claude',
      authenticated: false,
      status: 'unauthenticated',
      gates: [],
      prereqs: [],
      warnings: [],
      wsl: { distro: 'Ubuntu', wsl2: true, default: true, interop: true, netMode: 'nat', mountRoot: '/mnt/', home: '/home/k', setsid: true, notes: ['A repository inside the distro is fast for the agent.'] },
    });
    const r = show({
      connectionList: async () => [conn({ key: 'claude', runMode: 'native', gates: [], prereqs: [], warnings: [] }), inWSL],
      connectionTest: async (a: string, prompt?: boolean, distro?: string) => (tests.push(`${a}:${prompt}:${distro ?? ''}`), report()),
    });
    const c = await waitFor(() => {
      const x = r.$('[data-key="claude@wsl:Ubuntu"]');
      if (!x) throw new Error('not yet');
      return x as HTMLElement;
    }, r);
    expect(r.$$('[data-testid="connection"]').length).toBe(2);
    expect(c.getAttribute('data-run-mode')).toBe('wsl');
    expect(c.querySelector('h2')!.textContent).toBe('claude in WSL (Ubuntu)');
    const text = c.textContent ?? '';
    expect(text).toContain('inside the WSL 2 distro Ubuntu (the default distro)');
    expect(text).toContain('its own sign-in, separate from Windows');
    expect(text).toContain('wsl -d Ubuntu');
    const facts = c.querySelector('[data-testid="wsl-facts"]')!;
    expect(facts.textContent).toContain('nat: the distro cannot reach Windows localhost directly');
    expect(facts.textContent).toContain('/mnt/');
    expect(facts.textContent).toContain('A repository inside the distro is fast for the agent.');
    // the Windows card says where it runs too
    expect(r.$('[data-key="claude"]')!.textContent).toContain('on Windows');
    await r.act(() => byText(c, 'button', 'Test connection...').click());
    expect(c.textContent).toContain('inside the WSL distro Ubuntu');
    await r.act(() => byText(c, 'button', 'Run the test').click());
    await waitFor(() => expect(tests).toEqual(['claude:false:Ubuntu']), r);
  });

  it('says plainly when no CLI was found', async () => {
    const r = show({ connectionList: async () => [] });
    await waitFor(() => expect(r.container.textContent).toContain('No agent CLIs found'), r);
  });
});
