// Where Work's crew rail sends a click on an agent, and its "+" (decision P141). One place, so the opener can change here alone.
// A click opens the agent editor in place (decision P137: openAgentEditor, with the AgentPanelHost Work renders); the link's own
// address, /team?edit=<agentId>, opens the same editor on the Crew tab (a new window, a middle click). "+" goes to the Crew tab,
// where New agent is: the Crew page opens New agent (on Describe) for ?new=agent.

import { routes } from '../../routes.ts';
import { openAgentEditor } from '../team/agent-panel.tsx';

/** The address that opens an agent's editor. */
export function editAgentHref(agentId: string): string {
  return `${routes.team.href()}?edit=${encodeURIComponent(agentId)}`;
}

/** Opens an agent's editor on the page (the page renders <AgentPanelHost />). */
export function editAgent(agentId: string): void {
  openAgentEditor(agentId);
}

/** The address that starts a new agent for the project. */
export function newAgentHref(): string {
  return `${routes.team.href()}?new=agent`;
}
