import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { numberText, parseDollars, parseInteger, parsePercent, parseWhole } from './limits.ts';

const MSG = 'say it plainly';

describe('reading a limit field strictly (M25)', () => {
  it('an empty field is "not set", whatever blank it holds', () => {
    for (const blank of ['', '   ', '\t', '\n', ' ']) {
      expect(parseDollars(blank, MSG)).toEqual({ ok: true, value: undefined });
      expect(parseWhole(blank, 1, MSG)).toEqual({ ok: true, value: undefined });
      expect(parseInteger(blank, MSG)).toEqual({ ok: true, value: undefined });
      expect(parsePercent(blank, MSG)).toEqual({ ok: true, value: undefined });
    }
  });

  it('dollars are digits with an optional decimal point, greater than zero', () => {
    for (const [text, value] of [
      ['12', 12],
      ['12.5', 12.5],
      [' 12.50 ', 12.5],
      ['0.01', 0.01],
      ['.5', 0.5],
      ['5.', 5],
      ['007', 7],
      ['1000000', 1_000_000],
    ] as const) {
      expect(parseDollars(text, MSG)).toEqual({ ok: true, value });
    }
  });

  it('refuses every text that used to become "no limit" (5 dollars, $5, 1,000, 1e999, abc), with the field\'s own message', () => {
    for (const text of ['5 dollars', '$5', '1,000', '1e999', '1e3', 'abc', '0', '0.0', '.', '-5', '5-', '+5', '1.2.3', '5,5', '0x10', 'Infinity', 'NaN', '٣', '５', '5 5', '12.50 USD', `${'9'.repeat(400)}`]) {
      expect(parseDollars(text, MSG)).toEqual({ ok: false, error: MSG });
    }
  });

  it('whole numbers are digits only and at least the field\'s minimum', () => {
    expect(parseWhole('7', 1, MSG)).toEqual({ ok: true, value: 7 });
    expect(parseWhole(' 200000 ', 1, MSG)).toEqual({ ok: true, value: 200000 });
    expect(parseWhole('0', 0, MSG)).toEqual({ ok: true, value: 0 });
    expect(parseWhole('007', 1, MSG)).toEqual({ ok: true, value: 7 });
    for (const text of ['7.5', '7.', '1,000', '1e3', '1_000', '-1', '+7', 'x', '7 tokens', '0x10', '٣', '99999999999999999999']) {
      expect(parseWhole(text, 1, MSG)).toEqual({ ok: false, error: MSG });
    }
    expect(parseWhole('0', 1, MSG)).toEqual({ ok: false, error: MSG });
    expect(parseWhole('2', 3, MSG)).toEqual({ ok: false, error: MSG });
  });

  it('an integer field may be negative, and nothing else changes', () => {
    expect(parseInteger('-3', MSG)).toEqual({ ok: true, value: -3 });
    expect(parseInteger('0', MSG)).toEqual({ ok: true, value: 0 });
    expect(parseInteger(' 12 ', MSG)).toEqual({ ok: true, value: 12 });
    for (const text of ['--3', '3-', '+3', '3.5', '1e3', 'abc', '1,000', '99999999999999999999']) expect(parseInteger(text, MSG)).toEqual({ ok: false, error: MSG });
  });

  it('a warn percentage is a whole number from 1 to 99', () => {
    expect(parsePercent('1', MSG)).toEqual({ ok: true, value: 1 });
    expect(parsePercent(' 80 ', MSG)).toEqual({ ok: true, value: 80 });
    expect(parsePercent('99', MSG)).toEqual({ ok: true, value: 99 });
    for (const text of ['0', '100', '150', '80%', '8.5', '-5', 'abc']) expect(parsePercent(text, MSG)).toEqual({ ok: false, error: MSG });
  });

  it('shows a stored number as the plain digits a field takes back, never as an exponent', () => {
    expect(numberText(5)).toBe('5');
    expect(numberText(12.5)).toBe('12.5');
    expect(numberText(0.1 + 0.2)).toBe('0.30000000000000004');
    expect(numberText(1e-7)).toBe('0.0000001');
    expect(numberText(1e21)).toBe('1000000000000000000000');
    // whatever it prints, the strict reader takes it back as the same number
    for (const n of [5, 12.5, 1e-7, 1e21, 0.000123]) expect(parseDollars(numberText(n), MSG)).toEqual({ ok: true, value: n });
  });
});
