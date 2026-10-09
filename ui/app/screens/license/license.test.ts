import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { lic } from '../../test/fixtures.ts';
import { daysLabel, daysText, READONLY_OFF, READONLY_USABLE, stateText, stateTone } from './license.ts';

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

  it('read-only keeps export, backups and license import usable', () => {
    for (const x of ['Export', 'Backups and restore', 'Support bundle', 'Importing a license']) expect(READONLY_USABLE).toContain(x);
    expect(READONLY_OFF).toContain('Starting tasks and workflows');
  });
});
