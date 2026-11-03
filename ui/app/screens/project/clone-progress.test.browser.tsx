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
    expect(cloneLine('acme/gocalc', { phase: 'Resolving deltas', percent: 7 })).toBe('Cloning gocalc… resolving deltas 7%');
    expect(cloneLine('acme/gocalc', { phase: 'Receiving objects', percent: 3 })).toBe('Cloning gocalc… receiving 3%');
    expect(cloneLine('acme/gocalc', { phase: 'Updating files', percent: 90 })).toBe('Cloning gocalc… writing files 90%');
    // the remote's own phases reach 100% before anything is fetched: no percent for them
    expect(cloneLine('acme/gocalc', { phase: 'Counting objects', percent: 100 })).toBe('Cloning gocalc…');
    expect(cloneLine('acme/gocalc', { phase: 'Compressing objects', percent: 100 })).toBe('Cloning gocalc…');
  });

  it('shows the percent as it comes and cancels the clone', async () => {
    const cancels: string[] = [];
    const s = testServices({ githubCloneCancel: async (repo: string) => (cancels.push(repo), true) });
    const r = mount(<CloneProgress repo="acme/gocalc" />, s);
    expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('Cloning gocalc…');
    s.emit({ type: 'github.clone_progress', data: { repo: 'acme/gocalc', phase: 'Counting objects', percent: 100 } });
    await waitFor(() => expect(r.$('[data-testid="clone-progress"]')!.title).toBe('Counting objects'), r);
    expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('Cloning gocalc…');
    s.emit({ type: 'github.clone_progress', data: { repo: 'acme/gocalc', phase: 'Receiving objects', percent: 42 } });
    await waitFor(() => expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('Cloning gocalc… receiving 42%'), r);
    s.emit({ type: 'github.clone_progress', data: { repo: 'acme/other', phase: 'Receiving objects', percent: 90 } });
    await r.act(() => (r.$('[data-testid="clone-cancel"]') as HTMLButtonElement).click());
    await waitFor(() => expect(cancels).toEqual(['acme/gocalc']), r);
    expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('Cancelling the clone…');
  });

  it('says the clone has finished when Cancel finds nothing running, and drops Cancel', async () => {
    const s = testServices({ githubCloneCancel: async () => false });
    const r = mount(<CloneProgress repo="acme/gocalc" />, s);
    // before git has said anything, nothing was running yet: Cancel stays to be tried again
    await r.act(() => (r.$('[data-testid="clone-cancel"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('The clone has not started yet. Try Cancel again in a moment.'), r);
    expect((r.$('[data-testid="clone-cancel"]') as HTMLButtonElement).disabled).toBe(false);
    s.emit({ type: 'github.clone_progress', data: { repo: 'acme/gocalc', phase: 'Updating files', percent: 100 } });
    await waitFor(() => expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('Cloning gocalc… writing files 100%'), r);
    await r.act(() => (r.$('[data-testid="clone-cancel"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('The clone has finished, so it can no longer be cancelled.'), r);
    expect(r.$('[data-testid="clone-cancel"]')).toBeNull();
  });
});
