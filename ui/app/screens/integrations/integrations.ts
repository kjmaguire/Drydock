// Pure helpers of the Integrations screen: the services (before the core's catalog arrives), the vendors the page lists (an
// integration kind, the MCP connector cards of the same brand, or both), the state of each row in a word (On, Set up, or what
// needs the person) and the filters that count them, the tones of the status badges, the parameter text of an outbound write,
// the sentence a sync result becomes, and how a connection and an assignment are described in a line.

import { isRecord } from '../../api/guards.ts';
import type { IntegrationAssignment, IntegrationKindInfo, IntegrationSyncResult, IntegrationTestReport, IntegrationView } from '../../api/extTypes.ts';
import type { McpGalleryCard, McpLibraryServer, McpServer } from '../../api/mcpClient.ts';
import type { Agent, Provider } from '../../api/types.ts';
import { agoWords, formatDate, plural } from '../../lib/format.ts';
import type { TileBadge, TileTone } from '../../ui/tiles.tsx';
import type { Tone } from '../../ui/tokens.ts';

export interface ServiceKind {
  kind: string;
  label: string;
  blurb: string;
}

/** The services in display order. Their names are the vendors': fixed, never editable. The core's integration.kinds adds the rest. */
export const KINDS: ServiceKind[] = [
  { kind: 'linear', label: 'Linear', blurb: 'Issues and projects' },
  { kind: 'notion', label: 'Notion', blurb: 'Pages as requirements text' },
  { kind: 'sentry', label: 'Sentry', blurb: 'Errors and issues' },
  { kind: 'aikido', label: 'Aikido', blurb: 'Security findings' },
  { kind: 'github', label: 'GitHub', blurb: 'Issues of a repository' },
];

/** The fixed name of a kind. */
export const kindLabel = (kind: string): string => KINDS.find((k) => k.kind === kind)?.label ?? kind;

/** The injection-risk score: 0 is low; 3 and above are never turned into tasks by a rule and cannot be imported. */
export const riskTone = (risk: number): Tone => (risk >= 3 ? 'err' : risk >= 1 ? 'warn' : 'ok');
export const riskText = (risk: number, flags: readonly string[] | null | undefined): string => `injection risk ${risk}${(flags ?? []).length ? `: ${(flags ?? []).join(', ')}` : ''}`;

/** 'forbidden' is one assignment whose scope the credential may not read; the connection and its other projects carry on. */
export const syncTone = (state: string): Tone => (state === 'ok' ? 'ok' : state === 'error' || state === 'forbidden' || state === 'unauthenticated' ? 'err' : 'muted');
export const outboxTone = (status: string): Tone => (status === 'sent' ? 'ok' : status === 'awaiting_approval' || status === 'approved' ? 'warn' : 'muted');
export const statusTone = (status: string): Tone => (status === 'ok' ? 'ok' : status === 'untested' ? 'muted' : 'warn');

/** key=value per line; a line without = is a key with an empty value. */
export function parseParams(text: string): Record<string, string> {
  return Object.fromEntries(
    text
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean)
      .map((l) => {
        const i = l.indexOf('=');
        return i < 0 ? [l, ''] : [l.slice(0, i).trim(), l.slice(i + 1)];
      }),
  );
}

export const syncText = (r: IntegrationSyncResult): string => (r.skipped ? `Skipped: ${r.skipped}` : `${r.seen} seen: ${r.new} new, ${r.changed} changed, ${r.gone} gone.`);

/** One line about what a connection reaches: "Kyle in Acme", or what is missing. */
export function identityLine(c: IntegrationView | undefined): string {
  if (!c) return 'Not connected';
  const id = c.identity ?? {};
  const who = [id.account, id.workspace].filter((x) => x && x.trim()).join(' in ');
  if (who) return `Connected: ${who}`;
  return c.status === 'ok' ? 'Connected' : c.status === 'untested' ? 'Connected, not checked yet' : `Connected (${c.status})`;
}

/** An assignment's scope in words, using the setting labels: "Team: ENG · Linear project: Launch". */
export function scopeSummary(info: IntegrationKindInfo | undefined, a: Pick<IntegrationAssignment, 'config'>): string {
  const cfg = a.config ?? {};
  const parts = (info?.settings ?? [])
    .filter((s) => cfg[s.key])
    .map((s) => `${s.label}: ${cfg[s.key]}`);
  for (const [k, v] of Object.entries(cfg)) {
    if (v && !(info?.settings ?? []).some((s) => s.key === k)) parts.push(`${k}: ${v}`);
  }
  return parts.length ? parts.join(' · ') : 'Everything the connection can see';
}

/** The connection of a kind (one per kind in a workspace), if any. */
export function tileState(conns: IntegrationView[], kind: string): IntegrationView | undefined {
  return conns.find((c) => c.kind === kind);
}

/**
 * One tile of the Integrations page (decision P47): a vendor. It has an integration kind (app sync), MCP connector cards (agent
 * tools), or both. `key` is the tile's data-kind: the integration kind, or for an MCP-only vendor the card id (the group name in
 * lower case when several cards share a group, as Cloudflare's do).
 */
export interface Vendor {
  key: string;
  label: string;
  /** The brand id of the logo (ui/brand-logo.tsx). */
  logo: string;
  blurb: string;
  kind?: ServiceKind;
  cards: McpGalleryCard[];
  /** The skill registries the vendor publishes, from its cards, without repeats. */
  skillRegistries: string[];
}

const dedupe = (xs: readonly string[]): string[] => Array.from(new Set(xs.filter(Boolean)));

/** A card's name without the group it belongs to: "Cloudflare Documentation" in the Cloudflare tile is "Documentation". */
const shortName = (c: McpGalleryCard, group: string): string => {
  const d = c.display.trim();
  return d.toLowerCase().startsWith(group.toLowerCase()) && d.length > group.length ? d.slice(group.length).trim() : d;
};

/**
 * The tiles in display order: every integration kind (with the gallery cards whose `integration` names it), then each vendor
 * that only has MCP cards, in gallery order, with cards of one group on one tile.
 */
export function vendors(kinds: readonly ServiceKind[], cards: readonly McpGalleryCard[]): Vendor[] {
  const out: Vendor[] = kinds.map((k) => {
    const mine = cards.filter((c) => c.integration === k.kind);
    return { key: k.kind, label: k.label, logo: k.kind, blurb: k.blurb, kind: k, cards: mine, skillRegistries: dedupe(mine.flatMap((c) => c.skillRegistries ?? [])) };
  });
  const groups = new Map<string, Vendor>();
  for (const c of cards) {
    if (c.integration) {
      if (kinds.some((k) => k.kind === c.integration)) continue;
    }
    if (c.group) {
      const key = c.group.toLowerCase();
      let v = groups.get(key);
      if (!v) {
        v = { key, label: c.group, logo: c.brand || c.id, blurb: '', cards: [], skillRegistries: [] };
        groups.set(key, v);
        out.push(v);
      }
      v.cards.push(c);
      v.blurb = v.cards.map((x) => shortName(x, c.group!)).join(', ');
      v.skillRegistries = dedupe(v.cards.flatMap((x) => x.skillRegistries ?? []));
      continue;
    }
    out.push({ key: c.id, label: c.display, logo: c.brand || c.id, blurb: 'Tools for agents', cards: [c], skillRegistries: dedupe(c.skillRegistries ?? []) });
  }
  return out;
}

/** The line under a connection that says when it was last checked against the person's account (P59). */
export function verifiedText(conn: Pick<IntegrationView, 'verified'>): string {
  const v = conn.verified;
  if (!v) return 'Not checked against your account yet: Check against your account reads it with the credential Drydock holds, and writes nothing.';
  return v.ok ? `Verified against your account on ${formatDate(v.at)}.` : `The check against your account on ${formatDate(v.at)} did not pass: ${v.summary}`;
}

/** Where a row stands: working, never set up, or waiting on the person (an expired key, a failed check, a sign-in). */
export type Bucket = 'on' | 'setup' | 'needs';

/** A row's state in a word or two, with the tone of its dot ('none': the words only) and the filter it counts under. */
export interface RowState {
  text: string;
  tone: Tone | 'none';
  bucket: Bucket;
}

const SETUP: RowState = { text: 'Set up', tone: 'none', bucket: 'setup' };
const ON: RowState = { text: 'On', tone: 'ok', bucket: 'on' };

/** The words of a connection status that needs the person (internal/integrations: unauthenticated, unreachable, ...). */
const CONN_WORD: Record<string, string> = { unauthenticated: 'Key refused', unreachable: 'Unreachable', degraded: 'Degraded', disconnected: 'Disconnected' };

/** A sync state that means the import does not work: the connection's own, or one of its projects'. */
const failingSync = (state: string | undefined): boolean => state === 'error' || state === 'forbidden' || state === 'unauthenticated';

/** App sync as one word: Set up, On, or what needs the person (the key, a failed check, a failing sync), the most pressing first. */
export function serviceState(conn: IntegrationView | undefined): RowState {
  if (!conn) return SETUP;
  if (conn.status !== 'ok' && conn.status !== 'untested') return { text: CONN_WORD[conn.status] ?? 'Needs you', tone: 'warn', bucket: 'needs' };
  if (conn.verified && !conn.verified.ok) return { text: 'Check failed', tone: 'err', bucket: 'needs' };
  if (failingSync(conn.sync?.state) || (conn.assignments ?? []).some((a) => failingSync(a.sync?.state))) return { text: 'Sync failing', tone: 'err', bucket: 'needs' };
  return ON;
}

const CARD_WORD: Record<string, RowState> = {
  error: { text: 'Error', tone: 'err', bucket: 'needs' },
  needs_token: { text: 'Needs a token', tone: 'warn', bucket: 'needs' },
  needs_sign_in: { text: 'Needs sign-in', tone: 'warn', bucket: 'needs' },
};

/** A vendor's MCP servers as one word: the worst of what needs the person, else On when one is connected, else Set up. */
export function cardsState(cards: readonly Pick<McpGalleryCard, 'status'>[]): RowState {
  for (const st of ['error', 'needs_token', 'needs_sign_in']) if (cards.some((c) => c.status === st)) return CARD_WORD[st]!;
  return cards.some((c) => c.status === 'connected') ? ON : SETUP;
}

/** A vendor's row: what needs the person on either channel (app sync first), else On when either works, else Set up. */
export function vendorState(v: Pick<Vendor, 'kind' | 'cards'>, conn: IntegrationView | undefined): RowState {
  const parts = [v.kind ? serviceState(conn) : undefined, v.cards.length > 0 ? cardsState(v.cards) : undefined].filter((x): x is RowState => !!x);
  return parts.find((p) => p.bucket === 'needs') ?? parts.find((p) => p.bucket === 'on') ?? SETUP;
}

/**
 * A model provider's row (OpenRouter's, decision P103): set up when there is none; on while an enabled one is up; Down needs the
 * person; enabled but never checked is not counted as connected (its page in Models tests it); a disabled one is set up again
 * from its page.
 */
export function providerRowState(list: readonly Pick<Provider, 'enabled' | 'status'>[]): RowState {
  const on = list.filter((p) => p.enabled);
  if (on.length === 0) return list.length > 0 ? { text: 'Disabled', tone: 'muted', bucket: 'setup' } : SETUP;
  if (on.some((p) => p.status === 'up')) return ON;
  if (on.every((p) => p.status === 'down')) return { text: 'Down', tone: 'err', bucket: 'needs' };
  return { text: 'Not checked', tone: 'muted', bucket: 'needs' };
}

/** The provider list could not be read: the row says so rather than Set up (it may well be set up). */
export const PROVIDERS_UNREAD: RowState = { text: 'Not known', tone: 'warn', bucket: 'needs' };

/** A server you added yourself: a failed connection first, then a missing sign-in, then tools never found. */
export function ownServerState(s: Pick<McpLibraryServer, 'lastError' | 'oauth' | 'oauthAt' | 'tools'>): RowState {
  if (s.lastError) return { text: 'Could not connect', tone: 'err', bucket: 'needs' };
  if (s.oauth && !s.oauthAt) return { text: 'Not signed in', tone: 'warn', bucket: 'needs' };
  if (s.tools.length === 0) return { text: 'No tools yet', tone: 'warn', bucket: 'needs' };
  return ON;
}

/** The tools of a vendor's servers in a few words: "3 tools, 2 read-only". */
export function toolsFact(cards: readonly Pick<McpGalleryCard, 'tools' | 'readOnlyTools'>[]): string {
  const n = cards.reduce((k, c) => k + c.tools.length, 0);
  if (n === 0) return '';
  const ro = cards.reduce((k, c) => k + c.tools.filter((x) => c.readOnlyTools.includes(x.name)).length, 0);
  return ro > 0 ? `${plural(n, 'tool')}, ${ro} read-only` : plural(n, 'tool');
}

/** The line under a vendor's name in the list: what it does, and one fact (its projects, or its tools). */
export function vendorSub(v: Pick<Vendor, 'kind' | 'cards' | 'blurb'>, conn: IntegrationView | undefined): string {
  if (v.kind) {
    const n = (conn?.assignments ?? []).length;
    return n > 0 ? `${v.blurb} · ${plural(n, 'project')}` : v.blurb;
  }
  const n = v.cards.reduce((k, c) => k + c.tools.length, 0);
  return n > 0 ? `${v.blurb} · ${plural(n, 'tool')}` : v.blurb;
}

/** The line under one of your servers: how Drydock reaches it, and its tools once they are known. */
export function ownServerSub(s: Pick<McpLibraryServer, 'transport' | 'tools'>): string {
  const how = s.transport === 'http' ? 'Web address' : 'Program on this PC';
  return s.tools.length > 0 ? `${how} · ${plural(s.tools.length, 'tool')}` : how;
}


/** How a connection signs in, in words: the method the API reported, else its kind. */
export function methodText(c: Pick<IntegrationView, 'kind' | 'authKind' | 'identity'>): string {
  if (c.kind === 'github' && c.authKind === 'gh') return 'your gh sign-in';
  const m = c.identity?.method?.trim();
  if (m) return m.charAt(0).toLowerCase() + m.slice(1);
  return c.authKind === 'api_key' ? 'API key' : c.authKind === 'oauth' ? 'sign-in' : c.authKind;
}

/** The line under a connected service's name: "Synced 4 min ago · personal API key", from the newest sync of its projects. */
export function syncLine(c: IntegrationView, now = Date.now()): string {
  const last = Math.max(c.sync?.lastSuccessAt ?? 0, ...(c.assignments ?? []).map((a) => a.sync?.lastSuccessAt ?? 0));
  const when = last > 0 ? `Synced ${agoWords(last, now)}` : (c.assignments ?? []).length > 0 ? 'Not synced yet' : 'Not assigned to a project yet';
  return `${when} · ${methodText(c)}`;
}

/** The key a row has in the address: a vendor by its own key, one of your servers by its id after "server-". */
export const serverKey = (id: string): string => `server-${id}`;
/** The server id in a row key, or '' for a vendor's key. */
export const serverIdOf = (key: string): string => (key.startsWith('server-') ? key.slice('server-'.length) : '');

/** The servers listed under "Your servers": every connected server that is not one of the catalog's cards (those are a vendor's). */
export function ownServers<S extends Pick<McpLibraryServer, 'id' | 'catalogId'>>(servers: readonly S[], cards: readonly Pick<McpGalleryCard, 'id' | 'libraryId'>[]): S[] {
  return servers.filter((s) => !cards.some((c) => c.libraryId === s.id || (!!s.catalogId && c.id === s.catalogId)));
}

/** How many rows each filter above the list shows: All, On, Needs you. */
export function filterCounts(states: readonly RowState[]): { all: number; on: number; needs: number } {
  return { all: states.length, on: states.filter((x) => x.bucket === 'on').length, needs: states.filter((x) => x.bucket === 'needs').length };
}

/** Whether a row in this state is shown under a filter: all, on or needs. */
export const showsUnder = (filter: string, st: RowState): boolean => (filter === 'on' ? st.bucket === 'on' : filter === 'needs' ? st.bucket === 'needs' : true);

/** A connection as one project has it, for its tile on the project's Integrations page: the scope it imports, or that it is not assigned. */
export function projectConnState(info: IntegrationKindInfo | undefined, conn: IntegrationView, projectId: string): { text: string; tone: TileTone; badge: TileBadge } {
  const a = (conn.assignments ?? []).find((x) => x.projectId === projectId);
  if (!a) return { text: 'Not assigned to this project', tone: '', badge: { text: 'not assigned', tone: 'muted' } };
  const st = a.sync?.state ?? 'never_synced';
  const scope = scopeSummary(info, a);
  if (st === 'error' || st === 'forbidden' || st === 'unauthenticated') return { text: `${scope} · sync: ${st}`, tone: 'bad', badge: { text: 'sync failing', tone: 'err' } };
  if (conn.status !== 'ok' && conn.status !== 'untested') return { text: `${scope} · ${conn.status}`, tone: 'warn', badge: { text: 'needs attention', tone: 'warn' } };
  return { text: scope, tone: st === 'ok' ? 'ok' : '', badge: { text: 'assigned', tone: 'ok' } };
}

/** What a connected MCP server is, for its tile on the project's Integrations page: built in or your own, and its tools. */
export function serverBlurb(x: Pick<McpLibraryServer, 'catalogId' | 'tools'>): string {
  return `${x.catalogId ? 'built into Drydock' : 'your own'} · ${x.tools.length > 0 ? plural(x.tools.length, 'tool') : 'tools not found yet'}`;
}

/**
 * A server as one project has it, for its tile on the project's Integrations page (laid out like the workspace's tiles): which of
 * the project's agents may use it, and on or off here. One the project does not have yet is "not added".
 */
export function projectServerState(srv: McpServer | undefined, agents: readonly Pick<Agent, 'id' | 'name'>[]): { text: string; tone: TileTone; badge: TileBadge } {
  if (!srv) return { text: 'Not in this project', tone: '', badge: { text: 'not added', tone: 'muted' } };
  const names = agents.filter((a) => srv.agents.some((g) => g.agentId === a.id)).map((a) => a.name);
  const who =
    names.length === 0 ? 'No agent may use it yet' : names.length <= 2 ? `${names.join(' and ')} may use it` : `${names.slice(0, 2).join(', ')} and ${names.length - 2} more may use it`;
  if (!srv.enabled) return { text: who, tone: '', badge: { text: 'off', tone: 'muted' } };
  return { text: who, tone: names.length > 0 ? 'ok' : '', badge: { text: 'on', tone: 'ok' } };
}

const isTestStep = (v: unknown): boolean => isRecord(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.ok === 'boolean' && (v.detail === undefined || typeof v.detail === 'string');

/** Whether `v` has the shape the test report view reads: a verdict, a summary and steps (null for none). */
export const isTestReport = (v: unknown): v is IntegrationTestReport =>
  isRecord(v) && typeof v.ok === 'boolean' && typeof v.summary === 'string' && (v.steps === undefined || v.steps === null || (Array.isArray(v.steps) && v.steps.every(isTestStep)));

/**
 * The answer of integration.connect, checked (audit 2026-10-16, L43): the connection, and a test report only in the shape the
 * page draws. A reply of another shape is an error that says so, where it used to be cast and crash the report view later.
 */
export function connectReply(raw: unknown): { connection: IntegrationView; test?: IntegrationTestReport } {
  if (!isRecord(raw) || !isRecord(raw.connection)) throw new Error('The core answered the connect request in a shape this page does not know: there is no connection in it.');
  if (raw.test !== undefined && raw.test !== null && !isTestReport(raw.test)) throw new Error('The core connected the service but sent a test report in a shape this page can not show.');
  const reply: { connection: IntegrationView; test?: IntegrationTestReport } = { connection: raw.connection as unknown as IntegrationView };
  if (raw.test) reply.test = raw.test as IntegrationTestReport;
  return reply;
}
