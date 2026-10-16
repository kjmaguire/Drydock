import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { EvalsOptions } from '../../api/extTypes.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { EvalsScreen } from './evals-screen.tsx';

afterEach(cleanup);

function servicesWith(client: Partial<Client>) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as never],
    licenseStatus: async () => undefined as never,
    appInfo: async () => ({}) as never,
    ...client,
  });
  s.workspace.start();
  return s;
}

const need = (r: { $: (sel: string) => Element | null }, sel: string) => {
  const el = r.$(sel);
  if (!el) throw new Error(`no ${sel}`);
  return el as HTMLElement;
};

const report = () => ({
  markdown: '## Cost per accepted task\n\n| Group | Accepted | Cost |\n|---|---|---|\n| premium | 0 | n/a |\n\n<script>window.__xss=1</script>',
  report: {
    quality: { started: 2, accepted: 0, failed: 1, cancelled: 0, active: 1, completion: { num: 0, den: 2, rate: 0 }, firstCheckTestPass: { num: 0, den: 0 }, finalTestPass: { num: 0, den: 0 } },
  },
});

describe('EvalsScreen', () => {
  it('renders the markdown tables inert, the headline with n/a, and exports locally', async () => {
    const asked: (EvalsOptions | undefined)[] = [];
    const exports: (EvalsOptions | undefined)[] = [];
    const r = mount(
      <EvalsScreen />,
      servicesWith({
        evalsReport: async (o) => (asked.push(o), report()),
        evalsExport: async (o) => (exports.push(o), { path: '/data/evals/evals-1.json', bytes: 123, sha256: 'abcdef0123456789abcdef', tasks: 2, accepted: 0 }),
      }),
    );
    const md = await waitFor(() => need(r, '[data-testid="evals-markdown"]'), r);
    expect(md.querySelector('table th')).not.toBeNull();
    expect(r.container.querySelector('script')).toBeNull();
    expect((window as unknown as { __xss?: number }).__xss).toBe(undefined);
    expect(need(r, '[data-testid="ratio-completion"]').textContent).toBe('0 of 2 (0%)');
    const headline = need(r, '[data-testid="evals-headline"]');
    expect(Array.from(headline.querySelectorAll('dd')).filter((d) => d.textContent === 'n/a').length).toBeGreaterThanOrEqual(2);
    expect(asked[0]).toEqual({ projectId: 'prj_1', sinceMs: undefined });

    await r.act(() => byText(r.container, 'button', 'Export to a local file').click());
    const out = await waitFor(() => need(r, '[data-testid="evals-exported"]'), r);
    expect(out.textContent).toContain('/data/evals/evals-1.json');
    expect(out.textContent).toContain('stays on this machine');
    expect(exports[0]).toEqual({ projectId: 'prj_1', sinceMs: undefined });
  });

  it('a zero denominator is n/a in the headline; the window and project filters re-read', async () => {
    const asked: (EvalsOptions | undefined)[] = [];
    const empty = { markdown: '# Evals', report: { quality: { started: 0, accepted: 0, failed: 0, cancelled: 0, active: 0, completion: { num: 0, den: 0 }, firstCheckTestPass: { num: 0, den: 0 }, finalTestPass: { num: 0, den: 0 } } } };
    const r = mount(<EvalsScreen />, servicesWith({ evalsReport: async (o) => (asked.push(o), empty) }));
    await waitFor(() => expect(need(r, '[data-testid="ratio-completion"]').textContent).toBe('n/a'), r);
    type(byLabel<HTMLElement>(r.container, 'Window'), '7');
    await waitFor(() => expect(asked.length).toBe(2), r);
    expect(asked[1]!.projectId).toBe('prj_1');
    expect(typeof asked[1]!.sinceMs).toBe('number');
    await r.act(() => byLabel(r.container, 'This project only').click());
    await waitFor(() => expect(asked.length).toBe(3), r);
    expect(asked[2]!.projectId).toBe(undefined);
  });

  it('shows the core error verbatim', async () => {
    const r = mount(
      <EvalsScreen />,
      servicesWith({
        evalsReport: async () => {
          throw new Error('evals: database is locked');
        },
      }),
    );
    await waitFor(() => expect(need(r, '[role="alert"]').textContent).toContain('evals: database is locked'), r);
  });
});
