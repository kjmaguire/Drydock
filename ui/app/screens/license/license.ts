// Pure helpers for the License screen: the state words, the days line and what stays usable while read-only.

import type { LicenseView } from '../../api/pkgTypes.ts';
import type { Tone } from '../../ui/tokens.ts';
import { formatDateTime } from '../../lib/format.ts';

export const STATE_TEXT: Record<string, string> = {
  none: 'No license',
  trial: 'Trial',
  active: 'Active',
  grace: 'Grace period',
  expired: 'Expired',
};

/** What stays usable when the app is read-only, and what is turned off. Mirrors the core's own refusal text. */
export const READONLY_USABLE = ['Reading everything', 'Export', 'Backups and restore', 'Support bundle', 'Importing a license'];
export const READONLY_OFF = [
  'Starting tasks and workflows',
  'Approving agent actions',
  'Creating projects and agents',
  'Adding providers and connections',
  'Agent replies in rooms',
  'Merging and pull requests',
];

export function stateText(state: string): string {
  return STATE_TEXT[state] ?? state;
}

export function stateTone(state: string): Tone {
  return state === 'active' || state === 'trial' ? 'ok' : state === 'grace' ? 'warn' : 'err';
}

/** The label of the days row: "Expired", "Grace days left" or "Days left". */
export function daysLabel(v: LicenseView): string {
  return v.state === 'expired' ? 'Expired' : v.state === 'grace' ? 'Grace days left' : 'Days left';
}

/** The days row: the expiry time when expired, otherwise the days left (and the date, outside grace). */
export function daysText(v: LicenseView): string {
  const expires = v.expiresAt ? formatDateTime(v.expiresAt * 1000) : '';
  if (v.state === 'expired') return expires || 'expired';
  return `${v.daysLeft} day(s)${expires && v.state !== 'grace' ? `, until ${expires}` : ''}`;
}
