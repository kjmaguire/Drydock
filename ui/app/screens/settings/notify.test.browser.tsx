import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { NotifyPrefs } from '../../api/notifyClient.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { NotifySection } from './notify.tsx';

afterEach(cleanup);

describe('Settings > General, Notifications (G1)', () => {
  it('shows the switch the core keeps, on by default, and turning it off is kept by the core', async () => {
    const sent: NotifyPrefs[] = [];
    let kept: NotifyPrefs = { needsYou: true };
    const s = testServices({
      notifyGet: async () => kept,
      notifySet: async (p) => {
        sent.push(p);
        kept = p;
        return p;
      },
    });
    const r = mount(<NotifySection />, s);
    const sw = await waitFor(() => {
      const el = r.container.querySelector<HTMLButtonElement>('[data-testid="notify"] button');
      if (!el) throw new Error('no switch yet');
      return el;
    }, r);
    expect(r.container.querySelector('[data-testid="notify"] label')!.textContent).toBe('Tell me when something needs me');
    expect(sw.getAttribute('aria-checked')).toBe('true');
    sw.click();
    await waitFor(() => expect(sent).toEqual([{ needsYou: false }]), r);
    await waitFor(() => expect(sw.getAttribute('aria-checked')).toBe('false'), r);
  });
});
