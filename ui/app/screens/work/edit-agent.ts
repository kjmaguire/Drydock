// Where Work's crew rail sends a click on an agent, and its "+" (decision P141). One place, so the agent editor (the Crew tab's
// "Edit agent" panel, idea 4) is reached by changing these two lines alone: today the Crew tab with the agent named in ?edit=,
// and the Crew tab for a new agent (?new=agent; the Crew page's New agent button is there).

import { routes } from '../../routes.ts';

/** The address that opens an agent's editor. */
export function editAgentHref(agentId: string): string {
  return `${routes.team.href()}?edit=${encodeURIComponent(agentId)}`;
}

/** The address that starts a new agent for the project. */
export function newAgentHref(): string {
  return `${routes.team.href()}?new=agent`;
}
