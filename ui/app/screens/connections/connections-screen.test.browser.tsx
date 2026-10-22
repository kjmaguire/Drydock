import { expect } from 'remix/assert';
import { afterEach, beforeEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Connection, ConnectionReport } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, renderPlain, testServices, waitFor } from '../../test/render.tsx';
import { setTerminalLoader, type TerminalFactory } from '../dock/widgets.ts';
import { ConnectionsScreen, ReportView } from './connections-screen.tsx';

interface TestTerminal {
  output: string;
  type(s: string): void;
  disposed: boolean;
}
const terminals: TestTerminal[] = [];
/** TEST DOUBLE for xterm.js: keeps what was written as text and types what the test types. */
const testTerminal: TerminalFactory = () => {
  const listeners: ((s: string) => void)[] = [];
  const dec = new TextDecoder();
  const rec: TestTerminal = { output: '', type: (s) => listeners.forEach((l) => l(s)), disposed: false };
  terminals.push(rec);
  return {
    cols: 80,
    rows: 24,
    write: (d) => void (rec.output += typeof d === 'string' ? d : dec.decode(d)),
    onData: (fn) => void listeners.push(fn),
    fit: () => {},
    focus: () => {},
    dispose: () => void (rec.disposed = true),
  };
};

beforeEach(() => {
  terminals.length = 0;
  setTerminalLoader(async () => testTerminal);
});
afterEach(() => {
  setTerminalLoader(undefined);
  cleanup();
});

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

/** The card's "Signed in" value. */
const signedInRow = (card: Element): string | undefined =>
  Array.from(card.querySelectorAll('dt'))
    .find((d) => d.textContent === 'Signed in')
    ?.nextElementSibling?.textContent ?? undefined;

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
  // the MCP gateway's state (the panel below the tiles) is a TEST DOUBLE too: Claude verified, nothing else reported
  const gateway: Partial<Client> = {
    mcpStatus: async () => ({ running: true, address: '127.0.0.1:1', adapters: [{ adapter: 'claude', attached: true, canTry: false, trying: false }] }),
  };
  const r = mount(<ConnectionsScreen />, testServices({ ...gateway, ...client }));
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
    expect(r.container.querySelector('h3:nth-of-type(2)')!.textContent).toBe('What is limited');
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
      signIn: 'claude auth login',
      gates: [],
      prereqs: [],
      warnings: [],
      wsl: { distro: 'Ubuntu', wsl2: true, default: true, interop: true, netMode: 'nat', mountRoot: '/mnt/', home: '/home/k', setsid: true, notes: ['A repository inside the distro is fast for the agent.'] },
    });
    const r = show({
      connectionList: async () => [conn({ key: 'claude', runMode: 'native', gates: [], prereqs: [], warnings: [] }), inWSL],
      connectionTest: async (a: string, prompt?: boolean, distro?: string) => (tests.push(`${a}:${prompt}:${distro ?? ''}`), report()),
    });
    // a tile of its own, named by its distro; its card opens below the grid when it is chosen
    const tile = await waitFor(() => {
      const x = r.$('[data-testid="connection-tile"][data-key="claude@wsl:Ubuntu"]');
      if (!x) throw new Error('not yet');
      return x as HTMLButtonElement;
    }, r);
    expect(r.$$('[data-testid="connection-tile"]').length).toBe(2);
    expect(tile.getAttribute('data-run-mode')).toBe('wsl');
    // "<product> WSL", with the distro on the line below the name
    expect(tile.textContent).toBe('Claude Code WSL2.1.0 in the WSL distro UbuntuNot signed in');
    await r.act(() => tile.click());
    const c = await waitFor(() => {
      const x = r.$('[data-testid="connection"][data-key="claude@wsl:Ubuntu"]');
      if (!x) throw new Error('not yet');
      return x as HTMLElement;
    }, r);
    expect(r.$$('[data-testid="connection"]').length).toBe(1);
    expect(c.getAttribute('data-run-mode')).toBe('wsl');
    expect(c.querySelector('h2')!.textContent).toBe('Claude Code WSL');
    // the CLI's logo and the distro's
    expect(c.querySelector('[data-brand="claude"]')).not.toBeNull();
    expect(c.querySelector('[data-brand="ubuntu"]')!.getAttribute('aria-label')).toBe('Ubuntu');
    const text = c.textContent ?? '';
    expect(text).toContain('inside the WSL 2 distro Ubuntu (the default distro)');
    expect(text).toContain("this install's sign-in is separate from the Windows one");
    expect(byText<HTMLButtonElement>(c, 'button', 'Sign in inside Ubuntu').disabled).toBe(false);
    const facts = c.querySelector('[data-testid="wsl-facts"]')!;
    expect(facts.textContent).toContain('nat: the distro cannot reach Windows localhost directly');
    expect(facts.textContent).toContain('/mnt/');
    expect(facts.textContent).toContain('A repository inside the distro is fast for the agent.');
    await r.act(() => byText(c, 'button', 'Test connection...').click());
    expect(c.textContent).toContain('inside the WSL distro Ubuntu');
    await r.act(() => byText(c, 'button', 'Run the test').click());
    await waitFor(() => expect(tests).toEqual(['claude:false:Ubuntu']), r);
    // the Windows card says where it runs too
    await r.act(() => (r.$('[data-testid="connection-tile"][data-key="claude"]') as HTMLButtonElement).click());
    const native = await waitFor(() => {
      const x = r.$('[data-testid="connection"][data-key="claude"]');
      if (!x) throw new Error('not yet');
      return x as HTMLElement;
    }, r);
    expect(native.textContent).toContain('on Windows');
    expect(native.querySelector('[data-brand="ubuntu"]')).toBeNull();
  });

  it('the overview: a tile per CLI with its logo, what it is and its state; the first that needs attention opens, a click opens another', async () => {
    const r = show({
      connectionList: async () => [
        conn({ adapter: 'codex', key: 'codex', installed: false, status: 'not_installed', version: undefined, gates: [], prereqs: [], warnings: [] }),
        conn({ adapter: 'gemini', key: 'gemini', usable: true, gates: [], prereqs: [], warnings: [], version: '0.9.0' }),
        conn({ adapter: 'custom', key: 'custom', version: undefined, custom: true }),
      ],
    });
    const tiles = await waitFor(() => {
      const x = Array.from(r.$$('[data-testid="connection-tile"]')) as HTMLButtonElement[];
      if (x.length !== 3) throw new Error('not yet');
      return x;
    }, r);
    expect(r.$('ul[aria-label="Agent CLIs"]')).not.toBeNull();
    // codex shows its own mark (LobeHub icons; Simple Icons has none)
    expect(tiles[0]!.querySelector('[data-brand="codex"] svg path')).not.toBeNull();
    expect(tiles[0]!.textContent).toBe('CodexAgent CLI on WindowsNot installed');
    expect(tiles[1]!.querySelector('[data-brand="gemini"]')).not.toBeNull();
    expect(tiles[1]!.textContent).toBe('Gemini CLI0.9.0 on WindowsReady');
    expect(tiles[1]!.querySelector('small[data-tone="ok"]')!.textContent).toBe('Ready');
    expect(tiles[2]!.querySelector('small[data-tone="warn"]')!.textContent).toBe('1 notice to accept');
    // custom needs the human (a notice to accept): it is open, and the only card
    expect(tiles.map((t) => t.getAttribute('aria-pressed'))).toEqual(['false', 'false', 'true']);
    expect(r.$$('[data-testid="connection"]').length).toBe(1);
    expect(r.$('[data-testid="connection"]')!.getAttribute('data-adapter')).toBe('custom');
    await r.act(() => tiles[1]!.click());
    await waitFor(() => expect(r.$('[data-testid="connection"]')!.getAttribute('data-adapter')).toBe('gemini'), r);
    expect(tiles.map((t) => t.getAttribute('aria-pressed'))).toEqual(['false', 'true', 'false']);
    expect(r.$('[data-testid="connection"] [data-brand="gemini"]')).not.toBeNull();
    // a second click keeps it open: a tile selects, it does not toggle
    await r.act(() => tiles[1]!.click());
    expect(r.$('[data-testid="connection"]')!.getAttribute('data-adapter')).toBe('gemini');
  });

  it('with nothing that needs attention the first opens, and an accepted notice does not move the selection', async () => {
    let acked = false;
    const r = show({
      connectionList: async () => [
        conn({ adapter: 'claude', key: 'claude', gates: [{ id: 'g1', text: 'claude notice', acked }], usable: acked }),
        conn({ adapter: 'cline', key: 'cline', gates: [{ id: 'g2', text: 'cline notice', acked: false }] }),
      ],
      connectionAcknowledge: async () => void (acked = true),
    });
    const c = await card(r);
    expect(c.getAttribute('data-adapter')).toBe('claude');
    const gate = c.querySelector('[data-testid="gate"]')!;
    await r.act(() => byLabel(gate, 'I have read this and accept it').click());
    await r.act(() => byText(gate, 'button', 'Accept').click());
    await waitFor(() => expect(r.$('[data-testid="connection-tile"][data-key="claude"]')!.textContent).toContain('Ready'), r);
    expect(r.$('[data-testid="connection"]')!.getAttribute('data-adapter')).toBe('claude');

    // the first tile is Claude Code, whatever order the core listed them in
    const r2 = show({ connectionList: async () => [conn({ adapter: 'gemini', key: 'gemini', usable: true, gates: [] }), conn({ adapter: 'claude', key: 'claude', usable: true, gates: [] })] });
    await waitFor(() => expect(r2.$('[data-testid="connection"]')!.getAttribute('data-adapter')).toBe('claude'), r2);
  });

  it('the tiles are grouped: Claude Code with its WSL install, then the other vendors with theirs, a custom manifest last', async () => {
    const r = show({
      connectionList: async () => [
        // the core's order: adapters by id, the WSL installs last
        conn({ adapter: 'claude', key: 'claude', usable: true, gates: [] }),
        conn({ adapter: 'codex', key: 'codex', usable: true, gates: [] }),
        conn({ adapter: 'custom', key: 'custom', usable: true, gates: [], custom: true }),
        conn({ adapter: 'gemini', key: 'gemini', usable: true, gates: [] }),
        conn({ adapter: 'codex', key: 'codex@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', usable: true, gates: [] }),
        conn({ adapter: 'claude', key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', usable: true, gates: [] }),
      ],
    });
    const tiles = await waitFor(() => {
      const x = Array.from(r.$$('[data-testid="connection-tile"]')) as HTMLButtonElement[];
      if (x.length !== 6) throw new Error('not yet');
      return x;
    }, r);
    expect(tiles.map((t) => t.getAttribute('data-key'))).toEqual(['claude', 'claude@wsl:Ubuntu', 'codex', 'codex@wsl:Ubuntu', 'gemini', 'custom']);
  });

  it('says plainly when no CLI was found', async () => {
    const r = show({ connectionList: async () => [] });
    await waitFor(() => expect(r.container.textContent).toContain('No agent CLIs found'), r);
    expect(r.$('[data-testid="connection-tile"]')).toBeNull();
    expect(r.$('[data-testid="connection"]')).toBeNull();
  });

  it("Sign in runs the CLI's own login through the core in a terminal inside the card, with no project and no dock", async () => {
    const calls: [string, string | undefined][] = [];
    const writes: [string, string][] = [];
    const closes: string[] = [];
    const enc = new TextEncoder();
    let release: (() => void) | undefined;
    const term = { id: 'term_9', title: 'Sign in: claude in Ubuntu-26.04', cwd: 'C:\\Users\\k', shell: 'wsl.exe', cols: 100, rows: 24, startedAt: '', exited: false, exitCode: 0 };
    const wslConn = (signedIn: boolean) =>
      conn({
        key: 'claude@wsl:Ubuntu-26.04',
        runMode: 'wsl',
        distro: 'Ubuntu-26.04',
        platform: 'linux',
        gates: [],
        signIn: 'claude auth login',
        ...(signedIn
          ? { status: 'ready', usable: true, authenticated: true, authDetail: 'claude.ai', account: 'kyle@example.invalid' }
          : { status: 'unauthenticated', authenticated: false, authDetail: '`claude auth status --json` answered {"loggedIn": false}' }),
      });
    const list = (signedIn: boolean) => [
      wslConn(signedIn),
      conn({ key: 'gemini', adapter: 'gemini', gates: [] }),
      conn({ key: 'agy@wsl:Ubuntu-26.04', adapter: 'agy', runMode: 'wsl', distro: 'Ubuntu-26.04', gates: [] }),
    ];
    // the page's list as the core read it before the sign-in (signed out); the reads after it wait until the test lets them
    // answer, so what the card shows when the sign-in ends comes from its own check (connection.check) alone
    let releaseList: (() => void) | undefined;
    const checks: [string, string | undefined][] = [];
    const r = show({
      connectionList: async () => {
        if (checks.length === 0) return list(false);
        await new Promise<void>((res) => (releaseList = res));
        return list(true);
      },
      connectionCheck: async (adapter: string, distro?: string) => (checks.push([adapter, distro]), wslConn(true)),
      connectionSignIn: async (adapter: string, distro?: string) => (calls.push([adapter, distro]), term),
      terminalRead: async (_id, from) => {
        if (from === 0) return { data: enc.encode('Press Enter to finish signing in\r\n'), next: 33, dropped: 0, exited: false, exitCode: 0 };
        await new Promise<void>((res) => (release = res));
        return { data: new Uint8Array(0), next: 33, dropped: 0, exited: true, exitCode: 0 };
      },
      terminalWrite: async (id, d) => void writes.push([id, d]),
      terminalResize: async () => {},
      terminalClose: async (id) => void closes.push(id),
    });
    const tile = await waitFor(() => {
      const x = r.$('[data-testid="connection-tile"][data-key="claude@wsl:Ubuntu-26.04"]');
      if (!x) throw new Error('not yet');
      return x as HTMLButtonElement;
    }, r);
    await r.act(() => tile.click());
    const wsl = await waitFor(() => {
      const c = r.$('[data-testid="connection"][data-run-mode="wsl"]');
      if (!c) throw new Error('not yet');
      return c as HTMLElement;
    }, r);
    const btn = byText<HTMLButtonElement>(wsl, 'button', 'Sign in inside Ubuntu-26.04');
    // the command is the one the core sent with the Connection (its manifest's auth.login)
    expect(wsl.textContent).toContain("Runs the CLI's own sign-in (claude auth login) inside the distro, in a terminal here");
    expect(wsl.textContent).toContain('never reads or copies');
    expect(wsl.querySelector('[data-testid="sign-in-terminal"]')).toBeNull();
    await r.act(() => btn.click());
    await waitFor(() => expect(calls).toEqual([['claude', 'Ubuntu-26.04']]), r);
    // the terminal opens in the card itself: the dock is only there with a project, the Connections page is not
    const box = await waitFor(() => {
      const x = wsl.querySelector('[data-testid="sign-in-terminal"]');
      if (!x) throw new Error('not yet');
      return x as HTMLElement;
    }, r);
    expect(r.services.ui.dock.open).toBe(false);
    await waitFor(() => expect(terminals[0]?.output ?? '').toContain('Press Enter to finish signing in'), r);
    expect(box.querySelector('[data-testid="sign-in-state"]')!.textContent).toBe('claude auth login is running: answer it here.');
    expect(btn.disabled).toBe(true); // one sign-in at a time
    expect(signedInRow(wsl)).toBe('no');
    expect(wsl.querySelector('[data-testid="signed-in"]')).toBeNull();
    terminals[0]!.type('\r');
    await waitFor(() => expect(writes).toEqual([['term_9', '\r']]), r);
    release!();
    await waitFor(() => expect(terminals[0]!.output).toContain('[the sign-in exited with code 0]'), r);
    // the end of the sign-in checks this card afresh, and the card shows that answer (the cached "signed out" is gone)
    await waitFor(() => expect(box.querySelector('[data-testid="sign-in-state"]')!.textContent).toBe('The sign-in finished. Signed in as kyle@example.invalid (claude.ai).'), r);
    expect(checks).toEqual([['claude', 'Ubuntu-26.04']]);
    const card = r.$('[data-testid="connection"][data-run-mode="wsl"]') as HTMLElement;
    expect(card.querySelector('[data-testid="signed-in"]')!.textContent).toBe('Signed in as kyle@example.invalid (claude.ai)');
    expect(signedInRow(card)).toBe('yes');
    expect(r.$('[data-testid="connection-tile"][data-key="claude@wsl:Ubuntu-26.04"] small[data-tone]')!.textContent).toBe('Not signed in'); // the tiles wait for the list
    expect(btn.disabled).toBe(false);
    // the page's list, read again after the check, agrees and replaces it
    releaseList!();
    await waitFor(() => expect(r.$('[data-testid="connection-tile"][data-key="claude@wsl:Ubuntu-26.04"] small[data-tone]')!.textContent).toBe('Ready'), r);
    expect(signedInRow(card)).toBe('yes');
    await r.act(() => (box.querySelector('[data-testid="sign-in-close"]') as HTMLButtonElement).click());
    await waitFor(() => expect(wsl.querySelector('[data-testid="sign-in-terminal"]')).toBeNull(), r);
    expect(closes).toEqual(['term_9']);
    expect(terminals[0]!.disposed).toBe(true);

    // a CLI whose manifest names no sign-in Drydock can start has no button; inside WSL the card says how instead
    await r.act(() => (r.$('[data-testid="connection-tile"][data-key="gemini"]') as HTMLButtonElement).click());
    const gem = await waitFor(() => {
      const c = r.$('[data-testid="connection"][data-adapter="gemini"]');
      if (!c) throw new Error('not yet');
      return c as HTMLElement;
    }, r);
    expect(gem.querySelector('[data-testid="sign-in"]')).toBeNull();
    await r.act(() => (r.$('[data-testid="connection-tile"][data-key="agy@wsl:Ubuntu-26.04"]') as HTMLButtonElement).click());
    const agy = await waitFor(() => {
      const c = r.$('[data-testid="connection"][data-adapter="agy"]');
      if (!c) throw new Error('not yet');
      return c as HTMLElement;
    }, r);
    expect(agy.querySelector('[data-testid="sign-in"]')).toBeNull();
    expect(agy.textContent).toContain('open a terminal in the distro (wsl -d Ubuntu-26.04) and sign in the way the CLI documents');
  });

  it('Check again asks the CLI afresh for this card alone, and a signed-in card says so in green with the account', async () => {
    let signedIn = false;
    const checks: [string, string | undefined][] = [];
    const native = (yes: boolean) =>
      conn({ key: 'claude', gates: [], prereqs: [], warnings: [], signIn: 'claude auth login', ...(yes ? { authenticated: true, account: 'kyle@example.invalid', authDetail: 'claude.ai' } : { status: 'unauthenticated', authenticated: false }) });
    let answer: (() => void) | undefined;
    const r = show({
      connectionList: async () => [native(signedIn)],
      connectionCheck: async (adapter: string, distro?: string) => {
        checks.push([adapter, distro]);
        await new Promise<void>((res) => (answer = res));
        return native(signedIn);
      },
    });
    const c = await card(r);
    expect(signedInRow(c)).toBe('no');
    expect(c.querySelector('[data-testid="signed-in"]')).toBeNull();
    const again = c.querySelector('[data-testid="sign-in-check"]') as HTMLButtonElement;
    expect(again.textContent).toBe('Check again');
    await r.act(() => again.click());
    await waitFor(() => expect(again.textContent).toBe('Checking...'), r);
    expect(again.disabled).toBe(true);
    answer!();
    await waitFor(() => expect(again.textContent).toBe('Check again'), r);
    expect(checks).toEqual([['claude', undefined]]);
    expect(signedInRow(c)).toBe('no'); // still signed out: nothing claims otherwise
    expect(c.querySelector('[data-testid="signed-in"]')).toBeNull();

    signedIn = true;
    await r.act(() => again.click());
    answer!();
    const line = await waitFor(() => {
      const x = c.querySelector('[data-testid="signed-in"]');
      if (!x) throw new Error('not yet');
      return x as HTMLElement;
    }, r);
    expect(line.textContent).toBe('Signed in as kyle@example.invalid (claude.ai)');
    // green: the ok tone (the design tokens are not loaded in browser tests: the attribute says it)
    expect(line.getAttribute('data-tone')).toBe('ok');
    expect(line.querySelector('svg')).not.toBeNull();
    expect(signedInRow(c)).toBe('yes');
    expect(checks.length).toBe(2);
  });

  it('shows the agent CLIs and the MCP gateway below the tiles: Claude verified, an unproven CLI off until allowed, one with no mechanism not allowed', async () => {
    const tries: unknown[][] = [];
    const rows = (enabled: boolean) => [
      { adapter: 'claude', attached: true, canTry: false, trying: false },
      { adapter: 'codex', attached: enabled, canTry: true, trying: enabled, reason: 'codex: unverified' },
      { adapter: 'antigravity', attached: false, canTry: false, trying: false, reason: 'antigravity: unproven' },
    ];
    const r = show({
      connectionList: async () => [conn({ usable: true, gates: [] })],
      mcpStatus: async () => ({ running: true, address: '', adapters: rows(false) }),
      mcpTrySet: (async (...a: unknown[]) => (tries.push(a), { running: true, address: '', adapters: rows(a[1] as boolean) })) as never,
    });
    const section = await waitFor(() => {
      const s = r.$('[data-testid="cli-gateway-section"]');
      if (!s?.querySelector('[data-testid="cli-gateway"]')?.textContent?.includes('verified')) throw new Error('not yet');
      return s as HTMLElement;
    }, r);
    expect(section.getAttribute('aria-label')).toBe('Agent CLIs and the MCP gateway');
    expect(section.querySelector('h2')!.textContent).toBe('MCP gateway');
    // after the tiles of the agent CLIs, on the same page
    const tiles = r.$('ul[aria-label="Agent CLIs"]')!;
    expect(tiles.compareDocumentPosition(section) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // the gateway is the last part: the CI runners are on the GitHub page now
    expect(r.$('[data-testid="ci-runners-section"]')).toBeNull();
    const list = section.querySelector('[data-testid="cli-gateway"]') as HTMLElement;
    expect(list.querySelector('[data-adapter="claude"]')!.textContent).toContain('verified');
    expect(() => byLabel(list, 'Let claude use the gateway')).toThrow();
    expect(() => byLabel(list, 'Let antigravity use the gateway')).toThrow();
    expect(list.textContent).toContain('antigravity: unproven');
    const box = byLabel<HTMLInputElement>(list, 'Let codex use the gateway');
    expect(box.checked).toBe(false);
    await r.act(() => box.click());
    await waitFor(() => expect(tries).toEqual([['codex', true]]), r);
  });
});
