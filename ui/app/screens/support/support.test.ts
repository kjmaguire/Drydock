import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { bundleKey, bundleOpts, EVENTS_ERROR, identityText, scrubbedFiles } from './support.ts';

describe('support bundle helpers', () => {
  it('sends the core default for an empty field and a trimmed run id', () => {
    expect(bundleOpts('', '')).toEqual({ ok: true, opts: { eventLimit: undefined, includeRun: undefined } });
    expect(bundleOpts('200', ' run_1 ')).toEqual({ ok: true, opts: { eventLimit: 200, includeRun: 'run_1' } });
    expect(bundleKey('1', ' r ')).toBe(bundleKey('1', 'r'));
    expect(bundleKey('1', 'r')).not.toBe(bundleKey('2', 'r'));
  });

  it('text in the events field that is not a whole number is an error, not the core default (M25)', () => {
    for (const bad of ['lots', '1e3', '5.5', '1,000', '-5', '0', '500 events']) {
      expect(bundleOpts(bad, 'run_1')).toEqual({ ok: false, error: EVENTS_ERROR });
    }
  });

  it('reports only files where something was redacted or replaced', () => {
    const r = {
      files: [
        { name: 'a', secretsRedacted: 0 },
        { name: 'b', secretsRedacted: 1 },
        { name: 'c', secretsRedacted: 0, identityTokens: { host: 1 } },
      ],
      totalSecretsRedacted: 1,
      notice: '',
    };
    expect(scrubbedFiles(r).map((f) => f.name)).toEqual(['b', 'c']);
    expect(scrubbedFiles({ files: null, totalSecretsRedacted: 0, notice: '' })).toEqual([]);
    expect(identityText({ name: 'c', secretsRedacted: 0, identityTokens: { user: 3, host: 1 } })).toBe('user: 3, host: 1');
    expect(identityText({ name: 'a', secretsRedacted: 0 })).toBe('none');
  });
});
