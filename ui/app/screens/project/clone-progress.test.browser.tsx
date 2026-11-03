import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { CloneProgress, cloneLine, cloneStepOf } from './clone-progress.tsx';

afterEach(cleanup);

describe('a clone in progress (B12)', () => {
  it('reads the step from its own repository events only', () => {
    const ev = (data: unknown, type = 'github.clone_progress') => ({ seq: 1, time: '', type, data });
    expect(cloneStepOf(ev({ repo: 'Acme/GoCalc', phase: 'Receiving objects', percent: 42 }), 'acme/gocalc')).toEqual({ phase: 'Receiving objects', percent: 42 });
    expect(cloneStepOf(ev({ repo: 'acme/other', percent: 42 }), 'acme/gocalc')).toBeUndefined();
    expect(cloneStepOf(ev({ repo: 'acme/gocalc', percent: 42 }, 'github.cloned'), 'acme/gocalc')).toBeUndefined();
    expect(cloneLine('acme/gocalc', undefined)).toBe('Cloning gocalc…');
    expect(cloneLine('acme/gocalc', { phase: 'Resolving deltas', percent: 7 })).toBe('Cloning gocalc… 7%');
  });

  it('shows the percent as it comes and cancels the clone', async () => {
    const cancels: string[] = [];
    const s = testServices({ githubCloneCancel: async (repo: string) => (cancels.push(repo), true) });
    const r = mount(<CloneProgress repo="acme/gocalc" />, s);
    expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('Cloning gocalc…');
    s.emit({ type: 'github.clone_progress', data: { repo: 'acme/gocalc', phase: 'Receiving objects', percent: 42 } });
    await waitFor(() => expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('Cloning gocalc… 42%'), r);
    s.emit({ type: 'github.clone_progress', data: { repo: 'acme/other', phase: 'Receiving objects', percent: 90 } });
    await r.act(() => (r.$('[data-testid="clone-cancel"]') as HTMLButtonElement).click());
    await waitFor(() => expect(cancels).toEqual(['acme/gocalc']), r);
    expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('Cancelling the clone…');
  });
});
