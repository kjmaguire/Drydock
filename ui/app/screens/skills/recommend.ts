// The plain text of recommendations (decision P52): what an item says to do, who asked, and the advisor's state.

import type { RecommendItem, Recommendation } from '../../api/extTypes.ts';

/** Who an item is for: the agents' names (a skill item always names them, decision P76). */
export function forWhom(agents: readonly string[], names: ReadonlyMap<string, string>): string {
  if (agents.length === 0) return 'nobody named';
  return agents.map((id) => names.get(id) ?? id).join(', ');
}

/** What one recommendation says to do, in words. */
export function itemText(it: RecommendItem, names: ReadonlyMap<string, string>): string {
  const who = forWhom(it.agents, names);
  switch (`${it.kind}/${it.action}`) {
    case 'skill/add':
      return `Add the skill ${it.label || it.name} for ${who}`;
    case 'skill/give':
      return `Give the skill ${it.label || it.name} to ${who}`;
    case 'skill/team':
      // stored before P76; it cannot be applied now that skills go to agents
      return `Give the skill ${it.label || it.name} to the whole team (skills now go to chosen agents)`;
    case 'skill/remove':
      return `Stop using the skill ${it.label || it.name}`;
    case 'mcp/give':
      return `Give the MCP server ${it.label || it.name} to ${who}`;
    case 'integration/add':
      return `Assign ${it.label || it.name} to this project`;
    default:
      return `${it.action} ${it.kind} ${it.label || it.name}`;
  }
}

/** Why a turn ran. */
export function triggerText(trigger: string): string {
  switch (trigger) {
    case 'skills_added':
      return 'after you picked skills in the Library';
    case 'evolution':
      return 'by itself, as the project evolved';
    default:
      return 'when you asked';
  }
}

/** The items still waiting for the person. */
export const openItems = (r: Recommendation | undefined): RecommendItem[] => (r?.items ?? []).filter((i) => i.status === 'open');

/** An open item the person can apply with one click here (an integration is assigned on Integrations; a whole-team skill item
 *  from before P76 cannot be applied). */
export const canApply = (it: RecommendItem): boolean => it.status === 'open' && it.kind !== 'integration' && !(it.kind === 'skill' && it.action === 'team');

/** The open items of the latest finished turn, for the line on Work: who recommends them and how many; undefined when none wait
 *  (or the latest turn is still running or failed). */
export function waitingRecommendations(runs: readonly Recommendation[], names: ReadonlyMap<string, string>): { who: string; count: number } | undefined {
  const latest = runs[0];
  if (!latest || latest.status !== 'done') return undefined;
  const count = openItems(latest).length;
  return count > 0 ? { who: names.get(latest.agentId) ?? 'An agent', count } : undefined;
}

/** "Lead recommends 3 changes". */
export const recommendsText = (w: { who: string; count: number }): string => `${w.who} recommends ${w.count} change${w.count === 1 ? '' : 's'}`;
