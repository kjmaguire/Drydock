// A page, the dock or the details panel that throws while it draws shows what failed in its own region (audit 2026-10-16, H11):
// the top bar and the rest of the shell keep working, and the person can try again or reload.
import { expect } from 'remix/assert';
import { on, type Handle } from 'remix/component';
import { afterEach, describe, it } from 'remix/test';
import { lic } from '../test/fixtures.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { Services } from '../state/services.tsx';
import { Shell } from './shell.tsx';

afterEach(cleanup);

// TEST DOUBLE: a routed screen that throws while it draws, as long as `broken.on` is set.
const broken = { on: true };
function BrokenScreen(handle: Handle) {
  return () => {
    if (broken.on) throw new TypeError("Cannot read properties of undefined (reading 'map')");
    return (
      <section data-testid="screen">
        <button data-testid="inside" mix={on('click', () => void handle.update())}>
          screen is fine
        </button>
      </section>
    );
  };
}

function services() {
  const s = testServices({
    projectList: async () => [],
    licenseStatus: async () => lic(),
    appInfo: async () => ({}) as never,
    chatList: async () => [],
  });
  s.workspace.start();
  return s;
}

describe('the page region of the shell', () => {
  it('shows the error where the page would be, keeps the top bar, and draws the page after Try again', async () => {
    broken.on = true;
    const s = services();
    const r = mount(
      <Shell url={new URL('http://localhost/settings')}>
        <BrokenScreen />
      </Shell>,
      s,
    );
    // the shell was drawn although its page threw: before the boundary the whole window stayed blank
    const main = await waitFor(() => {
      const m = r.$('main#main');
      if (!m?.querySelector('[data-testid="region-error"]')) throw new Error('no panel yet');
      return m;
    }, r);
    expect(main.querySelector('[data-testid="region-error"]')!.textContent).toContain('Could not show this page');
    expect(main.querySelector('[data-testid="region-error-text"]')!.textContent).toBe("TypeError: Cannot read properties of undefined (reading 'map')");
    expect(r.$('nav[aria-label="Main"]')).not.toBeNull(); // the top bar
    expect(byText(main, 'button', 'Reload Drydock')).not.toBeNull();
    // fixed meanwhile: Try again
    broken.on = false;
    await r.act(() => byText(main, 'button', 'Try again').click());
    await waitFor(() => expect(r.$('main#main [data-testid="screen"]')).not.toBeNull(), r);
    expect(r.$('[data-testid="region-error"]')).toBeNull();
  });

  it('leaves the next page alone: navigating away from a failed page draws the next one', async () => {
    broken.on = true;
    const s = services();
    const r = mount(
      <Shell url={new URL('http://localhost/settings')}>
        <BrokenScreen />
      </Shell>,
      s,
    );
    await waitFor(() => expect(r.$('main#main [data-testid="region-error"]')).not.toBeNull(), r);
    r.root.render(
      <Services value={s}>
        <Shell url={new URL('http://localhost/license')}>
          <p data-testid="next-page">the next page</p>
        </Shell>
      </Services>,
    );
    await waitFor(() => expect(r.$('main#main [data-testid="next-page"]')).not.toBeNull(), r);
    expect(r.$('[data-testid="region-error"]')).toBeNull();
  });
});
