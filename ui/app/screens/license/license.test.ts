import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { lic } from '../../test/fixtures.ts';
import { clockNote, daysLabel, daysText, READONLY_OFF, READONLY_USABLE, stateText, stateTone } from './license.ts';

describe('license helpers', () => {
  it('names every state in words and keeps an unknown one as given', () => {
    expect(stateText('none')).toBe('No license');
    expect(stateText('grace')).toBe('Grace period');
    expect(stateText('weird')).toBe('weird');
    expect(stateTone('active')).toBe('ok');
    expect(stateTone('trial')).toBe('ok');
    expect(stateTone('grace')).toBe('warn');
    expect(stateTone('expired')).toBe('err');
  });

  it('words the days row for each state', () => {
    expect(daysLabel(lic({ state: 'expired' }))).toBe('Expired');
    expect(daysLabel(lic({ state: 'grace' }))).toBe('Grace days left');
    expect(daysLabel(lic())).toBe('Days left');
    expect(daysText(lic({ state: 'grace', daysLeft: 5 }))).toBe('5 day(s)');
    expect(daysText(lic({ daysLeft: 40 }))).toContain('40 day(s), until ');
    expect(daysText(lic({ state: 'expired', expiresAt: undefined }))).toBe('expired');
  });

  it('words the clock note as the finding asks: the date, why it counts, and the two ways out (M16)', () => {
    // 1_760_000_000 is 2025-10-09 UTC
    const note = clockNote(lic({ clockSuspect: true, lastSeen: 1_760_000_000 }));
    expect(note).toMatch(/^Drydock last saw .*2025.*, which is later than this computer's clock says now, so the license is judged by that date\. /);
    expect(note).toContain("If the clock is wrong, set it right. If Drydock's record of the date is the mistake, reset the clock check.");
    // not the old claim that the clock was set back: the record can be the mistake
    expect(note).not.toContain('set back');
    // without a date from the core nothing is made up
    const none = clockNote(lic({ clockSuspect: true, lastSeen: undefined }));
    expect(none.startsWith("Drydock has seen a date that is later than this computer's clock says now")).toBe(true);
  });

  it('read-only keeps export, backups and license import usable', () => {
    for (const x of ['Export', 'Backups and restore', 'Support bundle', 'Importing a license']) expect(READONLY_USABLE).toContain(x);
    expect(READONLY_OFF).toContain('Starting tasks and workflows');
  });
});
