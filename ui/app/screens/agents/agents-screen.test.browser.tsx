import { expect } from 'remix/assert';
import type { Handle } from 'remix/component';
import { afterEach, beforeEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { McpAdapterRow } from '../../api/mcpClient.ts';
import type { AdapterInfo, Agent, Connection, ConnectionReport, Project } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, renderPlain, testServices, waitFor } from '../../test/render.tsx';
import { setTerminalLoader, type TerminalFactory } from '../dock/widgets.ts';
import { adapterInfo } from '../project/wizard-fixtures.ts';
import { AgentsScreen, ReportView } from './agents-screen.tsx';

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

/** A connection with nothing in the way. */
const ready = (over: Partial<Connection> = {}): Connection => conn({ usable: true, gates: [], prereqs: [], warnings: [], ...over });

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

const agent = (id: string, over: Partial<Agent> = {}): Agent => ({ id, projectId: 'p1', name: id, adapter: 'claude', role: 'builder', model: 'sonnet', effort: '', providerId: '', permissionMode: '', status: 'idle', ...over });

/** TEST DOUBLE for one adapter of adapter.list: the wizard's fixture with the run modes the CLI declares. */
const adapterWith = (id: string, runModes: string[]): AdapterInfo => {
  const info = adapterInfo(id);
  return { ...info, capabilities: { ...info.capabilities, runModes } };
};

/** The names under Used by, in order. */
const usedNames = (el: Element) => Array.from(el.querySelectorAll('[data-testid="used-by-agent"] a')).map((a) => a.textContent);

function show(client: Partial<Client>, props: { initial?: string; navigations?: string[] } = {}) {
  // the MCP gateway's state, the adapters and the projects' run modes are TEST DOUBLEs too: Claude verified and nothing else
  // reported; Claude Code declaring that it runs on Windows and in WSL, so "Used by" goes by the declared run modes the way it
  // does on a real core; every project on Windows
  const defaults: Partial<Client> = {
    mcpStatus: async () => ({ running: true, address: '127.0.0.1:1', adapters: [{ adapter: 'claude', attached: true, canTry: false, trying: false }] }),
    adapterList: async () => [adapterWith('claude', ['native', 'wsl'])],
    projectRunModes: async () => ({}),
  };
  const nav = props.navigations ?? [];
  const s = testServices({ ...defaults, ...client });
  const r = mount(<AgentsScreen initial={props.initial} onNavigate={(h) => void nav.push(h)} />, s);
  return Object.assign(r, { s });
}
type R = ReturnType<typeof show>;

const detail = (r: R, key?: string) =>
  waitFor(() => {
    const c = r.$(key ? `[data-testid="agent-detail"][data-key="${key}"]` : '[data-testid="agent-detail"]');
    if (!c) throw new Error('not yet');
    return c as HTMLElement;
  }, r);
const rows = (r: R, n: number) =>
  waitFor(() => {
    const x = Array.from(r.$$('[data-testid="agent-row"]')) as HTMLButtonElement[];
    if (x.length !== n) throw new Error(`${x.length} rows`);
    return x;
  }, r);
/** The element once it is there. */
const find = (r: R, sel: string, root: ParentNode = r.container) =>
  waitFor(() => {
    const x = root.querySelector(sel);
    if (!x) throw new Error(`no ${sel}`);
    return x as HTMLElement;
  }, r);
const row = (r: R, key: string) => r.$(`[data-testid="agent-row"][data-key="${key}"]`) as HTMLButtonElement;
/** A fact of the detail's grid by its label. */
const fact = (el: Element, label: string): string | undefined =>
  Array.from(el.querySelectorAll('[data-testid="agent-facts"] dt'))
    .find((d) => d.textContent === label)
    ?.nextElementSibling?.textContent ?? undefined;
const tab = async (r: R, el: Element, name: 'overview' | 'tests' | 'install') => {
  await r.act(() => (el.querySelector(`[data-testid="agent-tab-${name}"]`) as HTMLButtonElement).click());
};

describe('Agents page', () => {
  it('lists the CLIs as rows under Installed and Not installed, with where it runs, the version and its state in a word', async () => {
    const r = show({
      connectionList: async () => [
        ready({ adapter: 'codex', key: 'codex', installed: false, status: 'not_installed', version: undefined }),
        ready({ adapter: 'gemini', key: 'gemini', version: '0.9.0' }),
        conn({ adapter: 'custom', key: 'custom', version: undefined, custom: true }),
        ready({ adapter: 'claude', key: 'claude', version: '2.1.309', status: 'degraded', reason: 'version_untested' }),
        ready({ adapter: 'claude', key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', version: '2.1.306' }),
        ready({ adapter: 'opencode', key: 'opencode', status: 'unauthenticated', authenticated: false, usable: false }),
        ready({ adapter: 'cline', key: 'cline', status: 'unsupported_version', usable: false }),
      ],
    });
    const list = await rows(r, 7);
    expect(r.$('h1')!.textContent).toBe('Agents');
    expect(r.container.textContent).toContain('The agent CLIs your crew runs on · 2 of 6 ready');
    expect(r.$('nav[aria-label="Agent CLIs"]')).not.toBeNull();
    const heads = Array.from(r.$$('nav[aria-label="Agent CLIs"] h2')).map((h) => h.textContent);
    expect(heads).toEqual(['Installed6', 'Not installed1']);
    expect(list.map((x) => x.getAttribute('data-key'))).toEqual(['claude', 'claude@wsl:Ubuntu', 'cline', 'gemini', 'opencode', 'custom', 'codex']);
    expect(row(r, 'claude').textContent).toBe('Claude CodeWindows · 2.1.309Untested');
    // "<product> WSL" without the distro (the owner: "we don't need to see the Ubuntu")
    expect(row(r, 'claude@wsl:Ubuntu').textContent).toBe('Claude Code WSLWSL · 2.1.306Ready');
    expect(row(r, 'claude@wsl:Ubuntu').getAttribute('data-run-mode')).toBe('wsl');
    expect(row(r, 'gemini').querySelector('[data-tone="ok"]')!.textContent).toBe('Ready');
    expect(row(r, 'opencode').textContent).toContain('Sign in');
    expect(row(r, 'cline').querySelector('[data-tone="err"]')!.textContent).toBe('Unavailable');
    expect(row(r, 'custom').textContent).toBe('customWindowsAccept notice');
    // a CLI that is not installed is a quiet row whose state says what to do
    expect(row(r, 'codex').getAttribute('data-quiet')).toBe('true');
    expect(row(r, 'codex').textContent).toBe('CodexInstall');
    // codex shows its own mark (LobeHub icons; Simple Icons has none)
    expect(row(r, 'codex').querySelector('[data-brand="codex"] svg path')).not.toBeNull();
    // the first installed one that needs the person is open
    expect(row(r, 'claude').getAttribute('aria-current')).toBe('true');
    expect((await detail(r)).getAttribute('data-key')).toBe('claude');
  });

  it('a chosen row opens its detail and goes in the address; the address opens a row; the screen stays mounted', async () => {
    const navigations: string[] = [];
    const r = show(
      {
        connectionList: async () => [ready({ key: 'claude' }), ready({ adapter: 'gemini', key: 'gemini' }), ready({ key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu' })],
      },
      { navigations },
    );
    await rows(r, 3);
    // nothing needs the person: the first opens, and the address is left as it is
    expect((await detail(r)).getAttribute('data-key')).toBe('claude');
    expect(navigations).toEqual([]);
    // choosing the row that opened by itself names it in the address
    await r.act(() => row(r, 'claude').click());
    expect(navigations).toEqual(['/agents/claude']);
    await r.act(() => row(r, 'claude@wsl:Ubuntu').click());
    await detail(r, 'claude@wsl:Ubuntu');
    expect(r.$$('[data-testid="agent-detail"]').length).toBe(1);
    expect(navigations).toEqual(['/agents/claude', '/agents/claude%40wsl%3AUbuntu']);
    expect(row(r, 'claude@wsl:Ubuntu').getAttribute('aria-current')).toBe('true');
    // a second click keeps it open and does not add to the address
    await r.act(() => row(r, 'claude@wsl:Ubuntu').click());
    expect(navigations.length).toBe(2);
    // the arrow keys move through the rows, and the address follows
    row(r, 'claude@wsl:Ubuntu').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    await detail(r, 'gemini');
    expect(navigations.at(-1)).toBe('/agents/gemini');
  });

  it('/agents/<key> opens that agent, and a new address opens another without remounting', async () => {
    let reads = 0;
    const list = [ready({ key: 'claude' }), ready({ adapter: 'gemini', key: 'gemini' })];
    const ctl: { set?: (k: string) => void } = {};
    // the controller renders the page with one key for every address, so a new address is a new prop
    function Host(handle: Handle) {
      let initial = 'gemini';
      ctl.set = (k) => ((initial = k), void handle.update());
      return () => <AgentsScreen key="agents" initial={initial} onNavigate={() => {}} />;
    }
    const s = testServices({ connectionList: async () => (reads++, list), mcpStatus: async () => ({ running: true, address: '', adapters: [] }), adapterList: async () => [] });
    const r = mount(<Host />, s);
    await waitFor(() => expect(r.$('[data-testid="agent-detail"]')?.getAttribute('data-key')).toBe('gemini'), r);
    await r.act(() => ctl.set!('claude'));
    await waitFor(() => expect(r.$('[data-testid="agent-detail"]')?.getAttribute('data-key')).toBe('claude'), r);
    expect(reads).toBe(1);
    // the page hands the layout the key its address names, so a narrow window shows that detail in place of the list
    expect(r.$('[data-testid="agents"]')!.getAttribute('data-open')).toBe('true');
    // an address for an install that is not listed falls back to the first that needs the person, else the first
    const r2 = mount(<AgentsScreen initial="nope" onNavigate={() => {}} />, testServices({ connectionList: async () => list, mcpStatus: async () => ({ running: true, address: '', adapters: [] }), adapterList: async () => [] }));
    await waitFor(() => expect(r2.$('[data-testid="agent-detail"]')?.getAttribute('data-key')).toBe('claude'), r2);
    // the row the address named is gone: the default is drawn, and a narrow window keeps the list rather than open it
    expect(r2.$('[data-testid="agents"]')!.getAttribute('data-open')).toBe('false');
  });

  it('the detail: the header with its state and one line, the actions, one notice, and three tabs', async () => {
    const r = show({
      connectionList: async () => [
        ready({
          key: 'claude',
          version: '2.1.309',
          path: 'C:\\Users\\you\\.local\\bin\\claude.exe',
          transport: 'headless',
          status: 'degraded',
          reason: 'version_untested',
          authenticated: true,
          account: 'kyle@example.invalid',
          signIn: 'claude auth login',
          problems: ['Claude Code 2.1.309 has not been verified by Drydock yet; run Test connection', 'something else to know'],
          warnings: ['a terms warning'],
          installHint: 'irm https://claude.ai/install.ps1 | iex',
        }),
      ],
    });
    const d = await detail(r);
    const head = d.querySelector('[data-testid="agent-header"]')!;
    expect(head.querySelector('h2')!.textContent).toBe('Claude Code');
    expect(head.querySelector('[data-brand="claude"]')).not.toBeNull();
    expect(head.querySelector('[data-testid="detail-status"]')!.textContent).toBe('Version not tested yet');
    expect(head.querySelector('[data-testid="detail-status"]')!.getAttribute('data-tone')).toBe('warn');
    expect(head.textContent).toContain('On Windows · signed in · checked just now');
    expect(byText(head, 'button', 'Test connection')).not.toBeNull();
    expect(byText(head, 'button', 'Sign in again')).not.toBeNull();
    expect(byText(head, 'button', 'Check again')).not.toBeNull();
    // at most one notice: the most important thing in the way, with its fix
    const notices = d.querySelectorAll('[data-testid="agent-notice"]');
    expect(notices.length).toBe(1);
    expect(notices[0]!.textContent).toContain('Drydock has not tested version 2.1.309 yet.');
    expect(notices[0]!.querySelector('button')!.textContent).toBe('Test connection');
    // Overview: the facts; the other problems and warnings once, not the one the notice says
    expect(fact(d, 'Version')).toBe('2.1.309');
    expect(fact(d, 'Program')).toBe('C:\\Users\\you\\.local\\bin\\claude.exe');
    expect(fact(d, 'Signed in')).toBe('Yes, as kyle@example.invalid');
    expect(d.querySelector('[data-testid="signed-in"]')!.getAttribute('data-tone')).toBe('ok');
    expect(fact(d, 'Transport')).toBe('headless');
    expect(fact(d, 'Runs')).toBe('On Windows');
    const notes = d.querySelector('[data-testid="agent-notes"]')!;
    expect(notes.textContent).toContain('something else to know');
    expect(notes.textContent).toContain('a terms warning');
    expect(notes.textContent).not.toContain('has not been verified');
    expect(d.textContent).toContain('Its own sign-in (claude auth login) runs in a terminal here. Drydock never reads or copies what it stores.');
    // the tabs
    const tabs = Array.from(d.querySelectorAll('[role="tab"]')).map((x) => x.textContent);
    expect(tabs).toEqual(['Overview', 'Test results', 'Install and update']);
    await tab(r, d, 'tests');
    await waitFor(() => expect(d.querySelector('[data-testid="test-results"]')!.textContent).toContain('No test of Claude Code in this session yet.'), r);
    await tab(r, d, 'install');
    const install = await find(r, '[data-testid="install-tab"]', d);
    expect(install.querySelector('[data-testid="install-hint"]')!.textContent).toBe('irm https://claude.ai/install.ps1 | iex');
    expect(install.textContent).toContain('Drydock never runs this for you');
    expect(install.querySelector('[data-testid="copy-install"]')).not.toBeNull();
  });

  it('shows the notice text and only the person can accept it: checkbox first, then Accept calls connection.acknowledge', async () => {
    const acks: string[] = [];
    let acked = false;
    const r = show({
      connectionList: async () => [conn(acked ? { gates: [{ id: 'claude-terms', text: 'Drydock uses your own unmodified claude login.', acked: true }] } : {})],
      connectionAcknowledge: async (id: string) => {
        acks.push(id);
        acked = true;
      },
    });
    const c = await detail(r);
    expect(c.querySelector('[data-testid="agent-notice"]')!.textContent).toContain('Read and accept its notice');
    expect(c.textContent).toContain('Drydock uses your own unmodified claude login.');
    const gate = c.querySelector('[data-testid="gate"]')!;
    expect(gate.getAttribute('data-acked')).toBe('false');
    // the notice's button takes the keyboard to the notice's box (the row chosen first: the test page is narrow, and a narrow
    // window shows the detail once a row is chosen)
    await r.act(() => row(r, 'claude').click());
    await r.act(() => (c.querySelector('[data-testid="agent-notice-fix"]') as HTMLButtonElement).click());
    await waitFor(() => expect(document.activeElement).toBe(byLabel(gate, 'I have read this and accept it')), r);
    const accept = byText<HTMLButtonElement>(gate, 'button', 'Accept');
    expect(accept.disabled).toBe(true);
    await r.act(() => accept.click());
    expect(acks).toEqual([]);
    await r.act(() => byLabel(gate, 'I have read this and accept it').click());
    expect(accept.disabled).toBe(false);
    await r.act(() => accept.click());
    await waitFor(() => expect(acks).toEqual(['claude-terms']), r);
    await waitFor(() => expect(r.$('[data-testid="gate"]')!.getAttribute('data-acked')).toBe('true'), r);
    expect(r.$('[data-testid="gate"]')!.textContent).toContain('accepted');
  });

  it('shows the core refusal when accepting fails', async () => {
    const r = show({
      connectionList: async () => [conn()],
      connectionAcknowledge: async () => {
        throw new Error('unknown gate');
      },
    });
    const c = await detail(r);
    const gate = c.querySelector('[data-testid="gate"]')!;
    await r.act(() => byLabel(gate, 'I have read this and accept it').click());
    await r.act(() => byText(gate, 'button', 'Accept').click());
    await waitFor(() => expect(c.querySelector('[role="alert"]')!.textContent).toContain('unknown gate'), r);
  });

  it('shows a problem and a warning side by side without a key the two share (the runtime warned "Duplicate keys" "0")', async () => {
    const warned: string[] = [];
    const warn = console.warn;
    console.warn = (...a: unknown[]) => void warned.push(a.map(String).join(' '));
    try {
      const r = show({ connectionList: async () => [conn({ problems: ['claude is not on PATH'], warnings: ['version not tested'] })] });
      const c = await detail(r);
      expect(c.textContent).toContain('claude is not on PATH');
      expect(c.textContent).toContain('version not tested');
    } finally {
      console.warn = warn;
    }
    expect(warned.filter((w) => w.includes('Duplicate keys'))).toEqual([]);
  });

  it('prerequisites with their guidance and the install guidance are under Install and update; Signed in says when it is not reported', async () => {
    const r = show({ connectionList: async () => [conn({ usable: true, gates: [{ id: 'g', text: 'notice', acked: true }], authenticated: null, installHint: 'winget install X' })] });
    const c = await detail(r);
    expect(c.getAttribute('data-usable')).toBe('true');
    expect(fact(c, 'Signed in')).toBe('Not reported');
    expect(c.textContent).toContain('accepted');
    expect(() => byText(c, 'button', 'Accept')).toThrow();
    await tab(r, c, 'install');
    const install = await find(r, '[data-testid="install-tab"]', c);
    expect(install.textContent).toContain('Install Node 22 from nodejs.org.');
    expect(install.textContent).toContain('node (needs 22 or newer): not found');
    expect(install.textContent).toContain('never runs this for you');
    expect(install.textContent).toContain('winget install X');
  });

  it('Test connection warns about the real CLI and cost first, runs only after confirmation, and shows the report under Test results', async () => {
    const tests: string[] = [];
    let answer: (() => void) | undefined;
    const r = show({
      connectionList: async () => [conn()],
      connectionTest: async (a: string, prompt?: boolean) => {
        tests.push(`${a}:${prompt}`);
        await new Promise<void>((res) => (answer = res));
        return report();
      },
    });
    const c = await detail(r);
    await r.act(() => byText(c, 'button', 'Test connection').click());
    expect(tests).toEqual([]);
    const dialog = await find(r, '[data-testid="test-confirm"]');
    expect(dialog.closest('dialog')!.open).toBe(true);
    expect(dialog.textContent).toContain('starts the real claude CLI on this machine');
    // the tiny prompt is offered with what it costs, unticked: a test spends nothing unless the person asks
    const box = byLabel<HTMLInputElement>(dialog, /^Also send one tiny prompt/);
    expect(box.checked).toBe(false);
    expect(dialog.textContent).toContain('a fraction of a cent');
    await r.act(() => byText(dialog, 'button', 'Cancel').click());
    expect(r.$('[data-testid="test-confirm"]')).toBeNull();
    expect(tests).toEqual([]);
    await r.act(() => byText(c, 'button', 'Test connection').click());
    await r.act(() => byText(r.$('[data-testid="test-confirm"]')!, 'button', 'Run the test').click());
    await waitFor(() => expect(tests).toEqual(['claude:false']), r);
    // it opens Test results, which says the test runs, and the button waits for it
    await waitFor(() => expect(c.querySelector('[data-testid="test-results"]')!.textContent).toContain('Testing Claude Code...'), r);
    expect(byText<HTMLButtonElement>(c, 'button', 'Testing...').disabled).toBe(true);
    answer!();
    const rep = await find(r, '[data-testid="connection-report"]', c);
    expect(rep.textContent).toContain('The CLI started but did not answer.');
    expect(rep.textContent).toContain('did not pass');
    // a second test with the prompt ticked
    await r.act(() => byText(c, 'button', 'Test connection').click());
    await r.act(() => byLabel<HTMLInputElement>(r.$('[data-testid="test-confirm"]')!, /^Also send one tiny prompt/).click());
    await r.act(() => byText(r.$('[data-testid="test-confirm"]')!, 'button', 'Run the test').click());
    await waitFor(() => expect(tests).toEqual(['claude:false', 'claude:true']), r);
    answer!();
  });

  it('a report stays with its agent when another row is chosen', async () => {
    const r = show({ connectionList: async () => [ready({ key: 'claude' }), ready({ adapter: 'gemini', key: 'gemini' })], connectionTest: async () => report({ ok: true, summary: 'All good.' }) });
    const c = await detail(r, 'claude');
    await r.act(() => byText(c, 'button', 'Test connection').click());
    await r.act(() => byText(r.$('[data-testid="test-confirm"]')!, 'button', 'Run the test').click());
    await waitFor(() => expect(r.$('[data-testid="connection-report"]')!.textContent).toContain('All good.'), r);
    await r.act(() => row(r, 'gemini').click());
    await detail(r, 'gemini');
    await r.act(() => row(r, 'claude').click());
    const back = await detail(r, 'claude');
    await tab(r, back, 'tests');
    await waitFor(() => expect(back.querySelector('[data-testid="connection-report"]')!.textContent).toContain('All good.'), r);
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

  it('a Claude Code install inside a WSL distro is its own row: where it runs, its own sign-in, the distro, and its own Test', async () => {
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
      connectionList: async () => [ready({ key: 'claude', runMode: 'native' }), inWSL],
      connectionTest: async (a: string, prompt?: boolean, distro?: string) => (tests.push(`${a}:${prompt}:${distro ?? ''}`), report()),
    });
    await rows(r, 2);
    // it needs the person (not signed in): it is open
    const c = await detail(r, 'claude@wsl:Ubuntu');
    expect(c.getAttribute('data-run-mode')).toBe('wsl');
    expect(c.querySelector('h2')!.textContent).toBe('Claude Code WSL');
    // the CLI's logo in the heading; the distro's logo and name only in the Runs fact
    const head = c.querySelector('[data-testid="agent-header"]')!;
    expect(head.querySelector('[data-brand="claude"]')).not.toBeNull();
    expect(head.querySelector('[data-brand="ubuntu"]')).toBeNull();
    expect(head.textContent).not.toContain('Ubuntu');
    expect(head.textContent).toContain('In WSL 2 · not signed in');
    expect(c.querySelector('[data-testid="agent-facts"] [data-brand="ubuntu"]')!.getAttribute('aria-label')).toBe('Ubuntu');
    expect(fact(c, 'Runs')).toBe('Inside the WSL 2 distro Ubuntu (the default distro)');
    expect(fact(c, 'Signed in')).toBe('No');
    expect(c.textContent).toContain("this install's sign-in is separate from the Windows one");
    expect(c.querySelector('[data-testid="agent-notice"]')!.textContent).toContain('Not signed in inside Ubuntu.');
    expect(byText<HTMLButtonElement>(head, 'button', 'Sign in').disabled).toBe(false);
    expect(byText<HTMLButtonElement>(head, 'button', 'Sign in').title).toBe('Runs claude auth login inside Ubuntu');
    const facts = c.querySelector('[data-testid="wsl-facts"]')!;
    expect(facts.textContent).toContain('nat: the distro cannot reach Windows localhost directly');
    expect(facts.textContent).toContain('/mnt/');
    expect(c.textContent).toContain('A repository inside the distro is fast for the agent.');
    await r.act(() => byText(head, 'button', 'Test connection').click());
    const dialog = await find(r, '[data-testid="test-confirm"]');
    expect(dialog.textContent).toContain('inside the WSL distro Ubuntu');
    await r.act(() => byText(dialog, 'button', 'Run the test').click());
    await waitFor(() => expect(tests).toEqual(['claude:false:Ubuntu']), r);
    // the Windows install says where it runs too
    await r.act(() => row(r, 'claude').click());
    const native = await detail(r, 'claude');
    expect(fact(native, 'Runs')).toBe('On Windows');
    expect(native.querySelector('[data-brand="ubuntu"]')).toBeNull();
  });

  it("Sign in runs the CLI's own login through the core in a terminal in the detail, with no project and no dock", async () => {
    const calls: [string, string | undefined][] = [];
    const writes: [string, string][] = [];
    const closes: string[] = [];
    const enc = new TextEncoder();
    let release: (() => void) | undefined;
    const term = { id: 'term_9', title: 'Sign in: claude WSL', cwd: 'C:\\Users\\k', shell: 'wsl.exe', cols: 100, rows: 24, startedAt: '', exited: false, exitCode: 0 };
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
      conn({ key: 'agy@wsl:Ubuntu-26.04', adapter: 'agy', runMode: 'wsl', distro: 'Ubuntu-26.04', gates: [], status: 'unauthenticated', authenticated: false }),
    ];
    // the page's list as the core read it before the sign-in (signed out); the reads after it wait until the test lets them
    // answer, so what the detail shows when the sign-in ends comes from its own check (connection.check) alone
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
    const wsl = await detail(r, 'claude@wsl:Ubuntu-26.04');
    const btn = wsl.querySelector('[data-testid="sign-in"]') as HTMLButtonElement;
    expect(btn.textContent).toBe('Sign in');
    // the command is the one the core sent with the Connection (its manifest's auth.login)
    expect(wsl.textContent).toContain("Its own sign-in (claude auth login) runs inside the distro, in a terminal here");
    expect(wsl.textContent).toContain('never reads or copies');
    expect(wsl.querySelector('[data-testid="sign-in-terminal"]')).toBeNull();
    // the notice's Sign in starts the same sign-in as the header's
    await r.act(() => (wsl.querySelector('[data-testid="agent-notice-fix"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toEqual([['claude', 'Ubuntu-26.04']]), r);
    // the terminal opens in the detail itself: the dock is only there with a project, the Agents page is not
    const box = await waitFor(() => {
      const x = wsl.querySelector('[data-testid="sign-in-terminal"]');
      if (!x) throw new Error('not yet');
      return x as HTMLElement;
    }, r);
    expect(r.services.ui.dock.open).toBe(false);
    await waitFor(() => expect(terminals[0]?.output ?? '').toContain('Press Enter to finish signing in'), r);
    expect(box.querySelector('[data-testid="sign-in-state"]')!.textContent).toBe('claude auth login is running: answer it here.');
    expect(btn.disabled).toBe(true); // one sign-in at a time
    expect(wsl.querySelector('[data-testid="agent-notice-fix"]')).toBeNull();
    expect(fact(wsl, 'Signed in')).toBe('No');
    expect(wsl.querySelector('[data-testid="signed-in"]')).toBeNull();
    terminals[0]!.type('\r');
    await waitFor(() => expect(writes).toEqual([['term_9', '\r']]), r);
    release!();
    await waitFor(() => expect(terminals[0]!.output).toContain('[the sign-in exited with code 0]'), r);
    // the end of the sign-in checks this install afresh, and the detail shows that answer (the cached "signed out" is gone)
    await waitFor(() => expect(box.querySelector('[data-testid="sign-in-state"]')!.textContent).toBe('The sign-in finished. Signed in as kyle@example.invalid (claude.ai).'), r);
    expect(checks).toEqual([['claude', 'Ubuntu-26.04']]);
    expect(wsl.querySelector('[data-testid="signed-in"]')!.textContent).toBe('Yes, as kyle@example.invalid (claude.ai)');
    expect(fact(wsl, 'Signed in')).toBe('Yes, as kyle@example.invalid (claude.ai)');
    expect(btn.textContent).toBe('Sign in again');
    expect(row(r, 'claude@wsl:Ubuntu-26.04').textContent).toContain('Sign in'); // the list waits for its own read
    expect(btn.disabled).toBe(false);
    // the page's list, read again after the check, agrees and replaces it
    releaseList!();
    await waitFor(() => expect(row(r, 'claude@wsl:Ubuntu-26.04').textContent).toContain('Ready'), r);
    expect(fact(wsl, 'Signed in')).toBe('Yes, as kyle@example.invalid (claude.ai)');
    await r.act(() => (box.querySelector('[data-testid="sign-in-close"]') as HTMLButtonElement).click());
    await waitFor(() => expect(wsl.querySelector('[data-testid="sign-in-terminal"]')).toBeNull(), r);
    expect(closes).toEqual(['term_9']);
    expect(terminals[0]!.disposed).toBe(true);

    // a CLI whose manifest names no sign-in Drydock can start has no button; inside WSL the detail says how instead
    await r.act(() => row(r, 'gemini').click());
    const gem = await detail(r, 'gemini');
    expect(gem.querySelector('[data-testid="sign-in"]')).toBeNull();
    await r.act(() => row(r, 'agy@wsl:Ubuntu-26.04').click());
    const agy = await detail(r, 'agy@wsl:Ubuntu-26.04');
    expect(agy.querySelector('[data-testid="sign-in"]')).toBeNull();
    expect(agy.textContent).toContain('open a terminal in the distro (wsl -d Ubuntu-26.04) and sign in the way the CLI documents');
  });

  it('Check again asks the CLI afresh for this install alone, and a signed-in one says so in green with the account', async () => {
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
    const c = await detail(r);
    expect(fact(c, 'Signed in')).toBe('No');
    expect(c.querySelector('[data-testid="signed-in"]')).toBeNull();
    const again = c.querySelector('[data-testid="sign-in-check"]') as HTMLButtonElement;
    expect(again.textContent).toBe('Check again');
    await r.act(() => again.click());
    await waitFor(() => expect(again.textContent).toBe('Checking...'), r);
    expect(again.disabled).toBe(true);
    answer!();
    await waitFor(() => expect(again.textContent).toBe('Check again'), r);
    expect(checks).toEqual([['claude', undefined]]);
    expect(fact(c, 'Signed in')).toBe('No'); // still signed out: nothing claims otherwise
    expect(c.querySelector('[data-testid="signed-in"]')).toBeNull();

    signedIn = true;
    await r.act(() => again.click());
    answer!();
    const line = await waitFor(() => {
      const x = c.querySelector('[data-testid="signed-in"]');
      if (!x) throw new Error('not yet');
      return x as HTMLElement;
    }, r);
    expect(line.textContent).toBe('Yes, as kyle@example.invalid (claude.ai)');
    // green: the ok tone (the design tokens are not loaded in browser tests: the attribute says it)
    expect(line.getAttribute('data-tone')).toBe('ok');
    expect(line.querySelector('svg')).not.toBeNull();
    expect(checks.length).toBe(2);
  });

  it('Check all again reads every CLI again', async () => {
    let reads = 0;
    let answer: (() => void) | undefined;
    const r = show({
      connectionList: async () => {
        reads++;
        if (reads > 1) await new Promise<void>((res) => (answer = res));
        return [ready({ key: 'claude' })];
      },
    });
    await detail(r);
    const all = r.$('[data-testid="check-all"]') as HTMLButtonElement;
    expect(all.textContent).toBe('Check all again');
    await r.act(() => all.click());
    await waitFor(() => expect(all.textContent).toBe('Checking...'), r);
    expect(all.disabled).toBe(true);
    answer!();
    await waitFor(() => expect(all.textContent).toBe('Check all again'), r);
    expect(reads).toBe(2);
  });

  it('the MCP gateway is one setting of the CLI: Claude verified and always on, an unproven CLI off until allowed, one with no mechanism not available', async () => {
    const tries: unknown[][] = [];
    const gw = (enabled: boolean): McpAdapterRow[] => [
      { adapter: 'claude', attached: true, canTry: false, trying: false },
      { adapter: 'codex', attached: enabled, canTry: true, trying: enabled, reason: 'codex: unverified' },
      { adapter: 'antigravity', attached: false, canTry: false, trying: false, reason: 'antigravity: unproven' },
    ];
    const r = show({
      connectionList: async () => [ready({ key: 'claude' }), ready({ adapter: 'codex', key: 'codex' }), ready({ adapter: 'antigravity', key: 'antigravity' })],
      mcpStatus: async () => ({ running: true, address: '', adapters: gw(false) }),
      mcpTrySet: (async (...a: unknown[]) => (tries.push(a), { running: true, address: '', adapters: gw(a[1] as boolean) })) as never,
    });
    const claude = await detail(r, 'claude');
    const section = await waitFor(() => {
      const s = claude.querySelector('[data-testid="cli-gateway"]');
      if (!s?.querySelector('[data-kind="verified"]')) throw new Error('not yet');
      return s as HTMLElement;
    }, r);
    expect(section.getAttribute('aria-label')).toBe('MCP gateway');
    expect(section.textContent).toContain('Verified for this CLI by real runs. Always on.');
    // on, and not something to switch off
    const sw = byLabel<HTMLButtonElement>(section, 'Let claude use the gateway');
    expect(sw.getAttribute('role')).toBe('switch');
    expect(sw.getAttribute('aria-checked')).toBe('true');
    expect(sw.getAttribute('aria-disabled')).toBe('true');
    // the CI runners are on the GitHub page
    expect(r.$('[data-testid="ci-runners-section"]')).toBeNull();

    await r.act(() => row(r, 'antigravity').click());
    const anti = await detail(r, 'antigravity');
    expect(anti.querySelector('[data-testid="gateway-state"]')!.getAttribute('data-kind')).toBe('unavailable');
    expect(anti.textContent).toContain('antigravity: unproven');
    expect(() => byLabel(anti, 'Let antigravity use the gateway')).toThrow();

    await r.act(() => row(r, 'codex').click());
    const codex = await detail(r, 'codex');
    expect(codex.querySelector('[data-testid="gateway-state"]')!.textContent).toContain('Off until you allow it');
    const box = byLabel<HTMLButtonElement>(codex, 'Let codex use the gateway');
    expect(box.getAttribute('aria-checked')).toBe('false');
    expect(box.getAttribute('aria-disabled')).toBeNull();
    await r.act(() => box.click());
    await waitFor(() => expect(tries).toEqual([['codex', true]]), r);
    await waitFor(() => expect(byLabel<HTMLButtonElement>(codex, 'Let codex use the gateway').getAttribute('aria-checked')).toBe('true'), r);
    expect(codex.querySelector('[data-testid="gateway-state"]')!.textContent).toContain('Allowed by you, not verified');
  });

  it('the gateway switch is off limits while Drydock is read-only, and says when the gateway is not running', async () => {
    const r = show({
      connectionList: async () => [ready({ adapter: 'codex', key: 'codex' })],
      mcpStatus: async () => ({ running: false, address: '', adapters: [{ adapter: 'codex', attached: false, canTry: true, trying: false }] }),
      licenseStatus: async () => ({ configured: true, readOnly: true, banner: 'Read-only' }) as never,
    });
    r.services.workspace.start();
    const c = await detail(r);
    await waitFor(() => expect(c.querySelector('[data-testid="gateway-state"]')!.textContent).toContain('The gateway is not running.'), r);
    await waitFor(() => expect(byLabel<HTMLButtonElement>(c, 'Let codex use the gateway').getAttribute('aria-disabled')).toBe('true'), r);
  });

  it('Used by: the crew agents in any project that run on this install, each linking to its page', async () => {
    const projects = [
      { id: 'p1', name: 'gocalc' },
      { id: 'p2', name: 'website' },
    ] as Project[];
    const r = show({
      connectionList: async () => [ready({ key: 'claude' }), ready({ key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', wsl: { distro: 'Ubuntu', wsl2: true, interop: true, setsid: true, default: true } })],
      projectList: async () => projects,
      agentListAll: async () => [agent('a1', { name: 'Ada' }), agent('a2', { name: 'Linus', role: 'reviewer', model: 'opus' }), agent('a3', { name: 'Mira', projectId: 'p2' }), agent('a4', { name: 'Cody', adapter: 'codex' })],
      projectRunModes: async () => ({ p1: { runMode: 'native' }, p2: { runMode: 'wsl', wslDistro: 'Ubuntu' } }),
    });
    r.services.workspace.start();
    const c = await detail(r, 'claude');
    const used = await waitFor(() => {
      const x = c.querySelector('[data-testid="used-by"]') as HTMLElement;
      if (x.querySelectorAll('[data-testid="used-by-agent"]').length !== 2) throw new Error('not yet');
      return x;
    }, r);
    const items = Array.from(used.querySelectorAll('[data-testid="used-by-agent"]'));
    expect(items.map((x) => x.textContent)).toEqual(['Adagocalc · builder · sonnet', 'Linusgocalc · reviewer · opus']);
    expect(used.textContent).toContain('2 agents in 1 project');
    const link = items[0]!.querySelector('a')!;
    expect(link.getAttribute('href')).toBe('/team/a1');
    // a short name ("Ada", "Rex") is still a 28 px target both ways (the density budget)
    expect(getComputedStyle(link).minWidth).toBe('28px');
    expect(getComputedStyle(link).minHeight).toBe('28px');
    expect(items[0]!.querySelector('[data-letters]')).not.toBeNull();
    // the project in WSL runs Mira inside the distro
    await r.act(() => row(r, 'claude@wsl:Ubuntu').click());
    const w = await detail(r, 'claude@wsl:Ubuntu');
    await waitFor(() => expect(Array.from(w.querySelectorAll('[data-testid="used-by-agent"]')).map((x) => x.querySelector('a')!.textContent)).toEqual(['Mira']), r);
    expect(w.textContent).toContain('website · builder · sonnet');
    // opening an agent of another project opens that project first
    await r.act(() => {
      const a = w.querySelector('[data-testid="used-by-agent"] a') as HTMLAnchorElement;
      a.addEventListener('click', (e) => e.preventDefault());
      a.click();
    });
    expect(r.services.workspace.projectId).toBe('p2');
  });

  it('Used by goes by the run modes the adapter declares: a CLI that runs only on Windows keeps a WSL project\'s agent on the Windows install, even when a WSL install of it was found', async () => {
    const projects = [
      { id: 'p1', name: 'gocalc' },
      { id: 'p2', name: 'website' },
    ] as Project[];
    const r = show({
      connectionList: async () => [ready({ key: 'claude' }), ready({ key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', wsl: { distro: 'Ubuntu', wsl2: true, interop: true, setsid: true, default: true } })],
      adapterList: async () => [adapterWith('claude', ['native'])],
      projectList: async () => projects,
      agentListAll: async () => [agent('a3', { name: 'Mira', projectId: 'p2' })],
      projectRunModes: async () => ({ p1: { runMode: 'native' }, p2: { runMode: 'wsl', wslDistro: 'Ubuntu' } }),
    });
    r.services.workspace.start();
    const c = await detail(r, 'claude');
    // the core runs Mira on Windows (effectiveRunMode: claude does not declare wsl), so the page lists her there
    await waitFor(() => expect(usedNames(c)).toEqual(['Mira']), r);
    expect(r.$('[role="alert"]')).toBeNull();
    await r.act(() => row(r, 'claude@wsl:Ubuntu').click());
    const w = await detail(r, 'claude@wsl:Ubuntu');
    await waitFor(() => expect(w.querySelector('[data-testid="used-by"]')!.textContent).toContain('No crew agent runs on this install yet.'), r);
    expect(usedNames(w)).toEqual([]);
  });

  it('when the adapters cannot be read, the detail says so and Used by goes by the installs found', async () => {
    const projects = [
      { id: 'p1', name: 'gocalc' },
      { id: 'p2', name: 'website' },
    ] as Project[];
    const r = show({
      connectionList: async () => [ready({ key: 'claude' }), ready({ key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', wsl: { distro: 'Ubuntu', wsl2: true, interop: true, setsid: true, default: true } })],
      adapterList: async () => {
        throw new Error('the core is restarting');
      },
      projectList: async () => projects,
      agentListAll: async () => [agent('a3', { name: 'Mira', projectId: 'p2' })],
      projectRunModes: async () => ({ p1: { runMode: 'native' }, p2: { runMode: 'wsl', wslDistro: 'Ubuntu' } }),
    });
    r.services.workspace.start();
    await detail(r, 'claude');
    // the note names what could not be read, in the core's words, so the person knows Used by is going by what was found
    const note = await waitFor(() => {
      const x = Array.from(r.$$('[role="alert"]')).find((el) => el.textContent?.includes("Could not read the adapters' run modes"));
      if (!x) throw new Error('not yet');
      return x as HTMLElement;
    }, r);
    expect(note.textContent).toBe("Could not read the adapters' run modes: the core is restarting");
    // a WSL install of claude was found, so a project in WSL runs Mira there
    await r.act(() => row(r, 'claude@wsl:Ubuntu').click());
    const w = await detail(r, 'claude@wsl:Ubuntu');
    await waitFor(() => expect(usedNames(w)).toEqual(['Mira']), r);
    await r.act(() => row(r, 'claude').click());
    const native = await detail(r, 'claude');
    await waitFor(() => expect(native.querySelector('[data-testid="used-by"]')!.textContent).toContain('No crew agent runs on this install yet.'), r);
    expect(usedNames(native)).toEqual([]);
  });

  it("when the projects' run modes cannot be read, the detail says so and an agent that follows its project is listed under the Windows install", async () => {
    const projects = [
      { id: 'p1', name: 'gocalc' },
      { id: 'p2', name: 'website' },
    ] as Project[];
    const r = show({
      connectionList: async () => [ready({ key: 'claude' }), ready({ key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', wsl: { distro: 'Ubuntu', wsl2: true, interop: true, setsid: true, default: true } })],
      projectList: async () => projects,
      agentListAll: async () => [agent('a3', { name: 'Mira', projectId: 'p2' })],
      projectRunModes: async () => {
        throw new Error('the database is being restored');
      },
    });
    r.services.workspace.start();
    const c = await detail(r, 'claude');
    const note = await waitFor(() => {
      const x = Array.from(r.$$('[role="alert"]')).find((el) => el.textContent?.includes("Could not read where the projects' agents run"));
      if (!x) throw new Error('not yet');
      return x as HTMLElement;
    }, r);
    expect(note.textContent).toBe("Could not read where the projects' agents run: the database is being restored");
    // without the projects' defaults, Mira (who follows her project) is drawn where an agent runs unless told otherwise
    await waitFor(() => expect(usedNames(c)).toEqual(['Mira']), r);
  });

  it("Used by asks the core for every project's run mode in one request, never one per project, and asks again when a project changes", async () => {
    const projects = [
      { id: 'p1', name: 'gocalc' },
      { id: 'p2', name: 'website' },
    ] as Project[];
    const perProject: string[] = [];
    let all = 0;
    const r = show({
      connectionList: async () => [ready({ key: 'claude' })],
      projectList: async () => projects,
      agentListAll: async () => [agent('a1', { name: 'Ada' }), agent('a3', { name: 'Mira', projectId: 'p2' })],
      projectRunModes: async () => (all++, { p1: { runMode: 'native' }, p2: { runMode: 'native' } }),
      projectRunMode: async (id: string) => (perProject.push(id), { runMode: 'native' }),
    });
    r.services.workspace.start();
    const c = await detail(r, 'claude');
    await waitFor(() => expect(usedNames(c)).toEqual(['Ada', 'Mira']), r);
    expect(all).toBe(1);
    expect(perProject).toEqual([]);
    // a project event (its run mode may have changed) reads them all again, still in one request
    r.s.emit({ type: 'project.updated', data: {} as never });
    await waitFor(() => expect(all).toBe(2), r);
    expect(perProject).toEqual([]);
  });

  it('Used by says so when no crew agent runs on the install', async () => {
    const r = show({ connectionList: async () => [ready({ key: 'claude' })], agentListAll: async () => [] });
    const c = await detail(r);
    await waitFor(() => expect(c.querySelector('[data-testid="used-by"]')!.textContent).toContain('No crew agent runs on this install yet.'), r);
  });

  it('a CLI that is not installed: its notice sends to Install and update, and there is nothing to test or sign in to', async () => {
    const r = show({ connectionList: async () => [ready({ adapter: 'goose', key: 'goose', installed: false, status: 'not_installed', version: undefined, installHint: 'winget install Block.Goose' })] });
    const c = await detail(r);
    expect(c.querySelector('[data-testid="detail-status"]')!.textContent).toBe('Not installed');
    expect(c.querySelector('[data-testid="test-connection"]')).toBeNull();
    expect(c.querySelector('[data-testid="sign-in"]')).toBeNull();
    expect(c.querySelector('[data-testid="sign-in-check"]')).not.toBeNull();
    const notice = c.querySelector('[data-testid="agent-notice"]')!;
    expect(notice.textContent).toContain('Goose was not found on this PC.');
    await r.act(() => (notice.querySelector('button') as HTMLButtonElement).click());
    const install = await find(r, '[data-testid="install-tab"]', c);
    expect(install.textContent).toContain('winget install Block.Goose');
    expect(c.querySelector('[data-testid="agent-tab-install"]')!.getAttribute('aria-selected')).toBe('true');
  });

  it('filters the rows by name with the filter box', async () => {
    const r = show({ connectionList: async () => [ready({ key: 'claude' }), ready({ adapter: 'gemini', key: 'gemini' })] });
    await rows(r, 2);
    const box = r.$('[data-testid="list-filter"]') as HTMLInputElement;
    expect(box.getAttribute('aria-label')).toBe('Filter agents');
    box.value = 'gem';
    box.dispatchEvent(new Event('input', { bubbles: true }));
    await rows(r, 1);
    expect(r.$('[data-testid="agent-row"]')!.getAttribute('data-key')).toBe('gemini');
  });

  it('a narrow window shows the list or the chosen agent, with Back to the list', async () => {
    const r = show({ connectionList: async () => [ready({ key: 'claude' }), ready({ adapter: 'gemini', key: 'gemini' })] });
    await rows(r, 2);
    const md = r.$('[data-testid="agents"]') as HTMLElement;
    expect(md.getAttribute('data-open')).toBe('false');
    await r.act(() => row(r, 'gemini').click());
    expect(md.getAttribute('data-open')).toBe('true');
    await r.act(() => (r.$('[data-testid="detail-back"]') as HTMLButtonElement).click());
    expect(md.getAttribute('data-open')).toBe('false');
  });

  it('says plainly when no CLI was found, shows the loading state, and an error in the core words', async () => {
    const r = show({ connectionList: async () => [] });
    await waitFor(() => expect(r.$('[data-testid="agents-empty"]')!.textContent).toContain('No agent CLIs found'), r);
    expect(r.$('[data-testid="agent-row"]')).toBeNull();
    expect(r.$('[data-testid="agent-detail"]')).toBeNull();

    const r2 = show({ connectionList: () => new Promise<Connection[]>(() => {}) });
    await waitFor(() => expect(r2.container.textContent).toContain('Looking for agent CLIs...'), r2);

    const r3 = show({
      connectionList: async () => {
        throw new Error('the core is gone');
      },
    });
    await waitFor(() => expect(r3.$('[role="alert"]')!.textContent).toContain('the core is gone'), r3);
  });
});
