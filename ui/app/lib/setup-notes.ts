// What failed after a project was made (B2): each warning of project.setup is a line on the project's Work page with the action
// that fixes it, kept for that project in this browser until the person dismisses it or it is resolved (a CLI line goes once
// the CLI is ready). A convenience of this viewer: the core keeps nothing of it.

import type { SetupResult, SetupWarning } from '../api/types.ts';

export const SETUP_NOTES_KEY = 'drydock.setupNotes';
/** Projects kept at most (the newest first). */
const KEEP = 20;

type Store = Pick<Storage, 'getItem' | 'setItem'>;

function readAll(store: Store | undefined): Record<string, SetupWarning[]> {
  try {
    const v = JSON.parse(store?.getItem(SETUP_NOTES_KEY) ?? '{}') as unknown;
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, SetupWarning[]>) : {};
  } catch {
    return {};
  }
}

function writeAll(store: Store | undefined, all: Record<string, SetupWarning[]>): void {
  try {
    store?.setItem(SETUP_NOTES_KEY, JSON.stringify(all));
  } catch {
    /* a convenience only */
  }
}

const valid = (w: unknown): w is SetupWarning => !!w && typeof w === 'object' && typeof (w as SetupWarning).text === 'string' && typeof (w as SetupWarning).kind === 'string';

/** The lines of a setup result: its problems, or (an older core) each warning as a line of kind other. */
export function setupLines(r: Pick<SetupResult, 'warnings'> & Partial<Pick<SetupResult, 'problems'>>): SetupWarning[] {
  if (r.problems?.length) return r.problems.filter(valid);
  return (r.warnings ?? []).map((text) => ({ kind: 'other', text }));
}

/** Keeps a new project's lines (none: nothing is kept). */
export function keepSetupNotes(store: Store | undefined, projectId: string, lines: readonly SetupWarning[]): void {
  if (!projectId || lines.length === 0) return;
  const all = readAll(store);
  delete all[projectId];
  const next: Record<string, SetupWarning[]> = { [projectId]: [...lines] };
  for (const [k, v] of Object.entries(all).slice(0, KEEP - 1)) next[k] = v;
  writeAll(store, next);
}

/** A project's lines still kept. */
export function setupNotes(store: Store | undefined, projectId: string): SetupWarning[] {
  const v = readAll(store)[projectId];
  return Array.isArray(v) ? v.filter(valid) : [];
}

/** Forgets one line (dismissed or resolved); the project is forgotten with its last line. */
export function dropSetupNote(store: Store | undefined, projectId: string, line: SetupWarning): SetupWarning[] {
  const all = readAll(store);
  const left = (all[projectId] ?? []).filter((w) => !(w.kind === line.kind && w.text === line.text));
  if (left.length) all[projectId] = left;
  else delete all[projectId];
  writeAll(store, all);
  return left;
}
