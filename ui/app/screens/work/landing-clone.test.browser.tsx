import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { LandingClone } from '../../api/types.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { LandingCloneLine, landingFailedText } from './landing-clone.tsx';

afterEach(() => cleanup());

describe('the clone a made project waits for (B12)', () => {
  it('shows the clone as it goes with Cancel, then why it did not land with Clone and attach, and goes once it lands', async () => {
    // TEST DOUBLE: project.cloneState answers what the core would at each step
    let state: LandingClone | null = { projectId: 'p1', repo: 'acme/gocalc', running: true };
    const asked: string[] = [];
    const s = testServices({ projectCloneState: async (id: string) => (asked.push(id), state) });
    const r = mount(<LandingCloneLine projectId="p1" />, s);
    await waitFor(() => expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('Cloning gocalc…'), r);
    expect(r.$('[data-testid="clone-cancel"]')).not.toBeNull();
    s.emit({ type: 'github.clone_progress', data: { repo: 'acme/gocalc', phase: 'Receiving objects', percent: 42 } });
    await waitFor(() => expect(r.$('[data-testid="clone-progress"]')!.textContent).toBe('Cloning gocalc… receiving 42%'), r);

    // another project's clone changes nothing here
    state = { projectId: 'p1', repo: 'acme/gocalc', running: false, error: 'the clone of acme/gocalc was cancelled: nothing was kept' };
    s.emit({ type: 'project.clone_failed', projectId: 'p2' });
    await new Promise((ok) => setTimeout(ok, 30));
    expect(asked).toEqual(['p1']);

    s.emit({ type: 'project.clone_failed', projectId: 'p1' });
    const line = await waitFor(() => {
      const el = r.$('[data-testid="landing-clone"][data-state="failed"]');
      if (!el) throw new Error('no failed line');
      return el;
    }, r);
    expect(line.textContent).toContain('The project has no repository yet: the clone of acme/gocalc was cancelled: nothing was kept.');
    expect(r.$('[data-testid="landing-clone-settings"]')!.getAttribute('href')).toBe('/project');

    state = null;
    s.emit({ type: 'project.clone_landed', projectId: 'p1' });
    await waitFor(() => expect(r.$('[data-testid="landing-clone"]')).toBeNull(), r);
  });

  it('an older core without project.cloneState shows nothing', async () => {
    const s = testServices({
      projectCloneState: async () => {
        throw new Error('unknown method project.cloneState');
      },
    });
    const r = mount(<LandingCloneLine projectId="p1" />, s);
    await new Promise((ok) => setTimeout(ok, 30));
    expect(r.$('[data-testid="landing-clone"]')).toBeNull();
  });

  it('says why in one sentence', () => {
    expect(landingFailedText({ repo: 'acme/x', error: '' })).toBe('The project has no repository yet: the clone of acme/x did not land.');
    expect(landingFailedText({ repo: 'acme/x', error: 'cloning acme/x failed: exit 128.' })).toBe('The project has no repository yet: cloning acme/x failed: exit 128.');
  });
});
