import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { projectLabels } from './project-label.ts';

describe('project names that tell projects apart (critique 2026-11-08)', () => {
  it('leaves a name only one project has as it is', () => {
    const m = projectLabels([{ id: 'a', name: 'gocalc', repoPath: '/src/one/gocalc' }]);
    expect(m.get('a')).toEqual({ label: 'gocalc', detail: '' });
  });

  it("gives a shared name its repository's parent folder, on either separator and in any case", () => {
    const m = projectLabels([
      { id: 'a', name: 'gocalc', repoPath: '/tmp/x/repo0/gocalc' },
      { id: 'b', name: 'GoCalc', repoPath: 'C:\\code\\repo1\\gocalc\\' },
      { id: 'c', name: 'shop', repoPath: '/src/shop' },
    ]);
    expect(m.get('a')).toEqual({ label: 'gocalc · repo0', detail: 'repo0' });
    expect(m.get('b')).toEqual({ label: 'GoCalc · repo1', detail: 'repo1' });
    expect(m.get('c')).toEqual({ label: 'shop', detail: '' });
  });

  it('falls back to the key, then to the place in the list, when the folders do not tell them apart', () => {
    const keyed = projectLabels([
      { id: 'a', name: 'app', repoPath: '/src/app', key: 'APP' },
      { id: 'b', name: 'app', repoPath: '', key: 'APP2' },
    ]);
    expect([keyed.get('a')!.label, keyed.get('b')!.label]).toEqual(['app · APP', 'app · APP2']);
    const bare = projectLabels([
      { id: 'a', name: 'app', repoPath: '' },
      { id: 'b', name: 'app', repoPath: '' },
    ]);
    expect([bare.get('a')!.label, bare.get('b')!.label]).toEqual(['app · #1', 'app · #2']);
  });
});
