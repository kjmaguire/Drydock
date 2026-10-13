// Pure helpers of the Integrations screen: the services (before the core's catalog arrives), the vendor tiles the page shows
// (an integration kind, the MCP connector cards of the same brand, or both), the tones of the status badges, the parameter text
// of an outbound write, the sentence a sync result becomes, and how a connection and an assignment are described in a line.

import type { IntegrationAssignment, IntegrationKindInfo, IntegrationSyncResult, IntegrationView } from '../../api/extTypes.ts';
import type { McpGalleryCard } from '../../api/mcpClient.ts';
import type { TileTone } from '../../ui/tiles.tsx';
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

/** The app-sync half of a tile's state line. */
export function syncStateText(c: IntegrationView | undefined): string {
  if (!c) return 'not connected';
  const account = c.identity?.account?.trim();
  if (account) return `connected as ${account}`;
  return c.status === 'ok' ? 'connected' : c.status === 'untested' ? 'connected, not checked yet' : `connected (${c.status})`;
}

const CARD_STATE: Record<string, string> = { connected: 'connected', needs_sign_in: 'needs sign-in', needs_token: 'needs a token', error: 'error', not_connected: 'not connected' };
const CARD_RANK = ['error', 'needs_token', 'needs_sign_in', 'not_connected'];

/** The agent-tools half of a tile's state line: one card's state, or how many of several are connected and what the rest need. */
export function agentsStateText(cards: readonly McpGalleryCard[]): string {
  if (cards.length === 0) return '';
  const connected = cards.filter((c) => c.status === 'connected').length;
  if (connected === cards.length) return 'connected';
  const rest: string[] = cards.filter((c) => c.status !== 'connected').map((c) => c.status);
  const worst = CARD_RANK.find((st) => rest.includes(st)) ?? rest[0] ?? 'not_connected';
  const word = CARD_STATE[worst] ?? worst;
  return connected === 0 ? word : `${connected} of ${cards.length} connected, ${cards.length - connected} ${word}`;
}

/** The tile's state line: both channels where the vendor has them ("Sync: connected as kyle · Agents: needs a token"). */
export function vendorStateText(v: Pick<Vendor, 'kind' | 'cards'>, conn: IntegrationView | undefined): string {
  const parts: string[] = [];
  if (v.kind) parts.push(`Sync: ${syncStateText(conn)}`);
  if (v.cards.length > 0) parts.push(`Agents: ${agentsStateText(v.cards)}`);
  return parts.join(' · ');
}

/** The tone of the state line: warn when something needs the human, ok when something is connected and nothing is wrong. */
export function vendorTone(v: Pick<Vendor, 'kind' | 'cards'>, conn: IntegrationView | undefined): TileTone {
  const cardStates = v.cards.map((c) => c.status);
  if (cardStates.some((st) => st === 'error' || st === 'needs_token' || st === 'needs_sign_in')) return 'warn';
  if (conn && conn.status !== 'ok' && conn.status !== 'untested') return 'warn';
  if (conn || cardStates.some((st) => st === 'connected')) return 'ok';
  return '';
}
