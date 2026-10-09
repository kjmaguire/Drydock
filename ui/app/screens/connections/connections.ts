// Pure helpers of the Connections screen: badge tones, the capability matrix in order, and the plain sentences of a report.

import type { Connection, ConnectionReport, MatrixCell, Prereq } from '../../api/types.ts';
import type { Tone } from '../../ui/tokens.ts';

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

export function prereqText(p: Prereq): string {
  return `${p.tool}${p.min ? ` (needs ${p.min} or newer)` : ''}: ${p.found ? `found${p.version ? ` ${p.version}` : ''}` : 'not found'}`;
}
