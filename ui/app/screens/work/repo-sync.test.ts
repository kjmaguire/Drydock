import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { RepoSync } from '../../api/types.ts';
import { remoteWord, showsSync, syncLine, syncWhy } from './repo-sync.ts';

const st = (over: Partial<RepoSync> = {}): RepoSync => ({ projectId: 'p1', remote: 'origin', github: true, branch: 'main', current: 'main', tracked: true, ahead: 0, behind: 0, checkedAt: 1, checking: false, ...over });

describe('the sync line (P143)', () => {
  it('shows only for a repository with a remote', () => {
    expect(showsSync(undefined)).toBe(false);
    expect(showsSync(st({ remote: '' }))).toBe(false);
    expect(showsSync(st())).toBe(true);
  });

  it('names GitHub, or the remote', () => {
    expect(remoteWord(st())).toBe('GitHub');
    expect(remoteWord(st({ github: false }))).toBe('origin');
  });

  it('says where the folder stands in one line', () => {
    expect(syncLine(st())).toEqual({ text: 'Up to date with GitHub', tone: 'ok', why: undefined });
    expect(syncLine(st({ behind: 3, ahead: 1 })).text).toBe('3 behind · 1 ahead');
    expect(syncLine(st({ behind: 3 })).tone).toBe('behind');
    expect(syncLine(st({ ahead: 2 }))).toEqual({ text: '2 ahead', tone: 'ok', why: undefined });
    expect(syncLine(st({ checkedAt: 0 }))).toEqual({ text: 'Not checked yet', tone: 'quiet', why: undefined });
    expect(syncLine(st({ checkedAt: 0, checking: true })).text).toBe('Checking GitHub…');
    expect(syncLine(st({ tracked: false })).text).toBe('GitHub has no main');
    expect(syncLine(st({ github: false, error: 'git fetch: could not read' }))).toEqual({ text: 'Could not reach origin', tone: 'bad', why: undefined });
  });

  it('after Sync now left the files alone, says why in one line', () => {
    expect(syncWhy(st({ outcome: 'local_changes' }))).toBe('Not updated: the folder has local changes.');
    expect(syncWhy(st({ outcome: 'diverged' }))).toBe('Not updated: both sides have new commits. Drydock does not merge them.');
    expect(syncWhy(st({ outcome: 'other_branch', current: 'experiment' }))).toBe('Not updated: experiment is checked out, not main.');
    expect(syncWhy(st({ outcome: 'ahead' }))).toBe('Nothing new to bring in. Your commits stay local.');
    for (const o of ['updated', 'up_to_date', '', undefined] as const) expect(syncWhy(st({ outcome: o }))).toBe(undefined);
    expect(syncLine(st({ behind: 1, outcome: 'local_changes' })).why).toBe('Not updated: the folder has local changes.');
  });
});
