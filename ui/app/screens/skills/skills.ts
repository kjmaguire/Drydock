// Pure helpers for the Skills screen and the Library's Skills section.

import type { LibrarySkill, Skill, SkillCoverage, SkillLibraryAddAll, SkillRegistryChange, SkillRegistryStatus, SkillScan, SkillUsage } from '../../api/extTypes.ts';
import { formatDateTime } from '../../lib/format.ts';
import type { TileTone } from '../../ui/tiles.tsx';
import type { Tone } from '../../ui/tokens.ts';

export const skillTone = (s: string): Tone => (s === 'accepted' ? 'ok' : s === 'offered' ? 'warn' : s === 'blocked' ? 'err' : 'muted');

/** What a scan did, in one sentence, with the core's problems verbatim. */
export function scanText(scan: SkillScan): string {
  const errors = scan.errors ?? [];
  return `Found ${scan.found}: ${scan.offered} offered, ${scan.accepted} accepted, ${scan.blocked} blocked, ${scan.removed} removed.${errors.length > 0 ? ` Problems: ${errors.join('; ')}` : ''}`;
}

export const coverageText = (c: SkillCoverage | undefined): string => (c ? `${c.files} delivered as rule files, ${c.tool} through a tool.` : '');

/** The brand each skill registry shows on its tiles (ui/brand-logo.tsx marks); a registry not named here shows a generic icon. */
export const SKILL_BRANDS: Record<string, string> = {
  'cloudflare-skills': 'cloudflare',
  'microsoft-skills': 'microsoft',
  'msdocs-agent-skills': 'microsoft',
  'msdocs-mcp-skills': 'microsoft',
  'msgraph-community': 'microsoft',
  'vercel-agent-skills': 'vercel',
  'anthropic-skills': 'anthropic',
  'openai-skills': 'openai',
  'stripe-ai': 'stripe',
  remix: 'remix',
  // retired registry (replaced by remix on 2026-10-14): a project's own copy of one of its skills still names it
  'react-router': 'remix',
};

/** The brand id of a skill's tile: its registry's brand; a repository skill (origin `repo`) has none and shows the file icon. */
export function skillBrand(registryId: string | undefined, origin?: string): string {
  if (origin === 'repo' || origin === 'repository' || !registryId) return '';
  return SKILL_BRANDS[registryId] ?? '';
}

/** The skill library entries whose name is not in the project yet (one name per project, whatever the registry). */
export function notInProject(library: readonly LibrarySkill[], skills: readonly Pick<Skill, 'name'>[]): LibrarySkill[] {
  const have = new Set(skills.map((s) => s.name));
  return library.filter((l) => !have.has(l.name));
}

/** The registries a list of library skills comes from, with their counts, in the order they first appear. */
export function libraryGroups(library: readonly LibrarySkill[]): { registryId: string; repo: string; count: number }[] {
  const out: { registryId: string; repo: string; count: number }[] = [];
  for (const l of library) {
    const g = out.find((x) => x.registryId === l.registryId);
    if (g) g.count++;
    else out.push({ registryId: l.registryId, repo: l.repo, count: 1 });
  }
  return out;
}

/** A library skill's state line: available, or blocked with the core's reason. */
export function libraryStateText(l: Pick<LibrarySkill, 'status' | 'blockReason' | 'gone'>): { text: string; tone: TileTone } {
  if (l.status === 'blocked') return { text: `Blocked: ${l.blockReason || 'the core did not say why'}`, tone: 'bad' };
  if (l.gone) return { text: 'Available · no longer upstream', tone: 'warn' };
  return { text: 'Available', tone: '' };
}

/** A library skill's blurb: what it does, and its license when known. */
export const libraryBlurb = (l: Pick<LibrarySkill, 'description' | 'license'>): string => [l.description, l.license].filter(Boolean).join(' · ');

/** The names in a project's skills whose library text is newer than the project's copy (registryStatus.projects[pid].updates). */
export function registryUpdates(statuses: readonly SkillRegistryStatus[] | undefined, projectId: string): Set<string> {
  const out = new Set<string>();
  for (const st of statuses ?? []) for (const u of st.projects?.[projectId]?.updates ?? []) out.add(u.name);
  return out;
}

/**
 * A project skill's state line on its tile: "Accepted ✓ · 2 agents" / "Accepted ✓ · no agent yet", "Offered", "Rejected",
 * "Blocked", or "Update in the Library" when the library's text is newer than this copy. Skills are given to agents (P76).
 */
export function skillStateText(s: Pick<Skill, 'status' | 'agents'>, hasUpdate: boolean): { text: string; tone: TileTone } {
  if (hasUpdate) return { text: 'Update in the Library', tone: 'warn' };
  if (s.status === 'accepted') {
    const n = (s.agents ?? []).length;
    return n === 0 ? { text: 'Accepted ✓ · no agent yet', tone: 'warn' } : { text: `Accepted ✓ · ${n} agent${n === 1 ? '' : 's'}`, tone: 'ok' };
  }
  if (s.status === 'offered') return { text: 'Offered', tone: 'warn' };
  if (s.status === 'blocked') return { text: 'Blocked', tone: 'bad' };
  if (s.status === 'rejected') return { text: 'Rejected', tone: '' };
  return { text: s.status, tone: '' };
}

/** What adding a whole registry did, in one line; blocked and skipped are named only when there were any. */
export function addAllText(r: SkillLibraryAddAll): string {
  const blocked = r.blocked ?? [];
  const skipped = r.skipped ?? [];
  const tail = `${blocked.length > 0 ? `, ${blocked.length} blocked` : ''}${skipped.length > 0 ? `, ${skipped.length} skipped` : ''}`;
  return `${(r.added ?? []).length} added, ${(r.unchanged ?? []).length} unchanged${tail}`;
}

/** The success line after one skill was added to a project: adding is the acceptance. */
export const addedText = (name: string, project: string): string => `${name} was added to ${project} and is accepted ✓`;

/**
 * A registry's state line on its tile: how many of its skills the library holds (and how many are blocked), and when it was
 * last synced; not synced yet; or the last sync's failure. A disabled registry says so first.
 */
export function registryStatusText(st: SkillRegistryStatus | undefined, enabled = st?.enabled ?? true): { text: string; tone: TileTone } {
  if (!enabled) return { text: 'Disabled: not synced', tone: '' };
  if (st?.lastError) return { text: `Last sync failed: ${st.lastError}`, tone: 'bad' };
  if (!st || !st.checkedAt) return { text: 'Not synced yet', tone: 'warn' };
  const blocked = st.blocked > 0 ? ` (${st.blocked} blocked)` : '';
  return { text: `${st.skills} skill${st.skills === 1 ? '' : 's'} in the Library${blocked} · synced ${formatDateTime(st.checkedAt)}`, tone: 'ok' };
}

/** What the last sync of a registry brought into the library, as lists (never null). */
export function syncChanges(st: SkillRegistryStatus | undefined): { added: SkillRegistryChange[]; changed: SkillRegistryChange[] } {
  return { added: st?.added ?? [], changed: st?.changed ?? [] };
}

/** The tokens a skill cost: the full text loaded into runs plus its listing for on-demand reading. */
export const usageTokens = (u: Pick<SkillUsage, 'tokensLoaded' | 'tokensListed'>): number => (u.tokensLoaded ?? 0) + (u.tokensListed ?? 0);

/** A count with thousands separators, the same in every locale. */
export const formatCount = (n: number): string => Math.round(n).toLocaleString('en-US');

/** Usage rows in reading order: the ones to remove first, then by tokens spent, most first, then by name. */
export function sortUsage(rows: readonly SkillUsage[]): SkillUsage[] {
  const rank = (u: SkillUsage) => (u.recommendation === 'remove' ? 0 : 1);
  return [...rows].sort((a, b) => rank(a) - rank(b) || usageTokens(b) - usageTokens(a) || a.name.localeCompare(b.name));
}

/** The tone and words of a usage recommendation; no recommendation reads "no data yet". */
export function recommendationBadge(rec: string): { tone: Tone; text: string } {
  if (rec === 'remove') return { tone: 'err', text: 'remove' };
  if (rec === 'narrow') return { tone: 'warn', text: 'narrow' };
  if (rec === 'keep') return { tone: 'ok', text: 'keep' };
  return { tone: 'muted', text: 'no data yet' };
}

/** A list as words, with a fallback for none ('all'). */
export const listOr = (xs: readonly string[] | null | undefined, none = 'all'): string => (xs ?? []).join(', ') || none;

type Audience = Pick<Skill, 'roles' | 'agents'>;

/**
 * Whether a skill's roles take in an agent's role: no roles listed means every role (internal/skills RolesMatch; a skill's
 * roles are already canonical, so the comparison only ignores case and surrounding space).
 */
export function rolesMatch(roles: readonly string[] | null | undefined, role: string): boolean {
  const rs = roles ?? [];
  const r = role.trim().toLowerCase();
  return rs.length === 0 || rs.some((x) => x.toLowerCase() === r);
}

/** Whether a skill goes to an agent: only to the agents it is given to (decision P76: each agent is a specialist; internal/skills
 * Reaches). */
export const reaches = (s: Audience, agentId: string): boolean => agentId !== '' && (s.agents ?? []).includes(agentId);

/** Whether a skill names the agent's role among its own: such an agent is the one it suggests, never one that gets it by role. */
export const suggestsRole = (s: Pick<Skill, 'roles'>, role: string): boolean => (s.roles ?? []).length > 0 && rolesMatch(s.roles, role);

/** The skill with only the agents still on the team (a removed agent keeps its rows but gets nothing); unchanged while the team
 * list is empty (not loaded yet). */
export function onTeam<T extends Audience>(s: T, team: readonly { id: string }[]): T {
  return team.length === 0 ? s : { ...s, agents: (s.agents ?? []).filter((id) => team.some((a) => a.id === id)) };
}

/** A list in words: "Ada", "Ada and Bob", "Ada, Bob and Cy". */
export function inWords(xs: readonly string[], and = 'and'): string {
  return xs.length <= 1 ? (xs[0] ?? '') : `${xs.slice(0, -1).join(', ')} ${and} ${xs[xs.length - 1]}`;
}

/** Who gets a skill, in words. nameOf turns an agent id into its name. */
export function audienceText(s: Audience, nameOf: (id: string) => string): string {
  const given = inWords((s.agents ?? []).map(nameOf));
  return given || 'Nobody yet: give it to the agents that need it';
}

/** The skills to give an agent and to take away from it (agent.create skills, agent.update addSkills / removeSkills). */
export interface SkillChanges {
  add: string[];
  remove: string[];
}

export const noSkillChanges = (): SkillChanges => ({ add: [], remove: [] });

/**
 * The changes after the human ticks (on) or unticks a skill. had says whether the agent was given the skill before the form
 * opened: ticking it again only cancels its removal, and unticking a skill it was never given only cancels its addition.
 */
export function toggleSkill(c: SkillChanges, name: string, had: boolean, on: boolean): SkillChanges {
  const add = c.add.filter((n) => n !== name);
  const remove = c.remove.filter((n) => n !== name);
  if (on && !had) add.push(name);
  if (!on && had) remove.push(name);
  return { add, remove };
}

/** Whether a skill is ticked in the form: given before and not removed, or added. */
export const isGiven = (c: SkillChanges, name: string, had: boolean): boolean => (had ? !c.remove.includes(name) : c.add.includes(name));

/**
 * A registry's tile line, short so the tile stays small: "20 skills · 3 blocked · 2 new" (new: what the last sync brought);
 * "Sync failed" or "Not synced yet"; the details (when, the error) are in the registry's panel.
 */
export function registryTileText(st: SkillRegistryStatus | undefined, enabled = st?.enabled ?? true): { text: string; tone: TileTone } {
  if (!enabled) return { text: 'Disabled', tone: '' };
  if (st?.lastError) return { text: 'Sync failed', tone: 'bad' };
  if (!st || !st.checkedAt) return { text: 'Not synced yet', tone: 'warn' };
  const fresh = (st.added ?? []).length;
  const parts = [`${st.skills} skill${st.skills === 1 ? '' : 's'}`];
  if (st.blocked > 0) parts.push(`${st.blocked} blocked`);
  if (fresh > 0) parts.push(`${fresh} new`);
  return { text: parts.join(' · '), tone: fresh > 0 ? 'warn' : 'ok' };
}

/** The first sentence of a description, at most max characters: what a row shows before it is opened. */
export function shortDescription(d: string, max = 110): string {
  const flat = d.replace(/\s+/g, ' ').trim();
  const end = flat.search(/[.!?](\s|$)/);
  const first = end >= 0 ? flat.slice(0, end + 1) : flat;
  return first.length <= max ? first : `${first.slice(0, max - 1).trimEnd()}…`;
}

/** The names the last sync of each registry added: they are marked new in the lists. */
export function newInLibrary(statuses: readonly SkillRegistryStatus[] | undefined): Set<string> {
  const out = new Set<string>();
  for (const st of statuses ?? []) for (const c of st.added ?? []) out.add(`${st.registryId}/${c.name}`);
  return out;
}

/** "Add 3 skills to gocalc" / "Add wrangler to gocalc". */
export function addSelectedText(names: readonly string[], project: string): string {
  return names.length === 1 ? `Add ${names[0]} to ${project}` : `Add ${names.length} skills to ${project}`;
}
