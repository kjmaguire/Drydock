// Pure helpers of the Agents page (decision P98; it was Connections): the list's order and groups, each install's state in a
// word, the line under its name, the one notice its detail leads with, the MCP gateway's setting for it, which crew agents run
// on it, and the plain sentences of a Test connection report.

import type { McpAdapterRow } from '../../api/mcpClient.ts';
import type { Agent, Connection, ConnectionReport, MatrixCell, Prereq } from '../../api/types.ts';
import type { ProjectRunMode } from '../../api/wslClient.ts';
import type { Tone } from '../../ui/tokens.ts';
import { agoWords } from '../../lib/format.ts';
import { adapterLabel } from '../shared/cli-name.ts';

/** observed: seen during the test. declared: the CLI says so. unknown: not verified. */
export const provTone = (p: string): Tone => (p === 'observed' ? 'ok' : p === 'declared' ? 'warn' : 'muted');
export const stepTone = (s: string): Tone => (s === 'pass' ? 'ok' : s === 'fail' ? 'err' : 'muted');

export const matrixRows = (r: Pick<ConnectionReport, 'matrix'>): [string, MatrixCell][] => Object.entries(r.matrix ?? {}).sort(([a], [b]) => a.localeCompare(b));
export const cellText = (c: MatrixCell): string => (typeof c.value === 'string' ? c.value : JSON.stringify(c.value));

/** "claude 2.1.0 via headless on windows; short form, took 31.5 s. Tokens used by the test: not reported." */
export function reportMeta(r: ConnectionReport): string {
  return `${r.adapter}${r.cliVersion ? ` ${r.cliVersion}` : ''} via ${r.transport || 'unknown transport'} on ${r.platform}; ${r.form} form, took ${(r.elapsed / 1e9).toFixed(1)} s. Tokens used by the test: ${r.tokensUsed > 0 ? r.tokensUsed : 'not reported'}.`;
}

/** An install's key: the Windows install is the adapter id, one inside a WSL distro has its own (decision P41). */
export const connectionKey = (c: Pick<Connection, 'adapter' | 'key'>): string => c.key || c.adapter;

export const isWSL = (c: Pick<Connection, 'runMode'>): boolean => c.runMode === 'wsl';

/** The CLI's program was found (a CLI blocked by a missing prerequisite is found too: its detail says what it needs). */
export const isInstalled = (c: Pick<Connection, 'installed'>): boolean => c.installed === true;

/** "WSL 2", "WSL 1", or "WSL" when the probe did not say. */
const wslWord = (c: Pick<Connection, 'wsl'>): string => (c.wsl?.wsl2 || c.wsl?.wslVersion === 2 ? 'WSL 2' : c.wsl?.wslVersion === 1 ? 'WSL 1' : 'WSL');

/** Where the CLI runs, in plain words: "on Windows", "inside the WSL 2 distro Ubuntu (the default distro)". */
export function runsText(c: Pick<Connection, 'runMode' | 'distro' | 'wsl'>): string {
  if (!isWSL(c)) return 'on Windows';
  return `inside the ${wslWord(c)} distro ${c.distro ?? ''}${c.wsl?.default ? ' (the default distro)' : ''}`;
}

/** The networking mode and what it means for Drydock's tools. */
export function netModeText(mode: string | undefined): string {
  if (!mode) return 'not reported (WSL uses NAT unless .wslconfig says otherwise)';
  if (mode === 'mirrored') return 'mirrored: the distro shares Windows localhost';
  return `${mode}: the distro cannot reach Windows localhost directly`;
}

/** The line under a row's name: where it runs and the version found ("Windows · 2.1.309", "WSL · 2.1.306"). */
export function agentSub(c: Pick<Connection, 'installed' | 'version' | 'runMode'>): string | undefined {
  const where = isWSL(c) ? 'WSL' : 'Windows';
  if (!isInstalled(c)) return isWSL(c) ? where : undefined;
  return c.version ? `${where} · ${c.version}` : where;
}

/** "Signed in as kyle@example.com (claude.ai)": the account and how, when the CLI says. */
export function signedInLine(c: Pick<Connection, 'account' | 'authDetail'>): string {
  return `Signed in${c.account ? ` as ${c.account}` : ''}${c.authDetail ? ` (${c.authDetail})` : ''}`;
}

/** The Signed in fact: "Yes, as …", "No", "Not reported". */
export function signedInText(c: Pick<Connection, 'authenticated' | 'account' | 'authDetail'>): string {
  if (c.authenticated === true) return c.account ? `Yes, as ${c.account}${c.authDetail ? ` (${c.authDetail})` : ''}` : `Yes${c.authDetail ? ` (${c.authDetail})` : ''}`;
  return c.authenticated === false ? 'No' : 'Not reported';
}

/** The unaccepted notices on a CLI. */
export const openGates = (c: Pick<Connection, 'gates'>): number => (c.gates ?? []).filter((g) => !g.acked).length;

export type StateKind = 'ready' | 'sign-in' | 'notice' | 'untested' | 'test-failed' | 'unavailable' | 'install';

export interface AgentState {
  kind: StateKind;
  /** The list's word: Ready, Sign in, Untested, Unavailable, Install. */
  word: string;
  /** The detail's pill, a few words more. */
  pill: string;
  tone: Tone;
}

/**
 * What stands between this install and the crew, from the core's fields (status, reason, usable, gates, signed in): Ready,
 * Sign in, Accept notice, Untested (a version Drydock has not verified, or output it did not read since the last Test),
 * Test failed (the last Test connection), Unavailable (a version it refuses, a missing prerequisite, a distro without
 * what the run mode needs), Install (not found).
 */
export function agentState(c: Pick<Connection, 'installed' | 'status' | 'usable' | 'reason' | 'gates' | 'authenticated'>): AgentState {
  if (!isInstalled(c)) return { kind: 'install', word: 'Install', pill: 'Not installed', tone: 'muted' };
  if (c.status === 'not_installed') return { kind: 'unavailable', word: 'Unavailable', pill: 'Needs a prerequisite', tone: 'err' };
  if (c.status === 'unsupported_version') return { kind: 'unavailable', word: 'Unavailable', pill: 'Version not supported', tone: 'err' };
  if (c.status === 'unauthenticated' || c.authenticated === false) return { kind: 'sign-in', word: 'Sign in', pill: 'Not signed in', tone: 'warn' };
  const notices = openGates(c);
  if (notices > 0) return { kind: 'notice', word: 'Accept notice', pill: notices === 1 ? 'A notice to accept' : `${notices} notices to accept`, tone: 'warn' };
  const reason = c.reason ?? '';
  if (c.status === 'degraded') {
    if (reason.startsWith('test_failed')) return { kind: 'test-failed', word: 'Test failed', pill: 'Last test failed', tone: 'warn' };
    if (reason.startsWith('version_untested')) return { kind: 'untested', word: 'Untested', pill: 'Version not tested yet', tone: 'warn' };
    if (reason.startsWith('drift')) return { kind: 'untested', word: 'Untested', pill: 'Output changed since its test', tone: 'warn' };
    return { kind: 'unavailable', word: 'Unavailable', pill: reason === 'wsl_setup' ? 'Distro setup incomplete' : 'Unavailable', tone: 'err' };
  }
  if (c.status === 'ready' && c.usable) return { kind: 'ready', word: 'Ready', pill: 'Ready', tone: 'ok' };
  return { kind: 'unavailable', word: 'Unavailable', pill: c.status || 'Unavailable', tone: 'err' };
}

/** An installed CLI that is not usable, or only usable with a doubt (untested, last test failed), needs the person. */
export const needsAttention = (c: Pick<Connection, 'installed' | 'status' | 'usable' | 'reason' | 'gates' | 'authenticated'>): boolean =>
  isInstalled(c) && agentState(c).kind !== 'ready';

/**
 * The installs in the order the page lists them (the core lists adapters by id with the WSL installs last): Claude Code first
 * with its WSL installs right after it, then every other vendor's CLI by name with its WSL installs right after it, and the
 * adapters of custom manifests (a CLI you described) last, in the same way. Within one CLI the Windows install comes before the
 * WSL ones, which keep the core's order (its distro order). Stable: equal keys never swap.
 */
export function orderConnections<T extends Pick<Connection, 'adapter' | 'runMode' | 'custom'>>(list: readonly T[]): T[] {
  const adapters: string[] = [];
  for (const c of list) if (!adapters.includes(c.adapter)) adapters.push(c.adapter);
  const custom = new Set(list.filter((c) => c.custom).map((c) => c.adapter));
  adapters.sort((a, b) => {
    const ra = custom.has(a) ? 2 : a === 'claude' ? 0 : 1;
    const rb = custom.has(b) ? 2 : b === 'claude' ? 0 : 1;
    return ra - rb || adapterLabel(a).localeCompare(adapterLabel(b)) || a.localeCompare(b);
  });
  const out: T[] = [];
  for (const a of adapters) {
    out.push(...list.filter((c) => c.adapter === a && !isWSL(c)), ...list.filter((c) => c.adapter === a && isWSL(c)));
  }
  return out;
}

/** The list's two groups, each in orderConnections' order: the installed CLIs, then the ones not found. */
export function agentGroups<T extends Connection>(list: readonly T[]): { installed: T[]; missing: T[] } {
  const ordered = orderConnections(list);
  return { installed: ordered.filter(isInstalled), missing: ordered.filter((c) => !isInstalled(c)) };
}

/** The install opened when the page opens: the first installed one that needs the person, else the first installed, else the first. */
export function defaultAgent(list: readonly Connection[]): string {
  const { installed, missing } = agentGroups(list);
  const c = installed.find(needsAttention) ?? installed[0] ?? missing[0];
  return c ? connectionKey(c) : '';
}

/** The page's line: "The agent CLIs your crew runs on · 2 of 3 ready" (of the installed ones). */
export function readyLine(list: readonly Connection[]): string {
  const installed = list.filter(isInstalled);
  const ready = installed.filter((c) => agentState(c).kind === 'ready').length;
  return installed.length === 0 ? 'The agent CLIs your crew runs on' : `The agent CLIs your crew runs on · ${ready} of ${installed.length} ready`;
}

/** "checked just now", "checked 14 min ago", "checked 2 d ago": how long ago the install was checked. */
export function checkedWords(at: number, now: number): string {
  return `checked ${agoWords(at, now)}`;
}

/** The line under the detail's name: "On Windows · signed in · checked 2 min ago", "In WSL 2 · not signed in · …". */
export function headerLine(c: Pick<Connection, 'installed' | 'runMode' | 'wsl' | 'authenticated'>, checkedAt: number | undefined, now: number): string {
  const where = isWSL(c) ? `In ${wslWord(c)}` : 'On Windows';
  const parts = [where];
  if (isInstalled(c)) parts.push(c.authenticated === true ? 'signed in' : c.authenticated === false ? 'not signed in' : 'sign-in not reported');
  else parts.push('not found');
  if (checkedAt) parts.push(checkedWords(checkedAt, now));
  return parts.join(' · ');
}

/** What the notice's button does: open a tab, start the sign-in, run a test or check again. */
export type NoticeFix = 'install' | 'sign-in' | 'notice' | 'test' | 'check';

export interface AgentNotice {
  tone: Tone;
  text: string;
  fix?: NoticeFix;
  /** The button's words. */
  label?: string;
  /** The core's problem line the notice says (the detail does not list it again). */
  from?: string;
}

const firstProblem = (c: Pick<Connection, 'problems'>, prefix?: string): string | undefined =>
  (c.problems ?? []).find((p) => (prefix ? p.toLowerCase().includes(prefix) : true));

/**
 * The one notice the detail leads with: the most important thing in the way, in a line, with the action that fixes it (the
 * rest is in the tabs). Undefined for an install with nothing in the way.
 */
export function agentNotice(c: Connection): AgentNotice | undefined {
  const name = adapterLabel(c.adapter);
  const st = agentState(c);
  switch (st.kind) {
    case 'install':
      return { tone: 'muted', text: `${name} was not found ${isWSL(c) ? `inside ${c.distro ?? 'the distro'}` : 'on this PC'}. Install it yourself, then check again.`, fix: 'install', label: 'How to install' };
    case 'unavailable': {
      if (c.status === 'not_installed') {
        const need = (c.prereqs ?? []).find((p) => !p.ok);
        return { tone: 'err', text: need ? `It needs ${prereqNeed(need)}.` : sentence(firstProblem(c) ?? 'A prerequisite is missing.'), fix: 'install', label: 'What it needs', from: need ? undefined : firstProblem(c) };
      }
      if (c.status === 'unsupported_version') {
        const from = firstProblem(c);
        return { tone: 'err', text: sentence(from ?? `${name} ${c.version ?? ''} is not a version Drydock supports.`), fix: 'install', label: 'How to update', from };
      }
      if (c.reason === 'wsl_setup' && c.wsl && !c.wsl.interop) {
        // the core's line says how to turn it on
        const from = firstProblem(c, 'interop');
        return { tone: 'err', text: sentence(from ?? `Windows interop is off in ${c.distro ?? 'the distro'}: Drydock's tools cannot reach an agent there.`), fix: 'check', label: 'Check again', from };
      }
      const from = firstProblem(c);
      return { tone: 'err', text: sentence(from ?? `${name} cannot be used yet.`), fix: 'check', label: 'Check again', from };
    }
    case 'sign-in': {
      const from = firstProblem(c, 'not signed in');
      return c.signIn
        ? { tone: 'warn', text: `Not signed in${isWSL(c) ? ` inside ${c.distro ?? 'the distro'}` : ''}. ${name} uses its own sign-in; Drydock never reads it.`, fix: 'sign-in', label: 'Sign in', from }
        : {
            tone: 'warn',
            text: isWSL(c)
              ? `Not signed in. Open a terminal in the distro (wsl -d ${c.distro ?? ''}) and sign in the way the CLI documents.`
              : 'Not signed in. Sign in the way the CLI documents, then check again.',
            fix: 'check',
            label: 'Check again',
            from,
          };
    }
    case 'notice':
      return { tone: 'warn', text: 'Read and accept its notice before the crew uses it.', fix: 'notice', label: 'Read the notice' };
    case 'untested':
      if ((c.reason ?? '').startsWith('drift')) {
        const from = firstProblem(c, 'does not read');
        return { tone: 'warn', text: sentence(from ?? 'It sent output Drydock does not read.'), fix: 'test', label: 'Test connection', from };
      }
      return {
        tone: 'warn',
        text: `Drydock has not tested ${c.version ? `version ${c.version}` : 'this version'} yet. A test checks that it starts, answers and stops.`,
        fix: 'test',
        label: 'Test connection',
        from: firstProblem(c, 'not been verified'),
      };
    case 'test-failed': {
      const from = firstProblem(c, 'last test');
      return { tone: 'warn', text: sentence(from ?? 'The last test did not pass.'), fix: 'test', label: 'Test again', from };
    }
    default:
      return undefined;
  }
}

/** A sentence starting with a capital and ending with a full stop. */
const sentence = (s: string): string => {
  const t = s.trim();
  if (!t) return t;
  const c = t[0]!.toUpperCase() + t.slice(1);
  return /[.!?]$/.test(c) ? c : `${c}.`;
};

/** "node 22 or newer", "git". */
const prereqNeed = (p: Prereq): string => `${p.tool}${p.min ? ` ${p.min} or newer` : ''}`;

export function prereqText(p: Prereq): string {
  return `${p.tool}${p.min ? ` (needs ${p.min} or newer)` : ''}: ${p.found ? `found${p.version ? ` ${p.version}` : ''}` : 'not found'}`;
}

/** The logo of a WSL distro's Linux: Ubuntu and Debian have their own, every other distro shows the Linux mark. */
export function distroBrand(distro: string | undefined): string {
  const d = (distro ?? '').toLowerCase();
  return d.startsWith('ubuntu') ? 'ubuntu' : d.startsWith('debian') ? 'debian' : 'linux';
}

export type GatewayKind = 'verified' | 'allowed' | 'off' | 'unavailable' | 'unknown';

/**
 * The MCP gateway's setting for a CLI (one per adapter: the Windows and WSL installs share it): verified by real runs and
 * always on, allowed by you though not verified, off until you allow it, not available (with the core's reason), or not
 * reported by the core.
 */
export function gatewayState(row: McpAdapterRow | undefined): { kind: GatewayKind; text: string; canSwitch: boolean; on: boolean } {
  if (!row) return { kind: 'unknown', text: 'The gateway did not report this CLI.', canSwitch: false, on: false };
  if (row.canTry) {
    return row.trying
      ? { kind: 'allowed', text: 'Allowed by you, not verified for this CLI yet.', canSwitch: true, on: true }
      : { kind: 'off', text: 'Off until you allow it: no real run has shown this CLI uses it yet.', canSwitch: true, on: false };
  }
  if (row.attached) return { kind: 'verified', text: 'Verified for this CLI by real runs. Always on.', canSwitch: false, on: true };
  return { kind: 'unavailable', text: row.reason ? `Not available: ${row.reason}` : 'Not available for this CLI.', canSwitch: false, on: false };
}

/**
 * The crew agents that run on this install: the same CLI, and the same place (an agent's own run mode, else its project's).
 * As the core decides it (effectiveRunMode): a project in WSL runs an agent in WSL when its CLI's adapter can run there
 * (`canWSL`, the adapter's declared run modes), else on Windows. A WSL agent without a distro runs in the default one.
 */
export function usedBy<A extends Pick<Agent, 'adapter' | 'runMode' | 'wslDistro' | 'projectId'>>(
  c: Pick<Connection, 'adapter' | 'runMode' | 'distro' | 'wsl'>,
  agents: readonly A[],
  projects: ReadonlyMap<string, Pick<ProjectRunMode, 'runMode' | 'wslDistro'>>,
  all: readonly Pick<Connection, 'adapter' | 'runMode' | 'distro' | 'wsl'>[],
  canWSL: (adapter: string) => boolean,
): A[] {
  const defaultDistro = all.find((x) => x.adapter === c.adapter && isWSL(x) && x.wsl?.default)?.distro;
  const same = (a: string | undefined, b: string | undefined) => (a ?? '').toLowerCase() === (b ?? '').toLowerCase();
  return agents.filter((a) => {
    if (a.adapter !== c.adapter) return false;
    let mode = a.runMode || '';
    let distro = a.wslDistro;
    if (!mode) {
      const p = projects.get(a.projectId);
      mode = p?.runMode === 'wsl' && canWSL(a.adapter) ? 'wsl' : 'native';
      distro = p?.wslDistro;
    }
    if (mode !== 'wsl') return !isWSL(c);
    if (!isWSL(c)) return false;
    return distro ? same(distro, c.distro) : defaultDistro ? same(defaultDistro, c.distro) : true;
  });
}

/** The id rule of a person's own CLI's manifest (internal/agents/manifests: meta.id). */
export const CUSTOM_ID = /^[a-z][a-z0-9-]{0,31}$/;

/** The Add a custom CLI dialog's check of the id before anything is sent ('' when it may be). taken are the ids listed. */
export function customIdProblem(id: string, taken: readonly string[]): string {
  const v = id.trim();
  if (!v) return 'Give your CLI an id.';
  if (!CUSTOM_ID.test(v)) return 'An id is lowercase letters, digits and dashes, starting with a letter, at most 32 characters.';
  if (v === 'custom' || taken.includes(v)) return 'That id is taken: choose another.';
  return '';
}
