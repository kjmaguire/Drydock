// The plain text of the workspace Home: where a project lives and what needs you in it.

import { projectHasRepo, type Project } from '../../api/types.ts';
import type { TileTone } from '../../ui/tiles.tsx';

/** Where a project's work happens: its repository's folder, or that it has none yet. */
export function projectWhere(p: Project): string {
  return projectHasRepo(p) ? p.repoPath : 'No repository yet';
}

/** The project tile's state line: how many items need the human there (unknown while it loads). */
export function needsText(n: number | undefined): { text: string; tone: TileTone } {
  if (n === undefined) return { text: 'Checking what needs you…', tone: '' };
  if (n === 0) return { text: 'Nothing needs you', tone: '' };
  return { text: `${n} ${n === 1 ? 'item needs' : 'items need'} you`, tone: 'warn' };
}
