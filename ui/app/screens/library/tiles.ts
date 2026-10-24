// What a Library tile says about an agent template: one line for what it is, one for its state (with the tone the tile colours it
// by). Pure, so the wording is tested without a page. The MCP servers it names are connected on the Integrations page.

import type { AgentTemplate } from '../../api/libraryClient.ts';

export type Line = { text: string; tone: 'ok' | 'warn' | 'bad' | '' };

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

/** What the template is: its role and the agent CLI it runs on. */
export function templateBlurb(tp: Pick<AgentTemplate, 'role' | 'adapter'>): string {
  return tp.role ? `${tp.role}, on ${tp.adapter}` : `Team member on ${tp.adapter}`;
}

/** The template's state: servers it names that are no longer connected, else what a new agent gets from it. */
export function templateState(tp: Pick<AgentTemplate, 'mcpServers' | 'skills'>, connectedIds: ReadonlySet<string>): Line {
  const missing = tp.mcpServers.filter((id) => !connectedIds.has(id)).length;
  if (missing > 0) return { text: `${plural(missing, 'server')} not connected`, tone: 'warn' };
  const parts = [tp.mcpServers.length ? plural(tp.mcpServers.length, 'server') : '', tp.skills.length ? plural(tp.skills.length, 'skill') : ''].filter(Boolean);
  return parts.length ? { text: `Gets ${parts.join(' and ')}`, tone: 'ok' } : { text: 'No servers or skills', tone: '' };
}
