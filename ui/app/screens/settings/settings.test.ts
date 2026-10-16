import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { envLineError, NOT_BUILT, parseEnvNames, THEMES } from './settings.ts';

describe('settings data', () => {
  it('keeps the "Not built yet" list as docs/ui.md section 4 has it', () => {
    expect(NOT_BUILT.map((x) => x.feature)).toEqual([
      'Live verification against vendor accounts (Linear, Notion, Sentry, Aikido)',
      'Cloud sync, accounts, team sharing',
    ]);
    expect(NOT_BUILT.map((x) => x.slice)).toEqual(['Slice 5', 'never']);
    expect(NOT_BUILT[1]!.meanwhile).toBe('Drydock has no cloud component.');
    // the vendors are verified by the person, from the app (P59)
    expect(NOT_BUILT[0]!.meanwhile).toContain('Check against your account');
  });

  it('offers System, Dark and Light', () => {
    expect(THEMES.map((x) => x.value)).toEqual(['system', 'dark', 'light']);
  });

  it('reads variable names one per line, trimmed, without blank lines', () => {
    expect(parseEnvNames('AWS_PROFILE\n\n CLAUDE_CODE_USE_BEDROCK ')).toEqual({ ok: true, names: ['AWS_PROFILE', 'CLAUDE_CODE_USE_BEDROCK'] });
    expect(parseEnvNames('')).toEqual({ ok: true, names: [] });
    expect(parseEnvNames('A\r\n_b1\r\n')).toEqual({ ok: true, names: ['A', '_b1'] });
  });

  it('refuses the first line that is not a name, counted as the box shows its lines, and the refusal never holds the line (L38)', () => {
    expect(parseEnvNames('AWS_PROFILE=work')).toEqual({ ok: false, error: 'Line 1 is not a variable name. Give names only, never values.' });
    // blank lines are counted: the bad line is the fourth on the screen
    expect(parseEnvNames('A\n\n  \nB C\nD')).toEqual({ ok: false, error: envLineError(4) });
    const samples = ['AWS_SECRET_ACCESS_KEY=abc123', 'ghp_aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5 token', '1ABC', 'A-B', 'x'.repeat(65), '"QUOTED"', '=hunter2secret'];
    const wrong = samples.filter((bad) => {
      const r = parseEnvNames(`OK\n${bad}`);
      if (r.ok) return true;
      // the line is named; not a word, number or piece of what it holds is in the message
      return r.error !== 'Line 2 is not a variable name. Give names only, never values.' || bad.split(/[=\s"]+/).some((piece) => piece.length > 2 && r.error.includes(piece));
    });
    expect(wrong).toEqual([]);
  });
});
