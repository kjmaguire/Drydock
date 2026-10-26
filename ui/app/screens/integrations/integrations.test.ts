import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { IntegrationKindInfo, IntegrationView } from '../../api/extTypes.ts';
import type { McpGalleryCard } from '../../api/mcpClient.ts';
import { galleryCard, libServer, pay, projServer, rev } from '../mcp/fixtures.ts';
import {
  providerRowState,
  cardsState,
  connectReply,
  filterCounts,
  identityLine,
  isTestReport,
  KINDS,
  kindLabel,
  methodText,
  outboxTone,
  ownServers,
  ownServerState,
  ownServerSub,
  parseParams,
  projectConnState,
  projectServerState,
  riskText,
  riskTone,
  scopeSummary,
  serverBlurb,
  serverIdOf,
  serverKey,
  serviceState,
  showsUnder,
  statusTone,
  syncLine,
  syncText,
  syncTone,
  tileState,
  toolsFact,
  verifiedText,
  vendors,
  vendorState,
  vendorSub,
} from './integrations.ts';

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

  it("a row's state is one word: On, Set up, or what needs the person, the most pressing first", () => {
    const rec = { at: Date.UTC(2026, 9, 15, 12), ok: true, summary: 'ok', calls: 3, failedCalls: 0, items: 3, mismatches: 0 };
    expect(serviceState(undefined)).toEqual({ text: 'Set up', tone: 'none', bucket: 'setup' });
    expect(serviceState(view())).toEqual({ text: 'On', tone: 'ok', bucket: 'on' });
    expect(serviceState(view({ status: 'untested' }))).toEqual({ text: 'On', tone: 'ok', bucket: 'on' });
    expect(serviceState(view({ verified: rec }))).toEqual({ text: 'On', tone: 'ok', bucket: 'on' });
    expect(serviceState(view({ verified: { ...rec, ok: false } }))).toEqual({ text: 'Check failed', tone: 'err', bucket: 'needs' });
    // a connection that is not working needs the person, whatever an older check said
    expect(serviceState(view({ status: 'unauthenticated', verified: rec }))).toEqual({ text: 'Key refused', tone: 'warn', bucket: 'needs' });
    expect(serviceState(view({ status: 'unreachable' })).text).toBe('Unreachable');
    expect(serviceState(view({ status: 'something new' })).text).toBe('Needs you');
    // one project's import failing is enough
    const failing = { id: 'a', integrationId: 'int_1', projectId: 'p', config: {}, enabled: true, createdBy: '', created: 0, updated: 0, version: 1, sync: { ...view().sync, state: 'forbidden' } };
    expect(serviceState(view({ assignments: [failing] }))).toEqual({ text: 'Sync failing', tone: 'err', bucket: 'needs' });

    expect(cardsState([])).toEqual({ text: 'Set up', tone: 'none', bucket: 'setup' });
    expect(cardsState([galleryCard({ status: 'connected' })]).text).toBe('On');
    expect(cardsState([galleryCard({ status: 'connected' }), galleryCard({ id: 'b', status: 'needs_sign_in' })])).toEqual({ text: 'Needs sign-in', tone: 'warn', bucket: 'needs' });
    expect(cardsState([galleryCard({ status: 'needs_sign_in' }), galleryCard({ id: 'b', status: 'error' })]).text).toBe('Error');
    expect(cardsState([galleryCard({ status: 'needs_token' })]).text).toBe('Needs a token');

    // a vendor: what needs the person on either channel (app sync first), else On when either works
    const lin = { kind: KINDS[0], cards: [galleryCard({ id: 'linear', status: 'not_connected' })] };
    expect(vendorState(lin, undefined).text).toBe('Set up');
    expect(vendorState(lin, view()).text).toBe('On');
    expect(vendorState({ ...lin, cards: [galleryCard({ status: 'connected' })] }, undefined).text).toBe('On');
    expect(vendorState({ ...lin, cards: [galleryCard({ status: 'needs_token' })] }, view()).text).toBe('Needs a token');
    expect(vendorState({ ...lin, cards: [galleryCard({ status: 'needs_token' })] }, view({ status: 'unauthenticated' })).text).toBe('Key refused');
    expect(vendorState({ cards: [galleryCard({ status: 'connected' })] }, undefined).text).toBe('On');

    // a server of your own
    const own = libServer();
    expect(ownServerState(own)).toEqual({ text: 'On', tone: 'ok', bucket: 'on' });
    expect(ownServerState({ ...own, tools: [] })).toEqual({ text: 'No tools yet', tone: 'warn', bucket: 'needs' });
    expect(ownServerState({ ...own, oauth: true, oauthAt: 0 }).text).toBe('Not signed in');
    expect(ownServerState({ ...own, oauth: true, oauthAt: 0, lastError: 'refused' }).text).toBe('Could not connect');

    // the filters count them
    const states = [serviceState(undefined), serviceState(view()), cardsState([galleryCard({ status: 'error' })]), ownServerState(own)];
    expect(filterCounts(states)).toEqual({ all: 4, on: 2, needs: 1 });
    expect(states.filter((x) => showsUnder('on', x)).length).toBe(2);
    expect(states.filter((x) => showsUnder('needs', x)).length).toBe(1);
    expect(states.filter((x) => showsUnder('all', x)).length).toBe(4);
  });

  it('the line under a row: what it does and one fact; the line under a connected service: when it synced and how it signs in', () => {
    const lin = { kind: KINDS[0], blurb: 'Issues and projects', cards: [] };
    const asg = (id: string, lastSuccessAt: number) => ({ id, integrationId: 'int_1', projectId: id, config: {}, enabled: true, createdBy: '', created: 0, updated: 0, version: 1, sync: { ...view().sync, lastSuccessAt } });
    expect(vendorSub(lin, undefined)).toBe('Issues and projects');
    expect(vendorSub(lin, view({ assignments: [asg('a', 0), asg('b', 0)] }))).toBe('Issues and projects · 2 projects');
    expect(vendorSub({ blurb: 'Tools for agents', cards: [galleryCard(), galleryCard({ id: 'b', tools: [{ name: 'x', readOnlyHint: false }, { name: 'y', readOnlyHint: false }] })] }, undefined)).toBe('Tools for agents · 3 tools');
    expect(ownServerSub(libServer())).toBe('Program on this PC · 2 tools');
    expect(ownServerSub(libServer({ transport: 'http', tools: [] }))).toBe('Web address');
    expect(toolsFact([galleryCard()])).toBe('1 tool, 1 read-only');
    expect(toolsFact([galleryCard({ readOnlyTools: [] })])).toBe('1 tool');
    expect(toolsFact([galleryCard({ tools: [] })])).toBe('');

    const now = Date.UTC(2026, 9, 24, 12);
    expect(syncLine(view({ identity: { method: 'Personal API key' }, assignments: [asg('a', now - 4 * 60_000), asg('b', now - 3_600_000)] }), now)).toBe('Synced 4 min ago · personal API key');
    expect(syncLine(view({ identity: {}, assignments: [] }), now)).toBe('Not assigned to a project yet · API key');
    expect(syncLine(view({ identity: {}, assignments: [asg('a', 0)] }), now)).toBe('Not synced yet · API key');
    expect(methodText({ kind: 'github', authKind: 'gh', identity: { method: 'GitHub CLI' } })).toBe('your gh sign-in');
    expect(methodText({ kind: 'sentry', authKind: 'oauth', identity: {} })).toBe('sign-in');
  });

  it('keys in the address: a vendor by its own key, one of your servers after "server-"; your servers are the ones no card stands for', () => {
    expect(serverKey('pg')).toBe('server-pg');
    expect(serverIdOf('server-pg')).toBe('pg');
    expect(serverIdOf('linear')).toBe('');
    const servers = [libServer({ id: 'mine' }), libServer({ id: 'mslearn', catalogId: 'mslearn' }), libServer({ id: 'ctx7-2', catalogId: 'ctx7' }), libServer({ id: 'gone', catalogId: 'retired' })];
    const cards = [galleryCard({ id: 'mslearn', libraryId: 'mslearn' }), galleryCard({ id: 'ctx7' })];
    // a server connected from a card is the card's vendor; one whose catalog entry left this build is listed as yours
    expect(ownServers(servers, cards).map((x) => x.id)).toEqual(['mine', 'gone']);
    expect(verifiedText(view())).toContain('Not checked against your account yet');
  });

  it('a service as one project has it: its scope and a badge, or not assigned', () => {
    const kinds = [{ kind: 'linear', settings: [{ key: 'team', label: 'Team' }] }] as unknown as IntegrationKindInfo[];
    const a = { id: 'asg_1', integrationId: 'int_1', projectId: 'prj_1', config: { team: 'ENG' }, enabled: true, createdBy: '', created: 0, updated: 0, version: 1,
      sync: { state: 'ok', lastSuccessAt: 0, lastAttemptAt: 0, itemsSeen: 0, itemsLastSync: 0, consecutiveFailures: 0, resuming: false } };
    expect(projectConnState(kinds[0], view({ assignments: [a] }), 'prj_2')).toEqual({ text: 'Not assigned to this project', tone: '', badge: { text: 'not assigned', tone: 'muted' } });
    expect(projectConnState(kinds[0], view({ assignments: [a] }), 'prj_1')).toEqual({ text: 'Team: ENG', tone: 'ok', badge: { text: 'assigned', tone: 'ok' } });
    const failing = { ...a, sync: { ...a.sync, state: 'forbidden' } };
    expect(projectConnState(kinds[0], view({ assignments: [failing] }), 'prj_1')).toEqual({ text: 'Team: ENG · sync: forbidden', tone: 'bad', badge: { text: 'sync failing', tone: 'err' } });
    expect(projectConnState(kinds[0], view({ status: 'unreachable', assignments: [a] }), 'prj_1').badge).toEqual({ text: 'needs attention', tone: 'warn' });
  });

  it("says on a server's tile what it is, which of the project's agents may use it, and whether it is on here", () => {
    expect(serverBlurb(libServer())).toBe('your own · 2 tools');
    expect(serverBlurb(libServer({ catalogId: 'ctx7', tools: [] }))).toBe('built into Drydock · tools not found yet');
    expect(serverBlurb(libServer({ tools: [{ name: 'one', readOnlyHint: true }] }))).toBe('your own · 1 tool');
    const agents = [pay, rev, { id: 'agt_3', name: 'Ops' }, { id: 'agt_4', name: 'Docs' }];
    expect(projectServerState(undefined, agents)).toEqual({ text: 'Not in this project', tone: '', badge: { text: 'not added', tone: 'muted' } });
    expect(projectServerState(projServer(), agents)).toEqual({ text: 'No agent may use it yet', tone: '', badge: { text: 'on', tone: 'ok' } });
    const grant = (...ids: string[]) => ids.map((agentId) => ({ agentId, tools: ['*'] }));
    expect(projectServerState(projServer({ agents: grant('agt_1') }), agents).text).toBe('Pay may use it');
    expect(projectServerState(projServer({ agents: grant('agt_2', 'agt_1') }), agents)).toEqual({ text: 'Pay and Rev may use it', tone: 'ok', badge: { text: 'on', tone: 'ok' } });
    expect(projectServerState(projServer({ agents: grant('agt_1', 'agt_2', 'agt_3', 'agt_4') }), agents).text).toBe('Pay, Rev and 2 more may use it');
    // a grant of an agent that left the project names nobody
    expect(projectServerState(projServer({ agents: grant('agt_9') }), agents).text).toBe('No agent may use it yet');
    expect(projectServerState(projServer({ enabled: false, agents: grant('agt_1') }), agents)).toEqual({ text: 'Pay may use it', tone: '', badge: { text: 'off', tone: 'muted' } });
  });
});

describe('the answer of integration.connect is checked (L43)', () => {
  const report = { integrationId: 'int_1', ok: true, steps: [{ id: 'auth', name: 'Sign in', ok: true, detail: 'ok' }], summary: 'passed', at: 1 };

  it('knows a test report by what the report view reads', () => {
    expect(isTestReport(report)).toBe(true);
    expect(isTestReport({ ...report, steps: null })).toBe(true); // Go's nil slice
    expect(isTestReport({ ok: false, summary: 'no' })).toBe(true);
    expect(isTestReport({ ...report, steps: [{ id: 'a', name: 'n', ok: true }] })).toBe(true);
    for (const bad of [null, undefined, 'report', 7, [], {}, { ...report, ok: 'yes' }, { ...report, summary: 3 }, { ...report, steps: 'none' }, { ...report, steps: {} }, { ...report, steps: [null] }, { ...report, steps: [{ id: 'a', name: 'n', ok: 'y' }] }]) {
      expect([bad, isTestReport(bad)]).toEqual([bad, false]);
    }
  });

  it('takes a connection with or without a report', () => {
    const c = view();
    expect(connectReply({ connection: c })).toEqual({ connection: c });
    expect(connectReply({ connection: c, test: null })).toEqual({ connection: c });
    expect(connectReply({ connection: c, test: report })).toEqual({ connection: c, test: report });
  });

  it('says plainly what is wrong with a reply of another shape', () => {
    for (const bad of [null, undefined, 'ok', 5, [], {}, { connection: 'x' }, { connection: null }]) expect(() => connectReply(bad)).toThrow(/no connection in it/);
    for (const test of ['passed', 1, [], { ok: true }, { ok: 'yes', summary: 's' }, { ok: true, summary: 's', steps: 'x' }]) {
      expect(() => connectReply({ connection: view(), test })).toThrow(/test report in a shape this page can not show/);
    }
  });
});

describe('providerRowState (OpenRouter on the Integrations page, decision P103)', () => {
  it('is Set up without a provider, On while one is enabled and answers, Down when every enabled one is down, Disabled when all are off', () => {
    expect(providerRowState([])).toEqual({ text: 'Set up', tone: 'none', bucket: 'setup' });
    expect(providerRowState([{ enabled: true, status: 'up' }]).bucket).toBe('on');
    expect(providerRowState([{ enabled: true, status: 'unknown' }]).bucket).toBe('on');
    expect(providerRowState([{ enabled: true, status: 'down' }, { enabled: true, status: 'up' }]).bucket).toBe('on');
    expect(providerRowState([{ enabled: true, status: 'down' }])).toEqual({ text: 'Down', tone: 'err', bucket: 'needs' });
    expect(providerRowState([{ enabled: false, status: 'up' }])).toEqual({ text: 'Disabled', tone: 'muted', bucket: 'setup' });
  });
});
