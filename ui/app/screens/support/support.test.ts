import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { bundleKey, bundleOpts, identityText, scrubbedFiles } from './support.ts';

describe('support bundle helpers', () => {
  it('sends the core default for an empty field and a trimmed run id', () => {
    expect(bundleOpts('', '')).toEqual({ eventLimit: undefined, includeRun: undefined });
    expect(bundleOpts('200', ' run_1 ')).toEqual({ eventLimit: 200, includeRun: 'run_1' });
    expect(bundleOpts('lots', '')).toEqual({ eventLimit: undefined, includeRun: undefined });
    expect(bundleKey('1', ' r ')).toBe(bundleKey('1', 'r'));
    expect(bundleKey('1', 'r')).not.toBe(bundleKey('2', 'r'));
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
