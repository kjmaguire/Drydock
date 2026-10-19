// Pure helpers for the License screen: the state words, the days line and what stays usable while read-only.

import type { LicenseView } from '../../api/pkgTypes.ts';
import type { Tone } from '../../ui/tokens.ts';
import { formatDate, formatDateTime, plural } from '../../lib/format.ts';

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

/**
 * What the License page says while Drydock's record of the latest date it has seen is ahead of this computer's clock (audit
 * 2026-10-16, M16): which date, why the license is judged by it, and the two ways out. Not "the clock was set back": the record
 * can be the mistake (a clock that was wrong when it was recorded), and then the clock check is reset, not the clock.
 */
export function clockNote(v: Pick<LicenseView, 'lastSeen'>): string {
  const seen = v.lastSeen ? `Drydock last saw ${formatDate(v.lastSeen * 1000)}, which is` : 'Drydock has seen a date that is';
  return `${seen} later than this computer's clock says now, so the license is judged by that date. If the clock is wrong, set it right. If Drydock's record of the date is the mistake, reset the clock check.`;
}

/** Whether the core's own message already carries the explanation clockNote gives (it adds it while the record is ahead of the clock), so the page says it once. */
export function messageCarriesClockNote(v: Pick<LicenseView, 'message'>): boolean {
  return v.message.includes('reset the clock check');
}

/** The label of the days row: "Expired", "Grace days left" or "Days left". */
export function daysLabel(v: LicenseView): string {
  return v.state === 'expired' ? 'Expired' : v.state === 'grace' ? 'Grace days left' : 'Days left';
}

/** The days row: the expiry time when expired, otherwise the days left (and the date, outside grace). */
export function daysText(v: LicenseView): string {
  const expires = v.expiresAt ? formatDateTime(v.expiresAt * 1000) : '';
  if (v.state === 'expired') return expires || 'expired';
  return `${plural(v.daysLeft, 'day')}${expires && v.state !== 'grace' ? `, until ${expires}` : ''}`;
}
