import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { BundleCreated, BundlePreview, BundleResult } from '../../api/pkgTypes.ts';
import { byLabel, byText, cleanup, errorOf, mount, testServices, type, waitFor } from '../../test/render.tsx';
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

// what the core writes: not the preview's report, so a test can tell which of the two the page shows
const bundleWritten = (over: Partial<BundleCreated> = {}): BundleCreated => ({
  path: 'C:\\data\\support\\b.zip',
  size: 4000,
  files: [
    { name: 'summary.json', size: 1200 },
    { name: 'logs/drydock.log', size: 3000 },
    { name: 'settings.json', size: 800 },
  ],
  report: {
    files: [
      { name: 'summary.json', secretsRedacted: 0 },
      { name: 'logs/drydock.log', secretsRedacted: 5, byRule: { 'aws-access-token': 5 }, identityTokens: { host: 1 } },
      { name: 'settings.json', secretsRedacted: 0 },
    ],
    totalSecretsRedacted: 5,
    byRule: { 'aws-access-token': 5 },
    refused: [{ name: 'logs/old.log', reason: 'it looked like a private key, so it was left out' }],
    notice: 'Scrubbing reduces but does not eliminate leakage of proprietary code.',
  },
  notice: 'x',
  ...over,
});

/** The element with the test id; with `optional` the answer may be null, otherwise a missing element is an error (so waitFor retries). */
function tid(root: ParentNode, id: string): HTMLElement;
function tid(root: ParentNode, id: string, optional: true): HTMLElement | null;
function tid(root: ParentNode, id: string, optional = false): HTMLElement | null {
  const el = root.querySelector<HTMLElement>(`[data-testid="${id}"]`);
  if (!el && !optional) throw new Error(`no ${id}`);
  return el;
}
const btn = (root: ParentNode, name: string) => byText<HTMLButtonElement>(root, 'button', name);
const noButton = (root: ParentNode, name: string) => !Array.from(root.querySelectorAll('button')).some((b) => (b.textContent ?? '').trim() === name);
const show = (client: Partial<Client>) => mount(<SupportScreen />, testServices({ crashList: async () => ({ reports: [] }), ...client }));

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
    expect(tid(r.container, 'bundle-created').textContent).toContain('2 files');
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

  it('shows what was written: the files in the bundle, its own scrub report with the files left out, and nothing about a replaced file when none was', async () => {
    const r = show({ supportbundlePreview: async () => bundlePreview(), supportbundleCreate: async () => bundleWritten() });
    await r.act(() => btn(r.container, 'Preview the bundle').click());
    await waitFor(() => tid(r.container, 'bundle-preview'), r);
    await r.act(() => btn(r.container, 'Create the bundle').click());
    const w = await waitFor(() => tid(r.container, 'bundle-written'), r);
    expect(tid(r.container, 'bundle-path').textContent).toBe('C:\\data\\support\\b.zip');
    expect(tid(r.container, 'bundle-created').textContent).toContain('(3.9 KB, 3 files)');
    expect(tid(r.container, 'bundle-replaced', true)).toBeNull();
    // the files that are in the bundle, with their sizes
    expect(Array.from(w.querySelectorAll('[data-testid="bundle-written-file"]')).map((x) => x.textContent)).toEqual(['summary.json1.2 KB', 'logs/drydock.log2.9 KB', 'settings.json800 B']);
    // the report of the bundle that was written: its own count, the rule that fired, the file where it did and the file left out
    const report = tid(w, 'written-scrub-report');
    expect(tid(report, 'written-scrub-total').textContent).toBe('5');
    expect(report.textContent).toContain('aws-access-token');
    expect(report.querySelector('table[aria-label="Per-file scrub results"]')!.textContent).toContain('logs/drydock.log');
    expect(report.textContent).toContain('Left out of the bundle:');
    expect(report.textContent).toContain('logs/old.log: it looked like a private key, so it was left out');
    // the preview above it is the preview: its report is not replaced by the written one
    expect(tid(tid(r.container, 'bundle-preview'), 'scrub-total').textContent).toBe('2');
    expect(r.container.querySelectorAll('[data-testid="scrub-report"]').length).toBe(1);
  });

  describe('a file is already at the destination (L39)', () => {
    /** The core's answers, as a test double: a file is there until a request carries overwrite. */
    function rig(over: { size?: number } = {}) {
      const creates: Record<string, unknown>[] = [];
      const r = show({
        supportbundlePreview: async () => bundlePreview(),
        supportbundleCreate: async (o): Promise<BundleResult> => {
          creates.push(o as Record<string, unknown>);
          if (!o.overwrite) return { exists: true, path: 'C:\\data\\support\\b.zip', size: over.size ?? 4096 };
          return bundleWritten({ replaced: true });
        },
      });
      return { r, creates };
    }
    const create = async (r: ReturnType<typeof show>) => {
      await r.act(() => btn(r.container, 'Create the bundle').click());
    };
    const previewed = async (r: ReturnType<typeof show>) => {
      await r.act(() => btn(r.container, 'Preview the bundle').click());
      await waitFor(() => tid(r.container, 'bundle-preview'), r);
    };

    it('writes nothing and shows nothing as created, but asks which file is there and whether it may be replaced', async () => {
      const { r, creates } = rig();
      await previewed(r);
      await create(r);
      const ask = await waitFor(() => tid(r.container, 'bundle-exists'), r);
      expect(ask.getAttribute('role')).toBe('alert');
      expect(tid(ask, 'bundle-exists-path').textContent).toBe('C:\\data\\support\\b.zip');
      expect(ask.textContent).toBe('A file is already there: C:\\data\\support\\b.zip (4.0 KB). Replace it?Yes, replace itKeep it');
      expect(Array.from(ask.querySelectorAll('button')).map((b) => b.textContent!.trim())).toEqual(['Yes, replace it', 'Keep it']);
      // the page does not say a bundle was created, and the first request did not carry overwrite
      expect(tid(r.container, 'bundle-created', true)).toBeNull();
      expect(tid(r.container, 'bundle-written', true)).toBeNull();
      expect(creates).toEqual([{ eventLimit: undefined, includeRun: undefined, dest: undefined, acknowledgeRun: false }]);
      expect('overwrite' in creates[0]!).toBe(false);
    });

    it('replaces the file only after "Yes, replace it": the request is sent again with overwrite, and the page says it replaced the earlier file', async () => {
      const { r, creates } = rig();
      await previewed(r);
      await create(r);
      await waitFor(() => tid(r.container, 'bundle-exists'), r);
      await r.act(() => btn(r.container, 'Yes, replace it').click());
      await waitFor(() => tid(r.container, 'bundle-created'), r);
      expect(creates).toEqual([
        { eventLimit: undefined, includeRun: undefined, dest: undefined, acknowledgeRun: false },
        { eventLimit: undefined, includeRun: undefined, dest: undefined, acknowledgeRun: false, overwrite: true },
      ]);
      expect(tid(r.container, 'bundle-replaced').textContent).toBe('It replaced the earlier file.');
      expect(tid(r.container, 'bundle-exists', true)).toBeNull();
      expect(tid(r.container, 'written-scrub-total').textContent).toBe('5');
      // the keyboard is back on the button that asked, not lost with the button that answered
      await waitFor(() => expect(document.activeElement).toBe(tid(r.container, 'bundle-create')), r);
    });

    it('"Keep it" sends nothing, says nothing was written and where the file stays, and puts the keyboard back on Create the bundle', async () => {
      const { r, creates } = rig();
      await previewed(r);
      await create(r);
      await waitFor(() => tid(r.container, 'bundle-exists'), r);
      await r.act(() => btn(r.container, 'Keep it').click());
      expect(creates.length).toBe(1);
      expect(tid(r.container, 'bundle-exists', true)).toBeNull();
      const kept = tid(r.container, 'bundle-kept');
      expect(kept.getAttribute('role')).toBe('status');
      expect(kept.textContent).toContain('Nothing was written. The file at C:\\data\\support\\b.zip was left as it is.');
      expect(tid(r.container, 'bundle-created', true)).toBeNull();
      await waitFor(() => expect(document.activeElement).toBe(tid(r.container, 'bundle-create')), r);
      // asking again is a new question, not an answer remembered: the note goes and the question comes back, without overwrite
      await create(r);
      await waitFor(() => tid(r.container, 'bundle-exists'), r);
      expect(tid(r.container, 'bundle-kept', true)).toBeNull();
      expect(creates.length).toBe(2);
      expect('overwrite' in creates[1]!).toBe(false);
    });

    it('drops the question when the destination, the events or the acknowledgement change, and the next request does not carry overwrite', async () => {
      const { r, creates } = rig();
      await previewed(r);
      await create(r);
      await waitFor(() => tid(r.container, 'bundle-exists'), r);
      // another destination: the question was about the first one
      await r.act(() => type(byLabel(r.container, /^Save to/), 'D:\\other.zip'));
      expect(tid(r.container, 'bundle-exists', true)).toBeNull();
      await create(r);
      await waitFor(() => tid(r.container, 'bundle-exists'), r);
      expect(creates.map((c) => c.dest)).toEqual([undefined, 'D:\\other.zip']);
      expect(creates.some((c) => 'overwrite' in c)).toBe(false);
      // other events: the preview is stale and the question goes; typing the same text back makes the preview fresh again, and the
      // question must not come back with it
      await r.act(() => type(byLabel(r.container, /Recent events to list/), '7'));
      expect(tid(r.container, 'bundle-exists', true)).toBeNull();
      await r.act(() => type(byLabel(r.container, /Recent events to list/), ''));
      expect(tid(r.container, 'bundle-preview').getAttribute('data-fresh')).toBe('true');
      expect(tid(r.container, 'bundle-exists', true)).toBeNull();
      expect(tid(r.container, 'bundle-kept', true)).toBeNull();
    });

    it('drops the question when the acknowledgement of a run is taken back or the run changes', async () => {
      const creates: Record<string, unknown>[] = [];
      const r = show({
        supportbundlePreview: async () => bundlePreview({ includesRun: 'run_1', warning: 'Including a run adds its events with payloads and its raw output.' }),
        supportbundleCreate: async (o): Promise<BundleResult> => (creates.push(o as Record<string, unknown>), { exists: true, path: 'C:\\data\\support\\b.zip', size: 10 }),
      });
      await r.act(() => type(byLabel(r.container, /Include one run/), 'run_1'));
      await previewed(r);
      await r.act(() => byLabel(r.container, /I have read the scrub report/).click());
      await create(r);
      await waitFor(() => tid(r.container, 'bundle-exists'), r);
      expect(creates).toEqual([{ eventLimit: undefined, includeRun: 'run_1', dest: undefined, acknowledgeRun: true }]);
      await r.act(() => byLabel(r.container, /I have read the scrub report/).click());
      expect(tid(r.container, 'bundle-exists', true)).toBeNull();
      // another run makes the preview stale; the same run typed back makes it fresh again, and the question must not come back with it
      await r.act(() => byLabel(r.container, /I have read the scrub report/).click());
      await create(r);
      await waitFor(() => tid(r.container, 'bundle-exists'), r);
      await r.act(() => type(byLabel(r.container, /Include one run/), 'run_2'));
      expect(tid(r.container, 'bundle-exists', true)).toBeNull();
      await r.act(() => type(byLabel(r.container, /Include one run/), 'run_1'));
      expect(tid(r.container, 'bundle-preview').getAttribute('data-fresh')).toBe('true');
      expect(tid(r.container, 'bundle-exists', true)).toBeNull();
    });

    it('a file that cannot be replaced is the core\'s refusal, shown as an error, and the question is gone', async () => {
      const r = show({
        supportbundlePreview: async () => bundlePreview(),
        supportbundleCreate: async (o): Promise<BundleResult> => {
          if (!o.overwrite) return { exists: true, path: 'C:\\data\\support\\b.zip', size: 10 };
          throw new Error('C:\\data\\support\\b.zip is open in another program');
        },
      });
      await previewed(r);
      await create(r);
      await waitFor(() => tid(r.container, 'bundle-exists'), r);
      await r.act(() => btn(r.container, 'Yes, replace it').click());
      await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('is open in another program'), r);
      expect(tid(r.container, 'bundle-exists', true)).toBeNull();
      expect(tid(r.container, 'bundle-created', true)).toBeNull();
    });
  });

  it('refuses events text that is not a whole number under the field, asks the core for nothing, and takes the keyboard to the field (M25)', async () => {
    const previews: unknown[] = [];
    const r = show({ supportbundlePreview: async (o) => (previews.push(o), bundlePreview()) });
    const field = () => byLabel(r.container, /Recent events to list/);
    for (const text of ['5OO', '1,000', '2.5', '-3', '0', '1e3', '$5']) {
      await r.act(() => type(field(), text));
      await r.act(() => btn(r.container, 'Preview the bundle').click());
      await waitFor(() => expect(errorOf(field())).toContain('whole number greater than zero'), r);
      expect(document.activeElement).toBe(field());
      expect(r.container.querySelector('[data-testid="bundle-preview"]')).toBeNull();
    }
    expect(previews).toEqual([]);
    // typing again clears the message at once; a whole number is sent as it is, and so is an empty field (the core's default)
    await r.act(() => type(field(), ' 200 '));
    expect(errorOf(field())).toBeNull();
    await r.act(() => btn(r.container, 'Preview the bundle').click());
    await waitFor(() => tid(r.container, 'bundle-preview'), r);
    await r.act(() => type(field(), ''));
    await r.act(() => btn(r.container, 'Preview the bundle').click());
    await waitFor(() => expect(previews.length).toBe(2), r);
    expect(previews).toEqual([
      { eventLimit: 200, includeRun: undefined },
      { eventLimit: undefined, includeRun: undefined },
    ]);
  });

  it('lists the crash reports kept on this computer, says they go into the bundle, and deletes one (review 2026-10-27)', async () => {
    let reports = [{ name: 'crash-20261027T090000Z-100.txt', time: '2026-10-27T09:00:00Z', size: 2048, summary: 'panic: runtime error: index out of range [3] with length 3' }];
    const deleted: string[] = [];
    const r = show({
      crashList: async () => ({ reports }),
      crashDelete: async (name) => (deleted.push(name), (reports = []), { deleted: true }),
    });
    const group = await waitFor(() => tid(r.container, 'crash-reports'), r);
    expect(group.textContent).toContain('index out of range');
    expect(group.textContent).toContain('support bundle');
    expect(r.container.querySelectorAll('[data-testid="crash-report"]').length).toBe(1);
    await r.act(() => group.querySelector<HTMLButtonElement>('button[aria-label^="Delete the crash report"]')!.click());
    await waitFor(() => {
      if (tid(r.container, 'crash-reports', true)) throw new Error('still listed');
    }, r);
    expect(deleted).toEqual(['crash-20261027T090000Z-100.txt']);
  });

  it('draws no crash group when there is no report', async () => {
    const r = show({});
    await waitFor(() => btn(r.container, 'Preview the bundle'), r);
    await r.act(() => undefined);
    expect(tid(r.container, 'crash-reports', true)).toBe(null);
  });
});
