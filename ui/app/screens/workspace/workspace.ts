// The plain text of the workspace Home: where a project lives and what needs you in it.

import { projectHasRepo, type Agent, type NeedsYouItem, type Project } from '../../api/types.ts';
import type { TileTone } from '../../ui/tiles.tsx';

/** Where a project's work happens: its repository's folder, or that it has none yet. */
export function projectWhere(p: Project): string {
  return projectHasRepo(p) ? p.repoPath : 'No repository yet';
}

/** The project tile's state line: how many items need the human there (undefined while it loads, null when it could not be read). */
export function needsText(n: number | null | undefined): { text: string; tone: TileTone } {
  if (n === undefined) return { text: 'Checking what needs you…', tone: '' };
  if (n === null) return { text: 'Could not check what needs you', tone: '' };
  if (n === 0) return { text: 'Nothing needs you', tone: '' };
  return { text: `${n} ${n === 1 ? 'item needs' : 'items need'} you`, tone: 'warn' };
}

/**
 * What needs the person in each project, from the one list of the whole workspace (needsyou.list of every project, which the
 * sidebar's count already reads): undefined while it loads, null for every project when it could not be read. One read
 * whatever the number of projects; one a project made Home ask 65 times with 64 projects (review 2026-10-27).
 */
export function needsByProject(items: readonly Pick<NeedsYouItem, 'projectId'>[] | undefined, failed: boolean, projects: readonly Pick<Project, 'id'>[]): Record<string, number | null | undefined> {
  const out: Record<string, number | null | undefined> = {};
  for (const p of projects) out[p.id] = items ? 0 : failed ? null : undefined;
  for (const it of items ?? []) if (it.projectId && out[it.projectId] !== undefined) out[it.projectId] = (out[it.projectId] ?? 0) + 1;
  return out;
}

/** Each project's crew, from the one list of every agent (agent.listAll). */
export function crewsByProject(agents: readonly Pick<Agent, 'id' | 'name' | 'projectId'>[]): Record<string, { id: string; name: string }[]> {
  const out: Record<string, { id: string; name: string }[]> = {};
  for (const a of agents) (out[a.projectId] ??= []).push({ id: a.id, name: a.name });
  return out;
}
