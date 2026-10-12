import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { IntegrationKindInfo, IntegrationView } from '../../api/extTypes.ts';
import { identityLine, KINDS, kindLabel, outboxTone, parseParams, riskText, riskTone, scopeSummary, statusTone, syncText, syncTone, tileState } from './integrations.ts';

const view = (over: Partial<IntegrationView> = {}): IntegrationView =>
  ({
    id: 'int_1',
    kind: 'linear',
    name: 'Linear',
    projectId: '',
    status: 'ok',
    statusDetail: '',
    authKind: 'api_key',
    config: {},
    pollIntervalS: 0,
    enabled: true,
    created: 0,
    updated: 0,
    version: 1,
    hasCredential: true,
    sync: { state: 'ok', lastSuccessAt: 0, lastAttemptAt: 0, itemsSeen: 0, itemsLastSync: 0, consecutiveFailures: 0, resuming: false },
    tools: { syncTools: [], ops: [] },
    ...over,
  }) as IntegrationView;

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
    expect(syncText({ integrationId: 'i', pages: 0, seen: 0, new: 0, changed: 0, unchanged: 0, gone: 0, complete: false, skipped: 'not assigned to a project' })).toBe(
      'Skipped: not assigned to a project',
    );
  });

  it('the five services, in a fixed order with fixed names, and the tones', () => {
    expect(KINDS.map((k) => k.kind)).toEqual(['linear', 'notion', 'sentry', 'aikido', 'github']);
    expect(kindLabel('github')).toBe('GitHub');
    expect(syncTone('ok')).toBe('ok');
    expect(syncTone('unauthenticated')).toBe('err');
    expect(syncTone('idle')).toBe('muted');
    expect(outboxTone('sent')).toBe('ok');
    expect(outboxTone('approved')).toBe('warn');
    expect(outboxTone('declined')).toBe('muted');
    expect(statusTone('ok')).toBe('ok');
    expect(statusTone('untested')).toBe('muted');
    expect(statusTone('unauthenticated')).toBe('warn');
  });

  it('a connection is described by what its API reported', () => {
    expect(identityLine(undefined)).toBe('Not connected');
    expect(identityLine(view({ identity: { account: 'Kyle', workspace: 'Acme' } }))).toBe('Connected: Kyle in Acme');
    expect(identityLine(view({ identity: { workspace: 'Acme Security' } }))).toBe('Connected: Acme Security');
    expect(identityLine(view({ status: 'untested' }))).toBe('Connected, not checked yet');
    expect(identityLine(view({ status: 'unauthenticated' }))).toBe('Connected (unauthenticated)');
    expect(tileState([view()], 'linear')?.id).toBe('int_1');
    expect(tileState([view()], 'notion')).toBeUndefined();
  });

  it('an assignment scope reads with the setting labels', () => {
    const info = { settings: [{ key: 'team', label: 'Team' }, { key: 'project', label: 'Linear project' }] } as unknown as IntegrationKindInfo;
    expect(scopeSummary(info, { config: { team: 'ENG', project: 'Launch' } })).toBe('Team: ENG · Linear project: Launch');
    expect(scopeSummary(info, { config: {} })).toBe('Everything the connection can see');
    expect(scopeSummary(undefined, { config: { repo: 'acme/web' } })).toBe('repo: acme/web');
  });
});
