import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { NeedsYouItem } from '../api/types.ts';
import { needsByProject } from './workspace.ts';

describe('needsByProject', () => {
  it("groups what needs the person by project, in the list's order, and leaves out what belongs to none", () => {
    const items = [
      { key: 'a', projectId: 'p1' },
      { key: 'b', projectId: 'p2' },
      { key: 'c' },
      { key: 'd', projectId: 'p1' },
    ] as NeedsYouItem[];
    const got = needsByProject(items);
    expect([...got.keys()]).toEqual(['p1', 'p2']);
    expect(got.get('p1')!.map((x) => x.key)).toEqual(['a', 'd']);
    expect(needsByProject(undefined).size).toBe(0);
  });
});
