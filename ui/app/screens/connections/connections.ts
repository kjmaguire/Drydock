// Pure helpers of the Connections screen: badge tones, the overview tiles (name, what it is, state, which one opens first),
// the capability matrix in order, and the plain sentences of a report.

import type { Connection, ConnectionReport, MatrixCell, Prereq } from '../../api/types.ts';
import type { TileTone } from '../../ui/tiles.tsx';
import type { Tone } from '../../ui/tokens.ts';
import { adapterLabel } from '../chats/chats.ts';

/** observed: seen during the test. declared: the CLI says so. unknown: not verified. */
export const provTone = (p: string): Tone => (p === 'observed' ? 'ok' : p === 'declared' ? 'warn' : 'muted');
export const statusTone = (s: string): Tone => (s === 'ready' ? 'ok' : s === 'degraded' ? 'warn' : 'muted');
export const stepTone = (s: string): Tone => (s === 'pass' ? 'ok' : s === 'fail' ? 'err' : 'muted');

export const signedInText = (c: Pick<Connection, 'authenticated'>): string => (c.authenticated === true ? 'yes' : c.authenticated === false ? 'no' : 'not reported');

export const matrixRows = (r: Pick<ConnectionReport, 'matrix'>): [string, MatrixCell][] => Object.entries(r.matrix ?? {}).sort(([a], [b]) => a.localeCompare(b));
export const cellText = (c: MatrixCell): string => (typeof c.value === 'string' ? c.value : JSON.stringify(c.value));

/** "claude 2.1.0 via headless on windows; short form, took 31.5 s. Tokens used by the test: not reported." */
export function reportMeta(r: ConnectionReport): string {
  return `${r.adapter}${r.cliVersion ? ` ${r.cliVersion}` : ''} via ${r.transport || 'unknown transport'} on ${r.platform}; ${r.form} form, took ${(r.elapsed / 1e9).toFixed(1)} s. Tokens used by the test: ${r.tokensUsed > 0 ? r.tokensUsed : 'not reported'}.`;
}

/** The card's key: the Windows install is the adapter id, one inside a WSL distro has its own (decision P41). */
export const connectionKey = (c: Pick<Connection, 'adapter' | 'key'>): string => c.key || c.adapter;

export const isWSL = (c: Pick<Connection, 'runMode'>): boolean => c.runMode === 'wsl';

/** The CLIs whose own sign-in Drydock can start in a terminal (the core's connections.SignInCommand table). */
const SIGN_IN: Record<string, string> = { claude: 'claude auth login', codex: 'codex login', opencode: 'opencode auth login' };
export const canSignIn = (adapter: string): boolean => adapter in SIGN_IN;
export const signInCommand = (adapter: string): string => SIGN_IN[adapter] ?? '';

/** "claude" for the Windows install, "claude in WSL (Ubuntu)" for one inside a distro. */
export const connectionTitle = (c: Pick<Connection, 'adapter' | 'runMode' | 'distro'>): string => (isWSL(c) ? `${c.adapter} in WSL (${c.distro ?? ''})` : c.adapter);

/** Where the CLI runs, in plain words. */
export function runsText(c: Pick<Connection, 'runMode' | 'distro' | 'wsl'>): string {
  if (!isWSL(c)) return 'on Windows';
  const v = c.wsl?.wsl2 || c.wsl?.wslVersion === 2 ? 'WSL 2' : c.wsl?.wslVersion === 1 ? 'WSL 1' : 'WSL';
  return `inside the ${v} distro ${c.distro ?? ''}${c.wsl?.default ? ' (the default distro)' : ''}`;
}

/** The networking mode and what it means for Drydock's tools. */
export function netModeText(mode: string | undefined): string {
  if (!mode) return 'not reported (WSL uses NAT unless .wslconfig says otherwise)';
  if (mode === 'mirrored') return 'mirrored: the distro shares Windows localhost';
  return `${mode}: the distro cannot reach Windows localhost directly`;
}

/** The tile's name: the CLI's product name ("Claude Code"), with the distro for one inside WSL ("Claude Code (Ubuntu)"). */
export const connectionName = (c: Pick<Connection, 'adapter' | 'runMode' | 'distro'>): string => (isWSL(c) ? `${adapterLabel(c.adapter)} (${c.distro ?? ''})` : adapterLabel(c.adapter));

/** The tile's "what it is" line: the version found and where it runs. */
export function connectionBlurb(c: Pick<Connection, 'version' | 'runMode' | 'distro'>): string {
  const where = isWSL(c) ? `in WSL, ${c.distro ?? ''}` : 'on Windows';
  return c.version ? `${c.version} ${where}` : `Agent CLI ${where}`;
}

/** The tile's state line and its tone: what, if anything, stands between this CLI and being usable. */
export function connectionState(c: Pick<Connection, 'installed' | 'status' | 'usable' | 'version' | 'gates'>): { text: string; tone: TileTone } {
  if (!c.installed || c.status === 'not_installed') return { text: 'Not installed', tone: '' };
  if (c.status === 'unsupported_version') return { text: `Unsupported version${c.version ? ` ${c.version}` : ''}`, tone: 'bad' };
  if (c.status === 'unauthenticated') return { text: 'Not signed in', tone: 'warn' };
  const open = (c.gates ?? []).filter((g) => !g.acked).length;
  if (open > 0) return { text: open === 1 ? '1 notice to accept' : `${open} notices to accept`, tone: 'warn' };
  if (c.status === 'degraded') return { text: 'Degraded', tone: 'warn' };
  if (c.status === 'ready') return c.usable ? { text: 'Ready', tone: 'ok' } : { text: 'Not usable yet', tone: 'warn' };
  return { text: c.status, tone: '' };
}

/** An installed CLI that is not usable (or is degraded) needs the human: sign in, accept a notice, update. */
export const needsAttention = (c: Pick<Connection, 'installed' | 'status' | 'usable'>): boolean =>
  c.installed && c.status !== 'not_installed' && (!c.usable || c.status === 'degraded');

/** The connection opened when the screen opens: the first that needs attention, else the first; '' for none. */
export function defaultConnection(list: readonly Connection[]): string {
  const c = list.find(needsAttention) ?? list[0];
  return c ? connectionKey(c) : '';
}

/** The logo of a WSL distro's Linux: Ubuntu and Debian have their own, every other distro shows the Linux mark. */
export function distroBrand(distro: string | undefined): string {
  const d = (distro ?? '').toLowerCase();
  return d.startsWith('ubuntu') ? 'ubuntu' : d.startsWith('debian') ? 'debian' : 'linux';
}

export function prereqText(p: Prereq): string {
  return `${p.tool}${p.min ? ` (needs ${p.min} or newer)` : ''}: ${p.found ? `found${p.version ? ` ${p.version}` : ''}` : 'not found'}`;
}
