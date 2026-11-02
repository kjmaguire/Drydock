// The words of a project's sync line (decision P143), kept apart from the component so they run in Node: where the project's
// folder stands against its remote ("Up to date with GitHub", "3 behind · 1 ahead", "Not checked yet") and, after Sync now did
// nothing to the files, why in one line.

import type { RepoSync } from '../../api/types.ts';

/** The remote as the person knows it: GitHub for a github.com remote, else its name. */
export const remoteWord = (st: Pick<RepoSync, 'github' | 'remote'>): string => (st.github ? 'GitHub' : st.remote || 'origin');

export interface SyncLine {
  text: string;
  /** ok: up to date; behind: there is something to bring in; quiet: unknown yet; bad: the check failed. */
  tone: 'ok' | 'behind' | 'quiet' | 'bad';
  /** After Sync now left the files alone: why, in one line. */
  why?: string;
}

/** Whether the line shows at all: a project whose repository has a remote to sync with. */
export const showsSync = (st: RepoSync | undefined): st is RepoSync => !!st && !!st.remote;

export function syncLine(st: RepoSync): SyncLine {
  const r = remoteWord(st);
  const why = syncWhy(st);
  if (st.error) {
    const text = st.errorKind === 'update' ? 'Could not update the folder' : st.errorKind === 'config' ? 'Git settings changed: review them first' : `Could not reach ${r}`;
    return { text, tone: 'bad', why };
  }
  if (!st.checkedAt) return { text: st.checking ? `Checking ${r}…` : 'Not checked yet', tone: 'quiet', why };
  if (!st.tracked) return { text: `${r} has no ${st.branch || 'default branch'}`, tone: 'quiet', why };
  if (!st.ahead && !st.behind) return { text: `Up to date with ${r}`, tone: 'ok', why };
  const parts = [st.behind ? `${st.behind} behind` : '', st.ahead ? `${st.ahead} ahead` : ''].filter(Boolean);
  return { text: parts.join(' · '), tone: st.behind ? 'behind' : 'ok', why };
}

/** Why the last Sync now changed no file, in one line; undefined when it did, or there is nothing to say. */
export function syncWhy(st: Pick<RepoSync, 'outcome' | 'branch' | 'current' | 'behind'>): string | undefined {
  switch (st.outcome) {
    case 'local_changes':
      return 'Not updated: the folder has local changes.';
    case 'diverged':
      return 'Not updated: both sides have new commits. Drydock does not merge them.';
    case 'other_branch':
      return `Not updated: ${st.current || 'another branch'} is checked out, not ${st.branch}.`;
    case 'ahead':
      return 'Nothing new to bring in. Your commits stay local.';
    case 'agent_busy':
      return 'Not updated: a chat is working in this folder. Try again when it finishes.';
    default:
      return undefined;
  }
}
