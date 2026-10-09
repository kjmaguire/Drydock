import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { BundlePreview } from '../../api/pkgTypes.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { SupportScreen } from './support-screen.tsx';

afterEach(cleanup);

const bundlePreview = (over: Partial<BundlePreview> = {}): BundlePreview => ({
  files: [
    { name: 'summary.json', size: 1200 },
    { name: 'logs/drydock.log', size: 3000 },
  ],
  report: {
    files: [
      { name: 'summary.json', secretsRedacted: 0 },
      { name: 'logs/drydock.log', secretsRedacted: 2, byRule: { 'generic-api-key': 2 }, identityTokens: { user: 3 } },
    ],
    totalSecretsRedacted: 2,
    byRule: { 'generic-api-key': 2 },
    notice: 'Scrubbing reduces but does not eliminate leakage of proprietary code. Review the file list and send the file yourself; Drydock never uploads it.',
  },
  notice: 'x',
  ...over,
});

const tid = (root: ParentNode, id: string) => {
  const el = root.querySelector<HTMLElement>(`[data-testid="${id}"]`);
  if (!el) throw new Error(`no ${id}`);
  return el;
};
const btn = (root: ParentNode, name: string) => byText<HTMLButtonElement>(root, 'button', name);
const noButton = (root: ParentNode, name: string) => !Array.from(root.querySelectorAll('button')).some((b) => (b.textContent ?? '').trim() === name);
const show = (client: Partial<Client>) => mount(<SupportScreen />, testServices(client));

describe('SupportScreen', () => {
  it('says it is never uploaded and previews files and the scrub report without writing anything', async () => {
    const previews: unknown[] = [];
    let creates = 0;
    const r = show({ supportbundlePreview: async (o) => (previews.push(o), bundlePreview()), supportbundleCreate: async () => (creates++, null as never) });
    expect(tid(r.container, 'bundle-never-uploaded').textContent).toContain('never uploads');
    expect(r.$('h1')!.textContent).toContain('Support bundle');
    await r.act(() => btn(r.container, 'Preview the bundle').click());
    const pv = await waitFor(() => tid(r.container, 'bundle-preview'), r);
    expect(pv.textContent).toContain('Nothing has been written yet');
    expect(Array.from(r.container.querySelectorAll('[data-testid="bundle-file"]')).map((x) => x.textContent)).toEqual(['summary.json1.2 KB', 'logs/drydock.log2.9 KB']);
    expect(tid(r.container, 'scrub-total').textContent).toBe('2');
    expect(pv.textContent).toContain('generic-api-key');
    // only the file where something was replaced gets a row in the per-file table
    const perFile = pv.querySelector('table[aria-label="Per-file scrub results"]')!;
    expect(perFile.querySelectorAll('tbody tr').length).toBe(1);
    expect(perFile.textContent).toContain('user: 3');
    expect(creates).toBe(0);
    expect(previews).toEqual([{ eventLimit: undefined, includeRun: undefined }]);
  });

  it('creates the bundle and reports where it was written', async () => {
    const creates: unknown[] = [];
    const r = show({
      supportbundlePreview: async () => bundlePreview(),
      supportbundleCreate: async (o) => (creates.push(o), { path: 'C:\\data\\support\\b.zip', size: 4000, files: bundlePreview().files, report: bundlePreview().report, notice: 'x' }),
    });
    expect(noButton(r.container, 'Create the bundle')).toBe(true);
    await r.act(() => btn(r.container, 'Preview the bundle').click());
    await waitFor(() => tid(r.container, 'bundle-preview'), r);
    await r.act(() => btn(r.container, 'Create the bundle').click());
    expect((await waitFor(() => tid(r.container, 'bundle-path'), r)).textContent).toBe('C:\\data\\support\\b.zip');
    expect(creates).toEqual([{ eventLimit: undefined, includeRun: undefined, dest: undefined, acknowledgeRun: false }]);
    expect(tid(r.container, 'bundle-created').textContent).toContain('not been uploaded');
    expect(tid(r.container, 'bundle-created').textContent).toContain('2 file(s)');
  });

  it('makes including a run opt-in: shows the scan result first and needs an acknowledgement', async () => {
    const creates: unknown[] = [];
    const previews: unknown[] = [];
    const r = show({
      supportbundlePreview: async (o) => (previews.push(o), bundlePreview({ warning: 'Including a run adds its events with payloads and its raw output.', includesRun: 'run_1' })),
      supportbundleCreate: async (o) => (creates.push(o), { path: 'b.zip', size: 1, files: null, report: bundlePreview().report, notice: 'x' }),
    });
    await r.act(() => type(byLabel(r.container, /Include one run/), ' run_1 '));
    await r.act(() => type(byLabel(r.container, /Recent events to list/), '200'));
    await r.act(() => btn(r.container, 'Preview the bundle').click());
    expect((await waitFor(() => tid(r.container, 'bundle-run-warning'), r)).textContent).toContain('raw output');
    expect(previews).toEqual([{ eventLimit: 200, includeRun: 'run_1' }]);
    expect(btn(r.container, 'Create the bundle').disabled).toBe(true);
    await r.act(() => byLabel(r.container, /I have read the scrub report/).click());
    expect(btn(r.container, 'Create the bundle').disabled).toBe(false);
    await r.act(() => btn(r.container, 'Create the bundle').click());
    await waitFor(() => expect(creates).toEqual([{ eventLimit: 200, includeRun: 'run_1', dest: undefined, acknowledgeRun: true }]), r);
    // changing the run id makes the preview stale: create goes away until previewed again
    await r.act(() => type(byLabel(r.container, /Include one run/), 'run_2'));
    expect(tid(r.container, 'bundle-preview').getAttribute('data-fresh')).toBe('false');
    expect(tid(r.container, 'bundle-preview').textContent).toContain('Preview again before creating');
    expect(noButton(r.container, 'Create the bundle')).toBe(true);
  });

  it('sends the chosen destination', async () => {
    const creates: unknown[] = [];
    const r = show({
      supportbundlePreview: async () => bundlePreview(),
      supportbundleCreate: async (o) => (creates.push(o), { path: 'D:\\x.zip', size: 1, files: [], report: bundlePreview().report, notice: 'x' }),
    });
    await r.act(() => btn(r.container, 'Preview the bundle').click());
    await waitFor(() => tid(r.container, 'bundle-preview'), r);
    await r.act(() => type(byLabel(r.container, /^Save to/), ' D:\\x.zip '));
    await r.act(() => btn(r.container, 'Create the bundle').click());
    await waitFor(() => expect(creates).toEqual([{ eventLimit: undefined, includeRun: undefined, dest: 'D:\\x.zip', acknowledgeRun: false }]), r);
  });

  it('shows the core refusal for an unknown run', async () => {
    const r = show({
      supportbundlePreview: async () => {
        throw new Error('there is no run run_x');
      },
    });
    await r.act(() => type(byLabel(r.container, /Include one run/), 'run_x'));
    await r.act(() => btn(r.container, 'Preview the bundle').click());
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('there is no run run_x'), r);
  });
});
