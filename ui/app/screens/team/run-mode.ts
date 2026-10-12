// Where an agent's CLI runs (decision P41): pure helpers of the run-mode field.

import type { Capabilities } from '../../api/types.ts';
import type { WSLDistroInfo } from '../../api/wslClient.ts';

/** The adapter can start its CLI inside a WSL distro on this machine (Claude Code, where WSL is installed). */
export const supportsWSL = (caps: Pick<Capabilities, 'runModes'> | undefined): boolean => (caps?.runModes ?? []).includes('wsl');

/** One select value for a run mode and distro: '' follows the project, 'native', or 'wsl:<distro>'. */
export const runModeValue = (mode: string | undefined, distro: string | undefined): string => (mode === 'wsl' ? `wsl:${distro ?? ''}` : mode === 'native' ? 'native' : '');

/** The run mode and distro a select value stands for. */
export function parseRunMode(v: string): { runMode: string; wslDistro: string } {
  if (v.startsWith('wsl:')) return { runMode: 'wsl', wslDistro: v.slice(4) };
  return { runMode: v === 'native' ? 'native' : '', wslDistro: '' };
}

/** "In WSL: Ubuntu", with what the last check found about Claude Code there. */
export function distroLabel(d: WSLDistroInfo): string {
  let s = `In WSL: ${d.name}`;
  if (d.default) s += ' (default distro)';
  if (d.probed && !d.claude) s += ' - Claude Code not found there';
  else if (d.probed && d.signedIn === false) s += ' - not signed in there';
  return s;
}

/** Plain words for what running in WSL changes. */
export const WSL_HELP =
  'Inside WSL, the agent uses the Claude Code installed in that distro, with that distro’s own sign-in. A repository on a Windows drive is read across the WSL file share, which is slower; a repository inside the distro is fast, and its task worktrees are made inside the distro too.';
