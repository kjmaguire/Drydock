// Pure helpers for the team page, the agent editor and the template form (no DOM, no services).

import type { AppliedTemplate } from '../../api/libraryClient.ts';
import type { AdapterInfo, Capabilities, RouteModel } from '../../api/types.ts';

export const CAP_LABEL: [keyof Capabilities, string][] = [
  ['streaming', 'Streaming'],
  ['structuredOutput', 'Structured output'],
  ['modelSelection', 'Model selection'],
  ['effortControl', 'Effort control'],
  ['sessionResume', 'Session resume'],
  ['interrupt', 'Interrupt'],
  ['mcp', 'MCP'],
  ['tokenUsage', 'Token usage'],
  ['costReporting', 'Cost reporting'],
  ['headless', 'Headless'],
  ['imageInput', 'Image input'],
  ['toolCallVisibility', 'Tool-call visibility'],
];

export const ROLE_SUGGESTIONS = ['backend', 'frontend', 'reviewer', 'tester', 'architect'];

export const PURPOSE_HELP =
  'Standing instructions for this agent, sent with every run: what it owns, what it must not touch, how it should work. It never overrides the task, the independent review or your approval.';

/** adapter.list comes back in map order; show ready adapters first, then installed ones, then by id. */
export function sortAdapters(list: readonly AdapterInfo[]): AdapterInfo[] {
  const rank = (a: AdapterInfo) => (a.detection.status === 'ready' ? 0 : a.detection.installed ? 1 : 2);
  return [...list].sort((a, b) => rank(a) - rank(b) || a.id.localeCompare(b.id));
}

/** The purpose as shown on a team card: at most 160 characters. */
export function purposeExcerpt(purpose: string, max = 160): string {
  return purpose.length > max ? `${purpose.slice(0, max)}...` : purpose;
}

/** The option text of an adapter in the add-agent form. */
export function adapterOption(a: AdapterInfo): string {
  return `${a.id} (${a.detection.installed ? a.detection.status : 'not installed'})`;
}

/** The routed models when the human turns routing on: haiku then opus when the adapter offers both, else two empty rows. */
export function defaultRoutes(suggestions: readonly string[]): RouteModel[] {
  return suggestions.includes('haiku') && suggestions.includes('opus')
    ? [
        { model: 'haiku', tier: 'cheap' },
        { model: 'opus', tier: 'premium' },
      ]
    : [
        { model: '', tier: 'cheap' },
        { model: '', tier: 'premium' },
      ];
}

/** routeModels for agent.update: undefined when unchanged (the core keeps them), else the rows that name a model. */
export function routesForUpdate(routes: readonly RouteModel[], original: readonly RouteModel[] | undefined): RouteModel[] | undefined {
  return JSON.stringify(routes) === JSON.stringify(original ?? []) ? undefined : routes.filter((r) => r.model.trim());
}

/** What a template apply says: who joined, what was granted and what was not. */
export function appliedNote(r: AppliedTemplate): string {
  return `${r.agent.name} joined the team${r.granted.length ? `, with ${r.granted.join(', ')}` : ''}${r.skipped.length ? `. Not granted: ${r.skipped.join('; ')}` : ''}.`;
}

/**
 * Whether the Claude preset may overwrite the model and effort fields: only while they are empty or still hold the preset
 * applied last (the human has not changed them).
 */
export function presetUntouched(model: string, effort: string, last: { model: string; effort: string } | undefined): boolean {
  return (model === '' && effort === '') || (last !== undefined && model === last.model && effort === last.effort);
}
