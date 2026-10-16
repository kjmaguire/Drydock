// The repo map method (internal/core/ext_repomap.go, decision P62). Like the rest of the client: no behavior beyond turning
// Go's nil slices and maps (JSON null) into empty ones.

import type { Transport } from './transport.ts';

/** One file's place in the ranking. */
export interface RepoMapFile {
  path: string;
  lang: string;
  rank: number;
  defs: number;
  refs: number;
  focus?: boolean;
  /** The file is in the rendered map (the budget may leave lower-ranked files out). */
  shown: boolean;
}

/** Listed files that were not parsed, by reason. Files .gitignore ignores are never listed. */
export interface RepoMapSkipped {
  unsupported: number;
  vendored: number;
  generated: number;
  tooLarge: number;
  binary: number;
  unreadable: number;
  /** a symbolic link, or a path through one that leads out of the checkout: never read */
  linked: number;
  overLimit: number;
}

export interface RepoMapResult {
  map: string;
  tokens: number;
  budget: number;
  root: string;
  focus: string[];
  listed: number;
  files: number;
  cached: number;
  shown: number;
  defs: number;
  refs: number;
  languages: Record<string, number>;
  skipped: RepoMapSkipped;
  ranked: RepoMapFile[];
  elapsedMs: number;
}

export interface RepoMapView {
  /** The build has the tree-sitter parser (it was built with CGO). */
  available: boolean;
  /** Why there is no map. */
  message?: string;
  languages: string[];
  result?: RepoMapResult;
}

/** Token budgets a map may have (the repo_map tool's schema). */
export const REPO_MAP_MIN_BUDGET = 256;
export const REPO_MAP_MAX_BUDGET = 8192;
export const REPO_MAP_DEFAULT_BUDGET = 2048;

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const ZERO: RepoMapSkipped = { unsupported: 0, vendored: 0, generated: 0, tooLarge: 0, binary: 0, unreadable: 0, linked: 0, overLimit: 0 };

function result(r: RepoMapResult): RepoMapResult {
  return { ...r, focus: arr(r.focus), ranked: arr(r.ranked), languages: r.languages ?? {}, skipped: { ...ZERO, ...(r.skipped ?? {}) } };
}

export interface RepoMapClient {
  /** The map of the project's repository, personalised toward focus (repo-relative files or folders). Read-only. */
  repoMapGet(projectId: string, focus?: string[], tokenBudget?: number): Promise<RepoMapView>;
}

export function createRepoMapClient(t: Transport): RepoMapClient {
  return {
    repoMapGet: async (projectId, focus = [], tokenBudget = REPO_MAP_DEFAULT_BUDGET) => {
      const v = await t.call<RepoMapView | null>('repomap.get', { projectId, focus, tokenBudget });
      return { available: !!v?.available, message: v?.message, languages: arr(v?.languages), result: v?.result ? result(v.result) : undefined };
    },
  };
}
