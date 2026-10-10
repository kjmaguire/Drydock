// Pure helpers for the Skills screen.

import type { Skill, SkillCoverage, SkillScan } from '../../api/extTypes.ts';
import type { Tone } from '../../ui/tokens.ts';

export const skillTone = (s: string): Tone => (s === 'accepted' ? 'ok' : s === 'offered' ? 'warn' : s === 'blocked' ? 'err' : 'muted');

/** What a scan did, in one sentence, with the core's problems verbatim. */
export function scanText(scan: SkillScan): string {
  const errors = scan.errors ?? [];
  return `Found ${scan.found}: ${scan.offered} offered, ${scan.accepted} accepted, ${scan.blocked} blocked, ${scan.removed} removed.${errors.length > 0 ? ` Problems: ${errors.join('; ')}` : ''}`;
}

export const coverageText = (c: SkillCoverage | undefined): string => (c ? `${c.files} delivered as rule files, ${c.tool} through a tool.` : '');

/** A list as words, with a fallback for none ('all'). */
export const listOr = (xs: readonly string[] | null | undefined, none = 'all'): string => (xs ?? []).join(', ') || none;

type Audience = Pick<Skill, 'audience' | 'roles' | 'agents'>;

/**
 * Whether a skill's roles take in an agent's role: no roles listed means every role (internal/skills RolesMatch; a skill's
 * roles are already canonical, so the comparison only ignores case and surrounding space).
 */
export function rolesMatch(roles: readonly string[] | null | undefined, role: string): boolean {
  const rs = roles ?? [];
  const r = role.trim().toLowerCase();
  return rs.length === 0 || rs.some((x) => x.toLowerCase() === r);
}

/** Whether a whole-team skill reaches every agent with this role, whatever the agent is given. */
export const byRole = (s: Audience, role: string): boolean => s.audience !== 'agents' && rolesMatch(s.roles, role);

/** Whether a skill goes to an agent: a whole-team skill to every agent its roles take in and to each agent given it, a skill for
 * chosen agents only to those (internal/skills Reaches). */
export const reaches = (s: Audience, agentId: string, role: string): boolean => (agentId !== '' && (s.agents ?? []).includes(agentId)) || byRole(s, role);

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
  if (s.audience === 'agents') return given ? `Only ${given}` : 'Nobody: no agent is chosen yet';
  const roles = s.roles ?? [];
  const team = roles.length > 0 ? `Every ${inWords(roles, 'or')} agent` : 'The whole team';
  return given ? `${team}, and ${given}` : team;
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
