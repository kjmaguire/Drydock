import { expect } from 'remix/assert';
import { afterEach, beforeEach, describe, it } from 'remix/test';
import type { LicenseView } from '../api/pkgTypes.ts';
import { expiredLicense, lic, unconfiguredLicense } from '../test/fixtures.ts';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { LicenseBanner } from './license-banner.tsx';

afterEach(cleanup);
beforeEach(() => localStorage.removeItem('drydock.licenseBannerDismissed'));

const grace = (daysLeft: number) => lic({ state: 'grace', daysLeft, banner: `License expired; full functionality for ${daysLeft} more grace day(s).` });

function banner(view: LicenseView) {
  const s = testServices({ licenseStatus: async () => view });
  s.workspace.start();
  const r = mount(<LicenseBanner />, s);
  return { r, s, el: () => r.container.querySelector<HTMLElement>('[data-testid="license-banner"]') ?? undefined };
}

function need(el: HTMLElement | undefined): HTMLElement {
  if (!el) throw new Error('no license banner');
  return el;
}

/** Lets the license load land: the banner is rendered from the workspace's license state. */
async function settled(b: ReturnType<typeof banner>) {
  await waitFor(() => (b.s.workspace.license.view ? true : null), b.r);
}

describe('LicenseBanner', () => {
  it('shows nothing in a build without licensing: nothing is restricted', async () => {
    const b = banner(unconfiguredLicense);
    await settled(b);
    expect(b.el()).toBe(undefined);
  });

  it('a grace banner can be dismissed, and comes back when the days left change', async () => {
    const b = banner(grace(5));
    const el = await waitFor(() => need(b.el()), b.r);
    expect(el.textContent).toContain('5 more grace day(s)');
    const close = el.querySelector<HTMLButtonElement>('button[aria-label="Dismiss the license message"]')!;
    close.click();
    await waitFor(() => (b.el() ? null : true), b.r);
    cleanup();
    // the same state after a reload stays dismissed
    const again = banner(grace(5));
    await settled(again);
    expect(again.el()).toBe(undefined);
    cleanup();
    // one day later it shows again
    const later = banner(grace(4));
    expect((await waitFor(() => need(later.el()), later.r)).textContent).toContain('4 more grace day(s)');
  });

  it('a read-only banner cannot be dismissed: it says why controls are off', async () => {
    const b = banner(expiredLicense);
    const el = await waitFor(() => need(b.el()), b.r);
    expect(el.getAttribute('role')).toBe('alert');
    expect(el.querySelector('button')).toBeNull();
  });
});
