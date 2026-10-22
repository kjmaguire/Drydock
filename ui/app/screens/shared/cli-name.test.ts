import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { adapterLabel, chatInstall, cliName, type CliInstall } from './cli-name.ts';

// internal/connections/testdata/cli_names.json: the cases internal/connections/name_test.go passes too, so the UI names an install
// as the core does (a moved chat's note, the sign-in terminal's title). fileURLToPath, not .pathname: on Windows the pathname
// is "/D:/a/...".
const CASES = fileURLToPath(new URL('../../../../internal/connections/testdata/cli_names.json', import.meta.url));

interface CliNames {
  products: Record<string, string>;
  cases: { adapter: string; distro: string; installedIn: string[]; want: string }[];
}

describe('the name of an agent CLI install', () => {
  it('is the same as the core gives it: "Claude Code WSL", the distro only when the CLI is installed in another distro too', () => {
    const n = JSON.parse(readFileSync(CASES, 'utf8')) as CliNames;
    expect(Object.keys(n.products).length > 0 && n.cases.length > 0).toBe(true);
    for (const [id, want] of Object.entries(n.products)) expect([id, adapterLabel(id)]).toEqual([id, want]);
    for (const c of n.cases) {
      const me: CliInstall = c.distro ? { adapter: c.adapter, runMode: 'wsl', distro: c.distro } : { adapter: c.adapter, runMode: 'native' };
      const all: CliInstall[] = c.installedIn.map((d) => ({ adapter: c.adapter, runMode: 'wsl', distro: d, installed: true, status: 'ready' }));
      expect([c, cliName(me, all)]).toEqual([c, c.want]);
      // a chat names its install the same way
      expect([c, cliName(chatInstall({ adapter: c.adapter, wslDistro: c.distro }), all)]).toEqual([c, c.want]);
    }
  });

  it('counts only the distros where the CLI is installed, of the same CLI', () => {
    const ubuntu: CliInstall = { adapter: 'claude', runMode: 'wsl', distro: 'Ubuntu', installed: true, status: 'ready' };
    const debian = (over: Partial<CliInstall>): CliInstall => ({ adapter: 'claude', runMode: 'wsl', distro: 'Debian', installed: true, status: 'ready', ...over });
    expect(cliName(ubuntu, [ubuntu, debian({ installed: false, status: 'not_installed' })])).toBe('Claude Code WSL');
    expect(cliName(ubuntu, [ubuntu, debian({ status: 'unauthenticated' })])).toBe('Claude Code WSL (Ubuntu)'); // signed out is still installed
    expect(cliName(ubuntu, [ubuntu, debian({ adapter: 'codex' })])).toBe('Claude Code WSL');
    expect(cliName(ubuntu, [ubuntu, { adapter: 'claude', runMode: 'native', installed: true, status: 'ready' }])).toBe('Claude Code WSL');
  });
});
