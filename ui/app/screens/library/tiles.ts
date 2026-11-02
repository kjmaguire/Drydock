// What a Library tile says about an agent template: one line for what it is, one for its state (with the tone the tile colours it
// by). Pure, so the wording is tested without a page. The MCP servers it names are connected on the Integrations page.

import type { AgentTemplate } from '../../api/libraryClient.ts';
import type { PrebuiltAgent } from '../../api/types.ts';

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

/** The card's chip: the model and the effort ("opus · high"), or the agent CLI when no model is set. */
export function modelChip(tp: Pick<AgentTemplate, 'adapter' | 'model' | 'effort'>): string {
  return [tp.model || tp.adapter, tp.model ? tp.effort : ''].filter(Boolean).join(' · ');
}

/** The card's one line of purpose: the purpose's first sentence or line, '' when none is written. */
export function purposeLine(tp: Pick<AgentTemplate, 'purpose'>): string {
  const first = (tp.purpose ?? '').trim().split(/\n/)[0] ?? '';
  const end = first.search(/[.!?](\s|$)/);
  return end >= 0 ? first.slice(0, end + 1) : first;
}

/**
 * A starter (a prebuilt agent) as the fields of a new agent of my own, for the agent form to show before it is saved: the
 * preferred CLI, model, effort and permission mode, its skills, and the MCP servers it names that are already in the workspace's
 * library (the others are connected in Settings > Integrations).
 */
export function starterDraft(e: PrebuiltAgent): Partial<AgentTemplate> {
  return {
    name: e.name,
    role: e.role,
    purpose: e.purpose,
    adapter: e.preferred.adapter,
    model: e.preferred.model,
    effort: e.preferred.effort,
    permissionMode: e.preferred.permissionMode,
    ...(e.preferred.practices === false ? { practices: false } : {}),
    mcpServers: e.availability.mcp.filter((m) => m.inLibrary && m.libraryId).map((m) => m.libraryId!),
    skills: e.skills.map((k) => ({ registry: k.registry, name: k.name, ref: k.ref })),
  };
}
