import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { StartReport } from '../../api/types.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { StartReportLine, startSentence, worthSaying } from './start-report.tsx';

afterEach(() => {
  cleanup();
  try {
    localStorage.removeItem('drydock.startReport.dismissed');
  } catch {
    /* nothing kept */
  }
});

const found = (r: { $: (sel: string) => Element | null }, sel: string) => () => {
  const el = r.$(sel);
  if (!el) throw new Error(`no ${sel} yet`);
  return el as HTMLElement;
};

const report = (over: Partial<StartReport> = {}): StartReport => ({ startedAt: 1000, unexpected: false, resumed: [], notResumed: [], ...over });

// TEST DOUBLES only: a partial client whose app.info carries the start report.
function show(r: StartReport | undefined) {
  const s = testServices({ appInfo: async () => ({ name: 'Drydock', user: 'k', human: 'human:k', dataDir: '/d', startReport: r }) as never, projectList: async () => [], licenseStatus: async () => ({}) as never });
  s.workspace.start();
  return mount(<StartReportLine />, s);
}

describe('StartReportLine (X511)', () => {
  it('says the last stop was unexpected, what was resumed and what could not be, with links, until dismissed for this start', async () => {
    const r = show(
      report({
        unexpected: true,
        crashes: 1,
        agentRunsClosed: 2,
        resumed: [{ runId: 'run_a', taskId: 't1', title: 'Add Multiply' }],
        notResumed: [{ runId: 'run_b', taskId: 't2', title: 'Add Divide' }],
      }),
    );
    const line = await waitFor(found(r, '[data-testid="start-report"]'), r);
    expect(line.textContent).toContain('Drydock was closed unexpectedly; 1 run was resumed, 1 run could not be resumed, 2 agent turns were stopped.');
    expect(r.$('[data-testid="start-report-resumed"] a')!.getAttribute('href')).toBe('/task/t1/run/run_a');
    expect(r.$('[data-testid="start-report-not-resumed"] a')!.textContent).toBe('Add Divide');
    expect(byText(line, 'a', 'The crash report is on the Support page.').getAttribute('href')).toBe('/support');
    await r.act(() => byText(line, 'button', 'Dismiss this note about the last stop').click());
    expect(r.$('[data-testid="start-report"]')).toBe(null);
    expect(localStorage.getItem('drydock.startReport.dismissed')).toBe('1000');
    cleanup();

    // the next start has its own report: said again
    const r2 = show(report({ startedAt: 2000, resumed: [{ runId: 'run_c', taskId: 't3', title: 'Retry' }] }));
    const line2 = await waitFor(found(r2, '[data-testid="start-report"]'), r2);
    expect(line2.textContent).toContain('Drydock started again; 1 run was resumed.');
    expect(line2.textContent).not.toContain('unexpectedly');
  });

  it('names the runs still being resumed, and re-reads the report when the core says it changed (X511 review)', async () => {
    let rep = report({ unexpected: true, resuming: [{ runId: 'run_k', taskId: 't9', title: 'Killed mid-run' }] });
    let reads = 0;
    const s = testServices({ appInfo: async () => (reads++, { name: 'Drydock', user: 'k', human: 'human:k', dataDir: '/d', startReport: rep }) as never, projectList: async () => [], licenseStatus: async () => ({}) as never });
    s.workspace.start();
    const r = mount(<StartReportLine />, s);
    const line = await waitFor(found(r, '[data-testid="start-report"]'), r);
    expect(line.textContent).toContain('Drydock was closed unexpectedly; 1 run is being resumed.');
    expect(r.$('[data-testid="start-report-resuming"] a')!.getAttribute('href')).toBe('/task/t9/run/run_k');
    // the core took the run over once the dead process's hold ran out, and said so
    rep = report({ unexpected: true, resumed: [{ runId: 'run_k', taskId: 't9', title: 'Killed mid-run' }], resuming: [] });
    s.emit({ type: 'app.start_report' });
    await waitFor(() => expect(r.$('[data-testid="start-report"]')!.textContent).toContain('Drydock was closed unexpectedly; 1 run was resumed.'), r, 3000);
    expect(r.$('[data-testid="start-report-resuming"]')).toBe(null);
    expect(r.$('[data-testid="start-report-resumed"] a')!.getAttribute('href')).toBe('/task/t9/run/run_k');
    expect(reads).toBeGreaterThanOrEqual(2);
  });

  it('says nothing after a clean close that left nothing, or for an older core', async () => {
    expect(worthSaying(report())).toBe(false);
    expect(worthSaying(undefined)).toBe(false);
    expect(startSentence(report({ unexpected: true }))).toBe('Drydock was closed unexpectedly.');
    const r = show(report());
    await new Promise((res) => setTimeout(res, 50));
    expect(r.$('[data-testid="start-report"]')).toBe(null);
  });
});
