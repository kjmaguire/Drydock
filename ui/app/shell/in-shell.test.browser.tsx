// The services and the shell around every route's content (router.tsx), and what a shell that fails to draw shows (audit
// 2026-10-16, H11): the window is never blank. Checked in the built app against a stub core as well; see the report.
import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { lic } from '../test/fixtures.ts';
import { byText, cleanup, renderPlain, testServices, waitFor } from '../test/render.tsx';
import { inShell } from './in-shell.tsx';

afterEach(cleanup);

// TEST DOUBLES: a partial client; `projects` is what project.list answers.
function services(projects: unknown[]) {
  const s = testServices({
    projectList: async () => projects as never,
    licenseStatus: async () => lic(),
    appInfo: async () => ({}) as never,
    chatList: async () => [],
  });
  s.workspace.start();
  return s;
}

describe('inShell', () => {
  it('draws the shell around the content of the route', async () => {
    const r = renderPlain(inShell(services([]), <p data-testid="content">the page</p>, new URL('http://localhost/settings')));
    await waitFor(() => expect(r.$('main#main [data-testid="content"]')!.textContent).toBe('the page'), r);
    expect(r.$('nav[aria-label="Main"]')).not.toBeNull();
    expect(r.$('[data-testid="region-error"]')).toBeNull();
  });

  it('shows the error, with Try again and Reload, when the shell itself throws on a malformed answer of the core', async () => {
    const real = console.error;
    console.error = () => {}; // the guard reports the error here as well; the panel is what is checked
    try {
      // a null in the list of projects, which the shell reads the id of
      const r = renderPlain(inShell(services([null]), <p data-testid="content">the page</p>, new URL('http://localhost/settings')));
      const panel = await waitFor(() => {
        const p = r.$('[data-testid="region-error"]');
        if (!p) throw new Error('no panel yet');
        return p;
      }, r);
      expect(panel.textContent).toContain('Could not show Drydock');
      expect(r.$('[data-testid="region-error-text"]')!.textContent).toContain("TypeError: Cannot read properties of null (reading 'id')");
      expect(byText(panel, 'button', 'Try again')).not.toBeNull();
      expect(byText(panel, 'button', 'Reload Drydock')).not.toBeNull();
    } finally {
      console.error = real;
    }
  });
});
