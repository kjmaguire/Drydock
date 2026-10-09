import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { LicenseView } from '../../api/pkgTypes.ts';
import { expiredLicense, lic, unconfiguredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { LicenseScreen } from './license-screen.tsx';

afterEach(cleanup);

const has = (root: ParentNode, sel: string) => root.querySelector(sel) !== null;
const tid = (root: ParentNode, id: string) => {
  const el = root.querySelector<HTMLElement>(`[data-testid="${id}"]`);
  if (!el) throw new Error(`no ${id}`);
  return el;
};
const buttons = (root: ParentNode, name: string) =>
  Array.from(root.querySelectorAll('button')).filter((b) => (b.getAttribute('aria-label') ?? b.textContent ?? '').trim() === name);

describe('LicenseScreen', () => {
  it('says plainly that licensing is not configured and offers no import', async () => {
    const r = mount(<LicenseScreen />, testServices({ licenseStatus: async () => unconfiguredLicense }));
    const box = await waitFor(() => tid(r.container, 'license-unconfigured'), r);
    expect(box.textContent).toContain('Licensing is not configured in this build');
    expect(box.textContent).toContain('Every feature is allowed');
    expect(() => byLabel(r.container, 'License token')).toThrow();
    expect(buttons(r.container, 'Import license').length).toBe(0);
    expect(r.$('h1')!.textContent).toContain('License');
  });

  it('shows state, days left and what the license entitles', async () => {
    const r = mount(<LicenseScreen />, testServices({ licenseStatus: async () => lic({ state: 'trial', kind: 'trial', daysLeft: 12, channels: ['stable', 'beta'] }) }));
    await waitFor(() => tid(r.container, 'license-details'), r);
    expect(tid(r.container, 'license-state').textContent).toBe('Trial');
    expect(tid(r.container, 'license-days').textContent).toContain('12 day(s)');
    expect(tid(r.container, 'license-details').textContent).toContain('stable, beta');
    expect(has(r.container, '[data-testid="license-readonly"]')).toBe(false);
  });

  it('shows grace days left', async () => {
    const r = mount(<LicenseScreen />, testServices({ licenseStatus: async () => lic({ state: 'grace', daysLeft: 5, message: 'License expired; full functionality for 5 more grace day(s).' }) }));
    await waitFor(() => tid(r.container, 'license-details'), r);
    expect(tid(r.container, 'license-state').textContent).toBe('Grace period');
    expect(Array.from(r.container.querySelectorAll('dt')).some((d) => d.textContent === 'Grace days left')).toBe(true);
    expect(tid(r.container, 'license-days').textContent).toContain('5 day(s)');
  });

  it('explains read-only: what is still usable and what is off', async () => {
    const r = mount(<LicenseScreen />, testServices({ licenseStatus: async () => expiredLicense }));
    const box = await waitFor(() => tid(r.container, 'license-readonly'), r);
    expect(box.textContent).toContain('Nothing has been deleted');
    const items = Array.from(box.querySelectorAll('li')).map((li) => li.textContent);
    expect(items).toContain('Export');
    expect(items).toContain('Starting tasks and workflows');
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Import license').disabled).toBe(false);
  });

  it('sends the token once, clears the field and never shows it back; the workspace license is re-read', async () => {
    const calls: unknown[] = [];
    let wsReads = 0;
    const s = testServices({
      licenseStatus: async () => (wsReads++, expiredLicense),
      licenseImport: async (req) => (calls.push(req), lic({ message: 'paid license valid for about 365 more day(s).' })),
    });
    const r = mount(<LicenseScreen />, s);
    const field = await waitFor(() => byLabel(r.container, 'License token'), r);
    expect(field.type).toBe('password');
    field.value = 'SECRET-TOKEN.abc';
    const before = wsReads;
    await r.act(() => byText(r.container, 'button', 'Import license').click());
    await waitFor(() => expect(calls).toEqual([{ token: 'SECRET-TOKEN.abc' }]), r);
    await waitFor(() => tid(r.container, 'license-done'), r);
    expect(field.value).toBe('');
    expect(document.body.textContent).not.toContain('SECRET-TOKEN');
    // the page and the workspace model (banner, read-only controls) both read the license again
    await waitFor(() => expect(wsReads).toBeGreaterThan(before + 1), r);
  });

  it('shows the core refusal verbatim and keeps the pasted token so a typo can be fixed', async () => {
    const r = mount(
      <LicenseScreen />,
      testServices({
        licenseStatus: async () => expiredLicense,
        licenseImport: async () => {
          throw new Error('this is not a Drydock license token');
        },
      }),
    );
    const field = await waitFor(() => byLabel(r.container, 'License token'), r);
    field.value = 'junk';
    await r.act(() => byText(r.container, 'button', 'Import license').click());
    const alert = await waitFor(() => {
      const a = r.container.querySelector('.error-note [role="alert"]');
      if (!a) throw new Error('no alert');
      return a;
    }, r);
    expect(alert.textContent).toContain('this is not a Drydock license token');
    expect(alert.textContent).toContain('License');
    expect(field.value).toBe('junk');
  });

  it('imports from a file path when no token is pasted, and removing needs a confirmation', async () => {
    const imports: unknown[] = [];
    let clears = 0;
    const r = mount(
      <LicenseScreen />,
      testServices({
        licenseStatus: async () => lic(),
        licenseImport: async (req) => (imports.push(req), lic()),
        licenseClear: async () => (clears++, expiredLicense),
      }),
    );
    type(await waitFor(() => byLabel(r.container, 'Or a license file path'), r), 'C:\\x\\a.drydock-license');
    await r.act(() => byText(r.container, 'button', 'Import license').click());
    await waitFor(() => expect(imports).toEqual([{ path: 'C:\\x\\a.drydock-license' }]), r);
    await r.act(() => byText(r.container, 'button', 'Remove the installed license').click());
    expect(clears).toBe(0);
    expect(r.container.textContent).toContain('Drydock becomes read-only until you import one');
    await r.act(() => byText(r.container, 'button', 'Yes, remove it').click());
    await waitFor(() => expect(clears).toBe(1), r);
  });

  it('keeping the license cancels the confirmation, and no license offers nothing to remove', async () => {
    const r = mount(<LicenseScreen />, testServices({ licenseStatus: async () => lic() }));
    await r.act(async () => (await waitFor(() => byText(r.container, 'button', 'Remove the installed license'), r)).click());
    await r.act(() => byText(r.container, 'button', 'Keep it').click());
    expect(buttons(r.container, 'Yes, remove it').length).toBe(0);
    cleanup();
    const none: LicenseView = lic({ state: 'none', readOnly: true, daysLeft: 0, message: 'No valid license.' });
    const r2 = mount(<LicenseScreen />, testServices({ licenseStatus: async () => none }));
    await waitFor(() => tid(r2.container, 'license-state'), r2);
    expect(tid(r2.container, 'license-state').textContent).toBe('No license');
    expect(has(r2.container, '[data-testid="license-days"]')).toBe(false);
    expect(buttons(r2.container, 'Remove the installed license').length).toBe(0);
  });

  it('warns when the clock was set back', async () => {
    const r = mount(<LicenseScreen />, testServices({ licenseStatus: async () => lic({ clockSuspect: true, lastSeen: 1760000000 }) }));
    await waitFor(() => tid(r.container, 'license-details'), r);
    expect(r.container.textContent).toContain('system clock appears to have been set back');
  });
});
