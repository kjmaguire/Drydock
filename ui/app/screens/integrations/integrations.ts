// Pure helpers of the Integrations screen: the services (before the core's catalog arrives), the tones of the status badges,
// the parameter text of an outbound write, the sentence a sync result becomes, and how a connection and an assignment are
// described in a line.

import type { IntegrationAssignment, IntegrationKindInfo, IntegrationSyncResult, IntegrationView } from '../../api/extTypes.ts';
import type { Tone } from '../../ui/tokens.ts';

export interface ServiceKind {
  kind: string;
  label: string;
  blurb: string;
}

/** The services in display order. Their names are the vendors': fixed, never editable. The core's integration.kinds adds the rest. */
export const KINDS: ServiceKind[] = [
  { kind: 'linear', label: 'Linear', blurb: 'Issues and projects' },
  { kind: 'notion', label: 'Notion', blurb: 'Pages as requirements text' },
  { kind: 'sentry', label: 'Sentry', blurb: 'Errors and issues' },
  { kind: 'aikido', label: 'Aikido', blurb: 'Security findings' },
  { kind: 'github', label: 'GitHub', blurb: 'Issues of a repository' },
];

/** The fixed name of a kind. */
export const kindLabel = (kind: string): string => KINDS.find((k) => k.kind === kind)?.label ?? kind;

/** The injection-risk score: 0 is low; 3 and above are never turned into tasks by a rule and cannot be imported. */
export const riskTone = (risk: number): Tone => (risk >= 3 ? 'err' : risk >= 1 ? 'warn' : 'ok');
export const riskText = (risk: number, flags: readonly string[] | null | undefined): string => `injection risk ${risk}${(flags ?? []).length ? `: ${(flags ?? []).join(', ')}` : ''}`;

export const syncTone = (state: string): Tone => (state === 'ok' ? 'ok' : state === 'error' || state === 'unauthenticated' ? 'err' : 'muted');
export const outboxTone = (status: string): Tone => (status === 'sent' ? 'ok' : status === 'awaiting_approval' || status === 'approved' ? 'warn' : 'muted');
export const statusTone = (status: string): Tone => (status === 'ok' ? 'ok' : status === 'untested' ? 'muted' : 'warn');

/** key=value per line; a line without = is a key with an empty value. */
export function parseParams(text: string): Record<string, string> {
  return Object.fromEntries(
    text
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean)
      .map((l) => {
        const i = l.indexOf('=');
        return i < 0 ? [l, ''] : [l.slice(0, i).trim(), l.slice(i + 1)];
      }),
  );
}

export const syncText = (r: IntegrationSyncResult): string => (r.skipped ? `Skipped: ${r.skipped}` : `${r.seen} seen: ${r.new} new, ${r.changed} changed, ${r.gone} gone.`);

/** One line about what a connection reaches: "Kyle in Acme", or what is missing. */
export function identityLine(c: IntegrationView | undefined): string {
  if (!c) return 'Not connected';
  const id = c.identity ?? {};
  const who = [id.account, id.workspace].filter((x) => x && x.trim()).join(' in ');
  if (who) return `Connected: ${who}`;
  return c.status === 'ok' ? 'Connected' : c.status === 'untested' ? 'Connected, not checked yet' : `Connected (${c.status})`;
}

/** An assignment's scope in words, using the setting labels: "Team: ENG · Linear project: Launch". */
export function scopeSummary(info: IntegrationKindInfo | undefined, a: Pick<IntegrationAssignment, 'config'>): string {
  const cfg = a.config ?? {};
  const parts = (info?.settings ?? [])
    .filter((s) => cfg[s.key])
    .map((s) => `${s.label}: ${cfg[s.key]}`);
  for (const [k, v] of Object.entries(cfg)) {
    if (v && !(info?.settings ?? []).some((s) => s.key === k)) parts.push(`${k}: ${v}`);
  }
  return parts.length ? parts.join(' · ') : 'Everything the connection can see';
}

/** The connection of a kind (one per kind in a workspace), if any. */
export function tileState(conns: IntegrationView[], kind: string): IntegrationView | undefined {
  return conns.find((c) => c.kind === kind);
}
