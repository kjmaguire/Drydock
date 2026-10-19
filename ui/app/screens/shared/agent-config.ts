// Pure helpers for the agent configuration guard's screens (audit 2026-10-16, M3): the wording, which findings need a person, and
// the key that tells one finding from another. No DOM, no services.

import type { AgentConfigFinding } from '../../api/types.ts';

/**
 * Why this matters, in two sentences (the group's description in Project settings). The files are the ones a CLI loads from
 * the folder it works in; the core refuses to start the CLI there until a person trusted exactly those files.
 */
export const AGENT_CONFIG_WHY =
  'Files a repository brings (hooks, plugins, MCP servers) can run programs or send prompts elsewhere. Drydock does not start the CLI where they are not trusted: read them, then trust or remove them.';

/** The findings nobody trusted yet: the ones that keep a CLI from starting. */
export const untrusted = (list: readonly AgentConfigFinding[]): AgentConfigFinding[] => list.filter((f) => !f.trusted);

/** One finding's identity in a list: the adapter, the place and the state that was shown. */
export const findingKey = (f: Pick<AgentConfigFinding, 'adapter' | 'taskId' | 'fingerprint'>): string => `${f.adapter}:${f.taskId || 'repository'}:${f.fingerprint}`;

/** What trusting a finding trusts: the adapter and the fingerprint that were shown (every place with the same files follows). */
export const trustKey = (f: Pick<AgentConfigFinding, 'adapter' | 'fingerprint'>): string => `${f.adapter}:${f.fingerprint}`;

/** "2 places have configuration nobody trusted: gemini, opencode", for the banner's lead. */
export function untrustedSummary(list: readonly AgentConfigFinding[]): string {
  const open = untrusted(list);
  const adapters = [...new Set(open.map((f) => f.adapter))].sort();
  return `${open.length} ${open.length === 1 ? 'place has' : 'places have'} configuration that ${adapters.length === 1 ? 'an agent CLI' : 'agent CLIs'} would act on and nobody trusted (${adapters.join(', ')})`;
}
