import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Connection, ConnectionReport } from '../../api/types.ts';
import {
  cellText,
  connectionBlurb,
  connectionKey,
  connectionName,
  connectionState,
  defaultConnection,
  distroBrand,
  matrixRows,
  needsAttention,
  netModeText,
  orderConnections,
  prereqText,
  provTone,
  reportMeta,
  runsText,
  signedInLine,
  signedInText,
  statusTone,
  stepTone,
} from './connections.ts';

const report = (over: Partial<ConnectionReport> = {}): ConnectionReport =>
  ({ adapter: 'claude', platform: 'windows', transport: 'headless', form: 'short', steps: [], capabilities: {} as never, matrix: null, summary: '', tokensUsed: 0, ok: false, elapsed: 31_500_000_000, ...over }) as ConnectionReport;

describe('connections helpers', () => {
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
    expect(statusTone('ready')).toBe('ok');
    expect(statusTone('degraded')).toBe('warn');
    expect(stepTone('fail')).toBe('err');
    expect(signedInText({ authenticated: null })).toBe('not reported');
    expect(signedInText({ authenticated: false })).toBe('no');
    expect(signedInLine({ account: 'kyle@example.invalid', authDetail: 'claude.ai' })).toBe('Signed in as kyle@example.invalid (claude.ai)');
    expect(signedInLine({})).toBe('Signed in');
    expect(prereqText({ tool: 'node', min: '22', found: false, ok: false })).toBe('node (needs 22 or newer): not found');
    expect(prereqText({ tool: 'git', found: true, version: '2.45', ok: true })).toBe('git: found 2.45');
  });

  it('tiles: the product name, what it is, the state with its tone, and which one opens first', () => {
    const base = { adapter: 'claude', installed: true, status: 'ready', usable: true, platform: 'windows', version: '2.1.0', gates: [] } as Connection;
    const inWSL = { ...base, key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu' } as Connection;
    expect(connectionName(base)).toBe('Claude Code');
    // a WSL install is "<product> WSL"; the distro is on the line below the name
    expect(connectionName(inWSL)).toBe('Claude Code WSL');
    expect(connectionName({ adapter: 'codex', runMode: 'wsl' })).toBe('Codex WSL');
    expect(connectionName({ adapter: 'custom' })).toBe('custom');
    expect(connectionBlurb(base)).toBe('2.1.0 on Windows');
    expect(connectionBlurb(inWSL)).toBe('2.1.0 in the WSL distro Ubuntu');
    expect(connectionBlurb({ runMode: 'wsl', distro: 'Ubuntu-26.04' })).toBe('Agent CLI in the WSL distro Ubuntu-26.04');
    expect(connectionBlurb({ runMode: 'native' })).toBe('Agent CLI on Windows');

    expect(connectionState(base)).toEqual({ text: 'Ready', tone: 'ok' });
    expect(connectionState({ ...base, usable: false })).toEqual({ text: 'Not usable yet', tone: 'warn' });
    expect(connectionState({ ...base, installed: false, status: 'not_installed', usable: false })).toEqual({ text: 'Not installed', tone: '' });
    expect(connectionState({ ...base, status: 'unauthenticated', usable: false })).toEqual({ text: 'Not signed in', tone: 'warn' });
    expect(connectionState({ ...base, status: 'unsupported_version', usable: false, version: '0.9' })).toEqual({ text: 'Unsupported version 0.9', tone: 'bad' });
    expect(connectionState({ ...base, status: 'degraded' })).toEqual({ text: 'Degraded', tone: 'warn' });
    const gate = (acked: boolean) => ({ id: 'g', text: 'notice', acked });
    expect(connectionState({ ...base, usable: false, gates: [gate(false)] })).toEqual({ text: '1 notice to accept', tone: 'warn' });
    expect(connectionState({ ...base, usable: false, gates: [gate(false), gate(false), gate(true)] })).toEqual({ text: '2 notices to accept', tone: 'warn' });

    // the first that needs the human (installed, not usable), else the first; a CLI that is not installed needs nothing
    const missing = { ...base, adapter: 'codex', installed: false, status: 'not_installed', usable: false } as Connection;
    expect(needsAttention(missing)).toBe(false);
    expect(needsAttention(base)).toBe(false);
    expect(needsAttention({ ...base, status: 'degraded' })).toBe(true);
    expect(defaultConnection([missing, base, { ...inWSL, usable: false, status: 'unauthenticated' }])).toBe('claude@wsl:Ubuntu');
    expect(defaultConnection([missing, base])).toBe('codex');
    expect(defaultConnection([])).toBe('');

    expect(distroBrand('Ubuntu-24.04')).toBe('ubuntu');
    expect(distroBrand('Debian')).toBe('debian');
    expect(distroBrand('openSUSE-Tumbleweed')).toBe('linux');
    expect(distroBrand(undefined)).toBe('linux');
  });

  it('tiles are ordered: Claude Code and its WSL installs, the other vendors by name with theirs, custom manifests last', () => {
    const c = (adapter: string, over: Partial<Connection> = {}): Connection => ({ adapter, installed: true, status: 'ready', usable: true, platform: 'windows', key: adapter, runMode: 'native', ...over }) as Connection;
    // the core's order: adapters by id, then the WSL installs
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
    expect(orderConnections(fromCore).map(connectionKey)).toEqual([
      'claude',
      'claude@wsl:Debian',
      'claude@wsl:Ubuntu',
      'antigravity',
      'cline',
      'codex',
      'codex@wsl:Ubuntu',
      'gemini',
      'goose',
      'opencode',
      'custom',
    ]);
    // by the product name, not the id: "Gemini CLI" sorts under G, "OpenCode" under O; several custom manifests sort by name too
    expect(orderConnections([c('zeta', { custom: true }), c('acme', { custom: true }), c('gemini'), c('claude')]).map((x) => x.adapter)).toEqual(['claude', 'gemini', 'acme', 'zeta']);
    // a custom manifest's WSL install follows it; the input is never changed
    const custom = [c('custom', { custom: true, key: 'custom@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', usable: false }), c('custom', { custom: true }), c('claude')];
    expect(orderConnections(custom).map(connectionKey)).toEqual(['claude', 'custom', 'custom@wsl:Ubuntu']);
    expect(custom.map(connectionKey)).toEqual(['custom@wsl:Ubuntu', 'custom', 'claude']);
    expect(orderConnections([])).toEqual([]);
    // the default connection is picked from the ordered list: Claude Code's WSL install before a vendor in need
    expect(defaultConnection(orderConnections([c('codex', { usable: false }), c('claude', { key: 'claude@wsl:Ubuntu', runMode: 'wsl', usable: false })]))).toBe('claude@wsl:Ubuntu');
  });

  it('a WSL install is its own card, named by its distro', () => {
    const native = { adapter: 'claude', key: 'claude', runMode: 'native' };
    const inWSL = { adapter: 'claude', key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', wsl: { distro: 'Ubuntu', wsl2: true, interop: true, setsid: true, default: true } };
    expect(connectionKey(native)).toBe('claude');
    expect(connectionKey({ adapter: 'codex' })).toBe('codex');
    expect(connectionKey(inWSL)).toBe('claude@wsl:Ubuntu');
    expect(runsText(native)).toBe('on Windows');
    expect(runsText(inWSL)).toBe('inside the WSL 2 distro Ubuntu (the default distro)');
    expect(runsText({ runMode: 'wsl', distro: 'Old', wsl: { distro: 'Old', wsl2: false, wslVersion: 1, interop: true, setsid: true } })).toBe('inside the WSL 1 distro Old');
    expect(netModeText('mirrored')).toBe('mirrored: the distro shares Windows localhost');
    expect(netModeText('nat')).toBe('nat: the distro cannot reach Windows localhost directly');
    expect(netModeText('')).toContain('NAT unless .wslconfig');
  });
});
