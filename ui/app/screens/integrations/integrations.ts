// Pure helpers of the Integrations screen: the services Drydock can read, the tones of the status badges, the parameter
// text of an outbound write and the sentence a sync result becomes.

import type { IntegrationSyncResult } from '../../api/extTypes.ts';
import type { Tone } from '../../ui/tokens.ts';

export interface ServiceKind {
  kind: string;
  label: string;
  blurb: string;
  config: { key: string; label: string }[];
}

export const KINDS: ServiceKind[] = [
  { kind: 'linear', label: 'Linear', blurb: 'Issues and projects', config: [{ key: 'team', label: 'Team key (optional)' }] },
  { kind: 'notion', label: 'Notion', blurb: 'Pages as requirements text', config: [{ key: 'query', label: 'Search query (optional)' }] },
  {
    kind: 'sentry',
    label: 'Sentry',
    blurb: 'Errors and issues',
    config: [
      { key: 'organization', label: 'Organization' },
      { key: 'project', label: 'Project' },
      { key: 'query', label: 'Query (optional)' },
    ],
  },
  {
    kind: 'aikido',
    label: 'Aikido',
    blurb: 'Security findings',
    config: [
      { key: 'workspace', label: 'Workspace (optional)' },
      { key: 'repo', label: 'Repository (optional)' },
    ],
  },
];

/** Aikido has no OAuth sign-in; the others can sign in instead of a pasted token. */
export const canOAuth = (kind: string): boolean => kind !== 'aikido';

/** The injection-risk score: 0 is low; 3 and above are never turned into tasks by a rule and cannot be imported. */
export const riskTone = (risk: number): Tone => (risk >= 3 ? 'err' : risk >= 1 ? 'warn' : 'ok');
export const riskText = (risk: number, flags: readonly string[] | null | undefined): string => `injection risk ${risk}${(flags ?? []).length ? `: ${(flags ?? []).join(', ')}` : ''}`;

export const syncTone = (state: string): Tone => (state === 'ok' ? 'ok' : state === 'error' || state === 'unauthenticated' ? 'err' : 'muted');
export const outboxTone = (status: string): Tone => (status === 'sent' ? 'ok' : status === 'awaiting_approval' || status === 'approved' ? 'warn' : 'muted');

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

/** The library id an integration OAuth reference points at ("oauth/mcplib/<id>"). */
export const oauthLibraryId = (ref: string): string => ref.replace('oauth/mcplib/', '');
