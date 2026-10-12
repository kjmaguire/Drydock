// What a Library tile says about an MCP server or an agent template: one line for what it is, one for its state (with the tone
// the tile colours it by). Pure, so the wording is tested without a page.

import type { AgentTemplate } from '../../api/libraryClient.ts';
import type { McpLibraryServer } from '../../api/mcpClient.ts';

export type Line = { text: string; tone: 'ok' | 'warn' | 'bad' | '' };

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

/** What the server is: how Drydock reaches it, and how many tools it has once they are known. */
export function serverBlurb(s: Pick<McpLibraryServer, 'transport' | 'tools'>): string {
  const kind = s.transport === 'http' ? 'Web address' : 'Program on this computer';
  return s.tools.length > 0 ? `${kind}, ${plural(s.tools.length, 'tool')}` : kind;
}

/** The server's state: a failed connection first, then a missing sign-in, unknown tools, and where it is used. */
export function serverState(s: Pick<McpLibraryServer, 'lastError' | 'oauth' | 'oauthAt' | 'tools' | 'projects'>): Line {
  if (s.lastError) return { text: 'Could not connect', tone: 'bad' };
  if (s.oauth && !s.oauthAt) return { text: 'Not signed in', tone: 'warn' };
  if (s.tools.length === 0) return { text: 'Tools not found yet', tone: 'warn' };
  if (s.projects.length === 0) return { text: 'In no project yet', tone: '' };
  return { text: `In ${plural(s.projects.length, 'project')}`, tone: 'ok' };
}

/** What the template is: its role and the agent CLI it runs on. */
export function templateBlurb(tp: Pick<AgentTemplate, 'role' | 'adapter'>): string {
  return tp.role ? `${tp.role}, on ${tp.adapter}` : `Team member on ${tp.adapter}`;
}

/** The template's state: servers it names that the Library no longer has, else what a new agent gets from it. */
export function templateState(tp: Pick<AgentTemplate, 'mcpServers' | 'skills'>, libraryIds: ReadonlySet<string>): Line {
  const missing = tp.mcpServers.filter((id) => !libraryIds.has(id)).length;
  if (missing > 0) return { text: `${plural(missing, 'server')} not in the Library`, tone: 'warn' };
  const parts = [tp.mcpServers.length ? plural(tp.mcpServers.length, 'server') : '', tp.skills.length ? plural(tp.skills.length, 'skill') : ''].filter(Boolean);
  return parts.length ? { text: `Gets ${parts.join(' and ')}`, tone: 'ok' } : { text: 'No servers or skills', tone: '' };
}
