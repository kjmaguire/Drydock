import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { distroLabel, parseRunMode, runModeValue, supportsWSL } from './run-mode.ts';

describe('run mode helpers', () => {
  it('only an adapter that lists the wsl run mode offers it', () => {
    expect(supportsWSL({ runModes: ['native', 'wsl'] })).toBe(true);
    expect(supportsWSL({ runModes: ['native'] })).toBe(false);
    expect(supportsWSL(undefined)).toBe(false);
  });

  it('a run mode and its distro round-trip through one select value', () => {
    for (const [mode, distro, v] of [
      ['', '', ''],
      ['native', '', 'native'],
      ['wsl', 'Ubuntu-24.04', 'wsl:Ubuntu-24.04'],
    ] as const) {
      expect(runModeValue(mode, distro)).toBe(v);
      expect(parseRunMode(v)).toEqual({ runMode: mode, wslDistro: distro });
    }
    expect(runModeValue('native', 'Ubuntu')).toBe('native');
  });

  it('a distro says what the last check found about Claude Code there', () => {
    expect(distroLabel({ name: 'Ubuntu', default: true, probed: false, interop: true })).toBe('In WSL: Ubuntu (default distro)');
    expect(distroLabel({ name: 'Debian', probed: true, claude: '', interop: true })).toBe('In WSL: Debian - Claude Code not found there');
    expect(distroLabel({ name: 'Debian', probed: true, claude: '/home/k/.local/bin/claude', signedIn: false, interop: true })).toBe('In WSL: Debian - not signed in there');
    expect(distroLabel({ name: 'Debian', probed: true, claude: '/x', signedIn: true, interop: true })).toBe('In WSL: Debian');
  });
});
