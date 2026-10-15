import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { numOrUndef } from './forms.ts';
import { envNames, NOT_BUILT, THEMES } from './settings.ts';

describe('settings data', () => {
  it('keeps the "Not built yet" list as docs/ui.md section 4 has it', () => {
    expect(NOT_BUILT.map((x) => x.feature)).toEqual([
      'Repo map view',
      'Live verification against vendor accounts (Linear, Notion, Sentry, Aikido)',
      'Cloud sync, accounts, team sharing',
    ]);
    expect(NOT_BUILT.map((x) => x.slice)).toEqual(['gated by decision D5', 'Slice 5', 'never']);
    expect(NOT_BUILT[2]!.meanwhile).toBe('Drydock has no cloud component.');
    // the vendors are verified by the person, from the app (P59)
    expect(NOT_BUILT[1]!.meanwhile).toContain('Check against your account');
  });

  it('offers System, Dark and Light', () => {
    expect(THEMES.map((x) => x.value)).toEqual(['system', 'dark', 'light']);
  });

  it('reads variable names one per line, trimmed, without blank lines', () => {
    expect(envNames('AWS_PROFILE\n\n CLAUDE_CODE_USE_BEDROCK ')).toEqual(['AWS_PROFILE', 'CLAUDE_CODE_USE_BEDROCK']);
    expect(envNames('')).toEqual([]);
  });

  it('parses number fields: empty and junk are unset', () => {
    expect(numOrUndef(' 12 ')).toBe(12);
    expect(numOrUndef('')).toBe(undefined);
    expect(numOrUndef('x')).toBe(undefined);
  });
});
