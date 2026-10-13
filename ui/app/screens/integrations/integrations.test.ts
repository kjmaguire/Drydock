import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { IntegrationKindInfo, IntegrationView } from '../../api/extTypes.ts';
import type { McpGalleryCard } from '../../api/mcpClient.ts';
import { galleryCard } from '../mcp/fixtures.ts';
import { agentsStateText, identityLine, KINDS, kindLabel, outboxTone, parseParams, riskText, riskTone, scopeSummary, statusTone, syncStateText, syncText, syncTone, tileState, vendors, vendorStateText, vendorTone } from './integrations.ts';

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
    expect(syncTone('forbidden')).toBe('err');
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

  it('vendors: every integration kind first (with the gallery cards of its brand), then each MCP-only vendor, a group on one tile', () => {
    const cards: McpGalleryCard[] = [
      galleryCard({ id: 'mslearn', skillRegistries: ['microsoft-skills', 'msdocs-mcp-skills'] }),
      galleryCard({ id: 'cfdocs', display: 'Cloudflare Documentation', brand: 'cloudflare', group: 'Cloudflare', skillRegistries: ['cloudflare-skills'] }),
      galleryCard({ id: 'gh', display: 'GitHub', brand: 'github', integration: 'github' }),
      galleryCard({ id: 'cfapi', display: 'Cloudflare API', brand: 'cloudflare', group: 'Cloudflare', skillRegistries: ['cloudflare-skills'] }),
      galleryCard({ id: 'ctx7', display: 'Context7', brand: 'context7' }),
      galleryCard({ id: 'linear', display: 'Linear', brand: 'linear', integration: 'linear' }),
    ];
    const vs = vendors(KINDS, cards);
    expect(vs.map((v) => v.key)).toEqual(['linear', 'notion', 'sentry', 'aikido', 'github', 'mslearn', 'cloudflare', 'ctx7']);
    expect(vs.map((v) => v.cards.map((c) => c.id))).toEqual([['linear'], [], [], [], ['gh'], ['mslearn'], ['cfdocs', 'cfapi'], ['ctx7']]);
    expect(vs[6]).toMatchObject({ label: 'Cloudflare', logo: 'cloudflare', blurb: 'Documentation, API', skillRegistries: ['cloudflare-skills'] });
    expect(vs[6]!.kind).toBeUndefined();
    expect(vs[5]!.skillRegistries).toEqual(['microsoft-skills', 'msdocs-mcp-skills']);
    expect(vs[0]!.kind).toBe(KINDS[0]);
    expect(vs[7]!.blurb).toBe('Tools for agents');
  });

  it('the state line names both channels where the vendor has them', () => {
    const conn = view({ identity: { account: 'kyle' } });
    const lin = { kind: KINDS[0], cards: [galleryCard({ id: 'linear', status: 'connected' })] };
    expect(vendorStateText(lin, conn)).toBe('Sync: connected as kyle · Agents: connected');
    expect(vendorStateText({ kind: KINDS[0], cards: [galleryCard({ status: 'needs_token' })] }, undefined)).toBe('Sync: not connected · Agents: needs a token');
    expect(vendorStateText({ kind: KINDS[3], cards: [] }, undefined)).toBe('Sync: not connected');
    expect(vendorStateText({ cards: [galleryCard({ status: 'connected' })] }, undefined)).toBe('Agents: connected');
    expect(vendorStateText({ cards: [galleryCard({ status: 'connected' }), galleryCard({ id: 'b', status: 'needs_sign_in' })] }, undefined)).toBe('Agents: 1 of 2 connected, 1 needs sign-in');
    expect(agentsStateText([galleryCard({ status: 'error' }), galleryCard({ id: 'b' })])).toBe('error');
    expect(agentsStateText([])).toBe('');
    expect(syncStateText(view({ identity: {} }))).toBe('connected');
    expect(syncStateText(view({ status: 'untested', identity: {} }))).toBe('connected, not checked yet');
    expect(syncStateText(view({ status: 'unauthorized', identity: {} }))).toBe('connected (unauthorized)');
    // tone: warn when something needs the human, ok when something is connected
    expect(vendorTone(lin, conn)).toBe('ok');
    expect(vendorTone({ kind: KINDS[0], cards: [galleryCard({ status: 'needs_token' })] }, conn)).toBe('warn');
    expect(vendorTone({ kind: KINDS[0], cards: [] }, undefined)).toBe('');
    expect(vendorTone({ cards: [galleryCard({ status: 'connected' })] }, undefined)).toBe('ok');
  });
});
