import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { canOAuth, KINDS, oauthLibraryId, outboxTone, parseParams, riskText, riskTone, syncText, syncTone } from './integrations.ts';

describe('integrations helpers', () => {
  it('risk badge tone follows the score and the text names the reasons', () => {
    expect(riskTone(0)).toBe('ok');
    expect(riskTone(1)).toBe('warn');
    expect(riskTone(2)).toBe('warn');
    expect(riskTone(3)).toBe('err');
    expect(riskText(0, null)).toBe('injection risk 0');
    expect(riskText(4, ['instruction', 'tool-call'])).toBe('injection risk 4: instruction, tool-call');
  });

  it('parameters are key=value lines; a bare key has an empty value', () => {
    expect(parseParams('body=fixed in PR #3\n\n  state = done \nflag')).toEqual({ body: 'fixed in PR #3', state: ' done', flag: '' });
  });

  it('sync results become one sentence', () => {
    expect(syncText({ integrationId: 'i', pages: 1, seen: 3, new: 2, changed: 1, unchanged: 0, gone: 0, complete: true })).toBe('3 seen: 2 new, 1 changed, 0 gone.');
    expect(syncText({ integrationId: 'i', pages: 0, seen: 0, new: 0, changed: 0, unchanged: 0, gone: 0, complete: false, skipped: 'already running' })).toBe('Skipped: already running');
  });

  it('tones, services and the OAuth reference', () => {
    expect(syncTone('ok')).toBe('ok');
    expect(syncTone('unauthenticated')).toBe('err');
    expect(syncTone('idle')).toBe('muted');
    expect(outboxTone('sent')).toBe('ok');
    expect(outboxTone('approved')).toBe('warn');
    expect(outboxTone('declined')).toBe('muted');
    expect(KINDS.map((k) => k.kind)).toEqual(['linear', 'notion', 'sentry', 'aikido']);
    expect(canOAuth('aikido')).toBe(false);
    expect(canOAuth('linear')).toBe(true);
    expect(oauthLibraryId('oauth/mcplib/lin1')).toBe('lin1');
  });
});
