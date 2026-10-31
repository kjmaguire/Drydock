import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { McpAdapterRow } from '../../api/mcpClient.ts';
import type { Agent, Connection, ConnectionReport } from '../../api/types.ts';
import {
  agentGroups,
  agentNotice,
  agentState,
  agentSub,
  capabilityName,
  cardValue,
  cellText,
  checkedWords,
  connectionKey,
  defaultAgent,
  distroBrand,
  gatewayState,
  headerLine,
  matrixFrom,
  matrixRows,
  needsAttention,
  netModeText,
  orderConnections,
  prereqText,
  provTone,
  readyLine,
  runLine,
  shortReason,
  reportMeta,
  runsText,
  signedInLine,
  signedInText,
  stepTone,
  usedBy,
} from './agents.ts';
import { cliName, wslTwins } from '../shared/cli-name.ts';

const report = (over: Partial<ConnectionReport> = {}): ConnectionReport =>
  ({ adapter: 'claude', platform: 'windows', transport: 'headless', form: 'short', steps: [], capabilities: {} as never, matrix: null, summary: '', tokensUsed: 0, ok: false, elapsed: 31_500_000_000, ...over }) as ConnectionReport;

const base = { adapter: 'claude', installed: true, status: 'ready', usable: true, platform: 'windows', version: '2.1.0', gates: [] } as Connection;
const inWSL = { ...base, key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu' } as Connection;
const missing = { ...base, adapter: 'codex', key: 'codex', installed: false, status: 'not_installed', usable: false, version: undefined } as Connection;

describe('agents helpers', () => {
  it('report meta: version, transport, duration and tokens not reported when zero', () => {
    expect(reportMeta(report())).toBe('claude via headless on windows; short form, took 31.5 s. Tokens used by the test: not reported.');
    expect(reportMeta(report({ cliVersion: '2.1.0', transport: '', tokensUsed: 42 }))).toBe('claude 2.1.0 via unknown transport on windows; short form, took 31.5 s. Tokens used by the test: 42.');
  });

  it('the matrix is sorted and values are shown as text', () => {
    const rows = matrixRows(report({ matrix: { streaming: { value: true, provenance: 'observed' }, mcp: { value: 'stdio', provenance: 'unknown' } } }));
    expect(rows.map(([k]) => k)).toEqual(['mcp', 'streaming']);
    expect(cellText(rows[0]![1])).toBe('stdio');
    expect(cellText(rows[1]![1])).toBe('true');
    expect(matrixRows(report())).toEqual([]);
  });

  it('tones and plain words', () => {
    expect(provTone('observed')).toBe('ok');
    expect(provTone('declared')).toBe('warn');
    expect(provTone('unknown')).toBe('muted');
    expect(stepTone('fail')).toBe('err');
    expect(stepTone('pass')).toBe('ok');
    expect(signedInText({ authenticated: null })).toBe('Not reported');
    expect(signedInText({ authenticated: false })).toBe('No');
    expect(signedInText({ authenticated: true })).toBe('Yes');
    expect(signedInText({ authenticated: true, account: 'kyle@example.invalid', authDetail: 'claude.ai' })).toBe('Yes, as kyle@example.invalid (claude.ai)');
    expect(signedInLine({ account: 'kyle@example.invalid', authDetail: 'claude.ai' })).toBe('Signed in as kyle@example.invalid (claude.ai)');
    expect(signedInLine({})).toBe('Signed in');
    expect(prereqText({ tool: 'node', min: '22', found: false, ok: false })).toBe('node (needs 22 or newer): not found');
    expect(prereqText({ tool: 'git', found: true, version: '2.45', ok: true })).toBe('git: found 2.45');
  });

  it('a row: the product name, where it runs with the version, its state in a word', () => {
    expect(cliName(base)).toBe('Claude Code');
    // a WSL install is "<product> WSL", without the distro (the owner: "we don't need to see the Ubuntu")
    expect(cliName(inWSL)).toBe('Claude Code WSL');
    expect(cliName(inWSL, [base, inWSL])).toBe('Claude Code WSL');
    expect(cliName({ adapter: 'custom' })).toBe('custom');
    // only the same CLI in two distros names them, so the two rows can be told apart
    const debian = { ...inWSL, key: 'claude@wsl:Debian', distro: 'Debian' } as Connection;
    expect([base, inWSL, debian].map((c) => cliName(c, [base, inWSL, debian]))).toEqual(['Claude Code', 'Claude Code WSL (Ubuntu)', 'Claude Code WSL (Debian)']);
    expect(wslTwins(base, [base, inWSL, debian])).toBe(false);

    expect(agentSub(base)).toBe('Windows · 2.1.0');
    expect(agentSub(inWSL)).toBe('WSL · 2.1.0');
    expect(agentSub({ installed: true, runMode: 'native' })).toBe('Windows');
    expect(agentSub(missing)).toBeUndefined();
    expect(agentSub({ ...missing, runMode: 'wsl' })).toBe('WSL');

    const word = (c: Partial<Connection>) => agentState({ ...base, ...c }).word;
    expect(agentState(base)).toEqual({ kind: 'ready', word: 'Ready', pill: 'Ready', tone: 'ok' });
    expect(agentState(missing)).toEqual({ kind: 'install', word: 'Install', pill: 'Not installed', tone: 'muted' });
    expect(word({ status: 'unauthenticated', usable: false })).toBe('Sign in');
    expect(word({ authenticated: false, usable: false })).toBe('Sign in');
    expect(word({ status: 'unsupported_version', usable: false })).toBe('Unavailable');
    // found, but a prerequisite it needs is not
    expect(agentState({ ...base, status: 'not_installed', usable: false })).toEqual({ kind: 'unavailable', word: 'Unavailable', pill: 'Needs a prerequisite', tone: 'err' });
    expect(word({ usable: false, gates: [{ id: 'g', text: 'n', acked: false }] })).toBe('Accept notice');
    expect(agentState({ ...base, usable: false, gates: [{ id: 'a', text: 'n', acked: false }, { id: 'b', text: 'n', acked: false }] }).pill).toBe('2 notices to accept');
    expect(agentState({ ...base, status: 'degraded', reason: 'version_untested' })).toEqual({ kind: 'untested', word: 'Untested', pill: 'Version not tested yet', tone: 'warn' });
    expect(word({ status: 'degraded', reason: 'drift:schema' })).toBe('Untested');
    expect(word({ status: 'degraded', reason: 'test_failed:timeout' })).toBe('Test failed');
    expect(agentState({ ...base, status: 'degraded', reason: 'wsl_setup', usable: false })).toEqual({ kind: 'unavailable', word: 'Unavailable', pill: 'Distro setup incomplete', tone: 'err' });
    expect(word({ status: 'degraded', reason: 'transport_fallback:acp', usable: false })).toBe('Unavailable');
    expect(word({ status: 'ready', usable: false })).toBe('Unavailable');
  });

  it('groups: the installed ones, then the ones not found; the first that needs the person opens', () => {
    const untested = { ...base, adapter: 'gemini', key: 'gemini', status: 'degraded', reason: 'version_untested' } as Connection;
    const g = agentGroups([missing, untested, base]);
    expect(g.installed.map(connectionKey)).toEqual(['claude', 'gemini']);
    expect(g.missing.map(connectionKey)).toEqual(['codex']);
    expect(needsAttention(missing)).toBe(false);
    expect(needsAttention(base)).toBe(false);
    expect(needsAttention(untested)).toBe(true);
    expect(defaultAgent([missing, untested, base])).toBe('gemini');
    expect(defaultAgent([missing, base])).toBe('claude');
    expect(defaultAgent([missing])).toBe('codex');
    expect(defaultAgent([])).toBe('');
    expect(readyLine([missing, untested, base])).toBe('The agent CLIs your crew runs on · 1 of 2 ready');
    expect(readyLine([missing])).toBe('The agent CLIs your crew runs on');
  });

  it('the order: Claude Code and its WSL installs, the other vendors by name with theirs, custom manifests last', () => {
    const c = (adapter: string, over: Partial<Connection> = {}): Connection => ({ adapter, installed: true, status: 'ready', usable: true, platform: 'windows', key: adapter, runMode: 'native', ...over }) as Connection;
    const fromCore = [
      c('antigravity'),
      c('claude'),
      c('cline'),
      c('codex'),
      c('custom', { custom: true }),
      c('gemini'),
      c('goose'),
      c('opencode'),
      c('claude', { key: 'claude@wsl:Debian', runMode: 'wsl', distro: 'Debian' }),
      c('claude', { key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu' }),
      c('codex', { key: 'codex@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu' }),
    ];
    expect(orderConnections(fromCore).map(connectionKey)).toEqual(['claude', 'claude@wsl:Debian', 'claude@wsl:Ubuntu', 'antigravity', 'cline', 'codex', 'codex@wsl:Ubuntu', 'gemini', 'goose', 'opencode', 'custom']);
    expect(orderConnections([c('zeta', { custom: true }), c('acme', { custom: true }), c('gemini'), c('claude')]).map((x) => x.adapter)).toEqual(['claude', 'gemini', 'acme', 'zeta']);
    const custom = [c('custom', { custom: true, key: 'custom@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', usable: false }), c('custom', { custom: true }), c('claude')];
    expect(orderConnections(custom).map(connectionKey)).toEqual(['claude', 'custom', 'custom@wsl:Ubuntu']);
    expect(custom.map(connectionKey)).toEqual(['custom@wsl:Ubuntu', 'custom', 'claude']); // the input is never changed
    expect(orderConnections([])).toEqual([]);
    expect(defaultAgent([c('codex', { usable: false }), c('claude', { key: 'claude@wsl:Ubuntu', runMode: 'wsl', usable: false })])).toBe('claude@wsl:Ubuntu');
  });

  it('where it runs, the distro, and the line under the name', () => {
    const facts = { distro: 'Ubuntu', wsl2: true, interop: true, setsid: true, default: true };
    expect(connectionKey({ adapter: 'codex' })).toBe('codex');
    expect(connectionKey(inWSL)).toBe('claude@wsl:Ubuntu');
    expect(runsText(base)).toBe('on Windows');
    expect(runsText({ ...inWSL, wsl: facts })).toBe('inside the WSL 2 distro Ubuntu (the default distro)');
    expect(runsText({ runMode: 'wsl', distro: 'Old', wsl: { distro: 'Old', wsl2: false, wslVersion: 1, interop: true, setsid: true } })).toBe('inside the WSL 1 distro Old');
    expect(netModeText('mirrored')).toBe('mirrored: the distro shares Windows localhost');
    expect(netModeText('nat')).toBe('nat: the distro cannot reach Windows localhost directly');
    expect(netModeText('')).toContain('NAT unless .wslconfig');
    expect(distroBrand('Ubuntu-24.04')).toBe('ubuntu');
    expect(distroBrand('Debian')).toBe('debian');
    expect(distroBrand('openSUSE-Tumbleweed')).toBe('linux');
    expect(distroBrand(undefined)).toBe('linux');

    const now = 1_000_000_000;
    expect(checkedWords(now - 5_000, now)).toBe('checked just now');
    expect(checkedWords(now - 120_000, now)).toBe('checked 2 min ago');
    expect(checkedWords(now - 7_200_000, now)).toBe('checked 2 h ago');
    expect(checkedWords(now - 49 * 3_600_000, now)).toBe('checked 2 d ago');
    expect(headerLine({ ...base, authenticated: true }, now - 120_000, now)).toBe('On Windows · signed in · checked 2 min ago');
    expect(headerLine({ ...inWSL, wsl: facts, authenticated: false }, now, now)).toBe('In WSL 2 · not signed in · checked just now');
    expect(headerLine(base, undefined, now)).toBe('On Windows · sign-in not reported');
    expect(headerLine(missing, undefined, now)).toBe('On Windows · not found');
  });

  it('one notice, the most important, with the action that fixes it', () => {
    expect(agentNotice(base)).toBeUndefined();
    expect(agentNotice(missing)).toEqual({ tone: 'muted', text: 'Codex was not found on this PC. Install it yourself, then check again.', fix: 'install', label: 'How to install' });
    expect(agentNotice({ ...missing, runMode: 'wsl', distro: 'Ubuntu' })!.text).toContain('not found inside Ubuntu');
    const node = { tool: 'node', min: '22', found: false, ok: false };
    expect(agentNotice({ ...base, status: 'not_installed', usable: false, prereqs: [node] })).toMatchObject({ text: 'It needs node 22 or newer.', fix: 'install' });
    const old = 'Claude Code 0.9 is older than the minimum supported 1.0 (recommended 2.1)';
    expect(agentNotice({ ...base, status: 'unsupported_version', usable: false, problems: [old] })).toEqual({ tone: 'err', text: `${old}.`, fix: 'install', label: 'How to update', from: old });
    // signed out: the CLI's own sign-in when the core names one, else how to sign in
    expect(agentNotice({ ...base, status: 'unauthenticated', signIn: 'claude auth login', usable: false })).toMatchObject({ fix: 'sign-in', label: 'Sign in' });
    expect(agentNotice({ ...inWSL, status: 'unauthenticated', usable: false })!.text).toBe('Not signed in. Open a terminal in the distro (wsl -d Ubuntu) and sign in the way the CLI documents.');
    expect(agentNotice({ ...base, usable: false, gates: [{ id: 'g', text: 'n', acked: false }] })).toMatchObject({ fix: 'notice', label: 'Read the notice' });
    const untested = 'Claude Code 2.1.309 has not been verified by Drydock yet; run Test connection';
    expect(agentNotice({ ...base, version: '2.1.309', status: 'degraded', reason: 'version_untested', problems: [untested] })).toEqual({
      tone: 'warn',
      text: 'Drydock has not tested version 2.1.309 yet. A test checks that it starts, answers and stops.',
      fix: 'test',
      label: 'Test connection',
      from: untested,
    });
    expect(agentNotice({ ...base, status: 'degraded', reason: 'test_failed:timeout', problems: ['the last Test connection failed: timeout'] })).toMatchObject({ text: 'The last Test connection failed: timeout.', label: 'Test again' });
    // a distro without Windows interop: Drydock's tools cannot reach an agent there
    const off = { distro: 'Ubuntu', wsl2: true, interop: false, setsid: true };
    expect(agentNotice({ ...inWSL, status: 'degraded', reason: 'wsl_setup', usable: false, wsl: off })).toMatchObject({ text: "Windows interop is off in Ubuntu: Drydock's tools cannot reach an agent there.", fix: 'check' });
    const how = "Windows interop is off in this distro, so Drydock's tools cannot reach an agent here: set [interop] enabled=true in /etc/wsl.conf, then wsl --terminate Ubuntu";
    expect(agentNotice({ ...inWSL, status: 'degraded', reason: 'wsl_setup', usable: false, wsl: off, problems: [how] })).toMatchObject({ text: `${how}.`, from: how });
  });

  it('the MCP gateway: verified and always on, allowed, off until allowed, not available with the reason', () => {
    const row = (r: Partial<McpAdapterRow>): McpAdapterRow => ({ adapter: 'x', attached: false, canTry: false, trying: false, ...r });
    expect(gatewayState(row({ attached: true }))).toMatchObject({ kind: 'verified', on: true, canSwitch: false });
    expect(gatewayState(row({ attached: true, canTry: true, trying: true }))).toMatchObject({ kind: 'allowed', on: true, canSwitch: true });
    expect(gatewayState(row({ canTry: true }))).toMatchObject({ kind: 'off', on: false, canSwitch: true });
    expect(gatewayState(row({ reason: 'antigravity: unproven' }))).toEqual({ kind: 'unavailable', text: 'Not available: antigravity: unproven', canSwitch: false, on: false });
    expect(gatewayState(undefined).kind).toBe('unknown');
  });

  it('used by: the agents on the same CLI in the same place, by their own run mode or their project', () => {
    const a = (id: string, over: Partial<Agent> = {}): Agent => ({ id, projectId: 'p1', name: id, adapter: 'claude', role: 'builder', model: '', effort: '', providerId: '', permissionMode: '', status: 'idle', ...over });
    const ubuntu = { ...inWSL, wsl: { distro: 'Ubuntu', wsl2: true, interop: true, setsid: true, default: true } } as Connection;
    const debian = { ...inWSL, key: 'claude@wsl:Debian', distro: 'Debian' } as Connection;
    const all = [base, ubuntu, debian];
    const agents = [
      a('native'),
      a('own-wsl', { runMode: 'wsl', wslDistro: 'debian' }),
      a('own-native', { runMode: 'native', projectId: 'p2' }),
      a('follows-wsl', { projectId: 'p2' }),
      a('wsl-default', { runMode: 'wsl' }),
      a('codex', { adapter: 'codex' }),
    ];
    const projects = new Map([
      ['p1', { runMode: 'native' }],
      ['p2', { runMode: 'wsl', wslDistro: 'Ubuntu' }],
    ]);
    const wsl = () => true;
    expect(usedBy(base, agents, projects, all, wsl).map((x) => x.id)).toEqual(['native', 'own-native']);
    expect(usedBy(ubuntu, agents, projects, all, wsl).map((x) => x.id)).toEqual(['follows-wsl', 'wsl-default']);
    expect(usedBy(debian, agents, projects, all, wsl).map((x) => x.id)).toEqual(['own-wsl']);
    // a project in WSL runs a CLI whose adapter cannot run there on Windows
    const codex = { ...base, adapter: 'codex', key: 'codex' } as Connection;
    expect(usedBy(codex, [a('c', { adapter: 'codex', projectId: 'p2' })], projects, [codex], (ad) => ad !== 'codex').map((x) => x.id)).toEqual(['c']);
    // the adapter's run modes decide, as in the core, not whether a WSL install was found: no WSL row listed, still not Windows
    expect(usedBy(base, [a('follows-wsl', { projectId: 'p2' })], projects, [base], wsl).map((x) => x.id)).toEqual([]);
  });
});

describe('the card matrix and the history in words (decision P132)', () => {
  it('names the capabilities, says yes or no, where the matrix comes from and each kept run in a line', () => {
    expect([capabilityName('tool_call_visibility'), capabilityName('permission_modes'), capabilityName('some_new_thing')]).toEqual(['Shows its tool calls', 'Permission modes', 'some new thing']);
    expect([cardValue({ value: true, provenance: 'observed' }), cardValue({ value: false, provenance: 'declared' }), cardValue({ value: 'default, plan', provenance: 'declared' })]).toEqual([
      'Yes',
      'No',
      'default, plan',
    ]);
    const now = Date.parse('2026-10-30T12:00:00Z');
    expect(matrixFrom({ matrixAt: now - 2 * 86_400_000 }, now)).toBe('With what the test 2 d ago saw');
    expect(matrixFrom({ matrixAt: 0 }, now)).toBe('What the adapter declares: no test yet');
    expect(runLine({ id: 1, cliVersion: '2.1.0', ok: true, prompt: true, passed: 6, failed: 0, skipped: 0, elapsedMs: 9_000, at: now })).toBe('2.1.0 · 6 passed · with the tiny prompt · 9.0 s');
    expect(runLine({ id: 2, ok: false, prompt: false, passed: 0, failed: 1, skipped: 4, elapsedMs: 0, at: now })).toBe('0 passed, 1 failed, 4 skipped');
    expect(shortReason('T2: timeout')).toBe('T2: timeout');
    expect(shortReason('x'.repeat(300)).length).toBe(160);
  });
});
