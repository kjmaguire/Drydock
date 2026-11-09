// Project names that tell projects apart (critique 2026-11-08): two projects may share a name ("gocalc" opened twice, from two
// folders), and a list that shows both the same way leaves the person guessing. A name only one project has is shown as it is; a
// shared one gets the folder that holds its repository ("gocalc · repo1"), else its key, else its place in the list. Pure.

import type { Project } from '../api/types.ts';

/** The last part of a path, on either separator; '' for none. */
const lastPart = (path: string): string => path.replace(/[\\/]+$/, '').split(/[\\/]/).pop() ?? '';

/** The folder that holds a repository folder: "repo1" of "/src/repo1/gocalc". */
const parentName = (path: string): string => lastPart(path.replace(/[\\/]+$/, '').replace(/[\\/][^\\/]*$/, ''));

/**
 * Each project's name with what tells it apart: `detail` is '' for a name only it has, else its repository's parent folder (or its
 * key, or "#2" by its place in the list when neither tells them apart). `label` is "name · detail", or the name.
 */
export function projectLabels(projects: readonly Pick<Project, 'id' | 'name' | 'repoPath' | 'key'>[]): Map<string, { label: string; detail: string }> {
  const out = new Map<string, { label: string; detail: string }>();
  const byName = new Map<string, (typeof projects)[number][]>();
  for (const p of projects) {
    const k = p.name.trim().toLowerCase();
    byName.set(k, [...(byName.get(k) ?? []), p]);
  }
  for (const same of byName.values()) {
    if (same.length === 1) {
      out.set(same[0]!.id, { label: same[0]!.name, detail: '' });
      continue;
    }
    const parents = same.map((p) => parentName(p.repoPath ?? ''));
    const keys = same.map((p) => p.key ?? '');
    const unique = (xs: string[]) => xs.every((x) => x) && new Set(xs).size === xs.length;
    same.forEach((p, i) => {
      const detail = unique(parents) ? parents[i]! : unique(keys) ? keys[i]! : `#${i + 1}`;
      out.set(p.id, { label: `${p.name} · ${detail}`, detail });
    });
  }
  return out;
}
