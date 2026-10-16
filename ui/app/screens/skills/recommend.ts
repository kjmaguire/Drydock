// The plain text of recommendations (decision P52): what an item says to do, who asked, and the advisor's state.

import type { RecommendItem, Recommendation } from '../../api/extTypes.ts';

/** Who an item is for: the agents' names, or the whole team when it names none. */
export function forWhom(agents: readonly string[], names: ReadonlyMap<string, string>): string {
  if (agents.length === 0) return 'the whole team';
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
      return `Give the skill ${it.label || it.name} to the whole team`;
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
