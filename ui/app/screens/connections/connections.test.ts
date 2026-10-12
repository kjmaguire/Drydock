import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { ConnectionReport } from '../../api/types.ts';
import { cellText, connectionKey, connectionTitle, matrixRows, netModeText, prereqText, provTone, reportMeta, runsText, signedInText, statusTone, stepTone } from './connections.ts';

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
    expect(prereqText({ tool: 'node', min: '22', found: false, ok: false })).toBe('node (needs 22 or newer): not found');
    expect(prereqText({ tool: 'git', found: true, version: '2.45', ok: true })).toBe('git: found 2.45');
  });

  it('a WSL install is its own card, named by its distro', () => {
    const native = { adapter: 'claude', key: 'claude', runMode: 'native' };
    const inWSL = { adapter: 'claude', key: 'claude@wsl:Ubuntu', runMode: 'wsl', distro: 'Ubuntu', wsl: { distro: 'Ubuntu', wsl2: true, interop: true, setsid: true, default: true } };
    expect(connectionKey(native)).toBe('claude');
    expect(connectionKey({ adapter: 'codex' })).toBe('codex');
    expect(connectionKey(inWSL)).toBe('claude@wsl:Ubuntu');
    expect(connectionTitle(native)).toBe('claude');
    expect(connectionTitle(inWSL)).toBe('claude in WSL (Ubuntu)');
    expect(runsText(native)).toBe('on Windows');
    expect(runsText(inWSL)).toBe('inside the WSL 2 distro Ubuntu (the default distro)');
    expect(runsText({ runMode: 'wsl', distro: 'Old', wsl: { distro: 'Old', wsl2: false, wslVersion: 1, interop: true, setsid: true } })).toBe('inside the WSL 1 distro Old');
    expect(netModeText('mirrored')).toBe('mirrored: the distro shares Windows localhost');
    expect(netModeText('nat')).toBe('nat: the distro cannot reach Windows localhost directly');
    expect(netModeText('')).toContain('NAT unless .wslconfig');
  });
});
