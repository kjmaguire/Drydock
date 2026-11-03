import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { NotifyPrefs } from '../../api/notifyClient.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { NotifySection } from './notify.tsx';

afterEach(cleanup);

describe('Settings > General, Notifications (G1)', () => {
  it('shows the switch the core keeps, on by default, and turning it off is kept by the core', async () => {
    const sent: NotifyPrefs[] = [];
    let kept: NotifyPrefs = { needsYou: true, sound: true };
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
    await waitFor(() => expect(sent).toEqual([{ needsYou: false, sound: true }]), r);
    await waitFor(() => expect(sw.getAttribute('aria-checked')).toBe('false'), r);
  });

  it('shows "Play a sound" under the switch, on by default; turning it off is kept by the core, and it waits while the switch is off', async () => {
    const sent: NotifyPrefs[] = [];
    let kept: NotifyPrefs = { needsYou: true, sound: true };
    const s = testServices({
      notifyGet: async () => kept,
      notifySet: async (p) => {
        sent.push(p);
        kept = p;
        return p;
      },
    });
    const r = mount(<NotifySection />, s);
    const switches = await waitFor(() => {
      const els = r.container.querySelectorAll<HTMLButtonElement>('[data-testid="notify"] button');
      if (els.length < 2) throw new Error('no switches yet');
      return els;
    }, r);
    const labels = [...r.container.querySelectorAll('[data-testid="notify"] label')].map((l) => l.textContent);
    expect(labels).toEqual(['Tell me when something needs me', 'Play a sound']);
    const sound = switches[1]!;
    expect(sound.getAttribute('aria-checked')).toBe('true');
    sound.click();
    await waitFor(() => expect(sent).toEqual([{ needsYou: true, sound: false }]), r);
    await waitFor(() => expect(sound.getAttribute('aria-checked')).toBe('false'), r);
    switches[0]!.click();
    await waitFor(() => expect(sent.at(-1)).toEqual({ needsYou: false, sound: false }), r);
    await waitFor(() => expect(sound.disabled || sound.getAttribute('aria-disabled') === 'true').toBe(true), r);
  });
});
