import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { CheckSettings } from '../../api/extTypes.ts';
import type { Client } from '../../api/client.ts';
import type { ScanSchedule } from '../../api/securityTypes.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { ScanRulesNote } from '../security/security-screen.tsx';
import { rulesWords } from '../security/security.ts';
import { SecuritySchedulePanel, setLine } from './project-security-schedule.tsx';

afterEach(cleanup);

const off: ScanSchedule = { projectId: 'prj_1', afterMerge: false, afterSync: false, weekly: false };

function toggle(r: ReturnType<typeof mount>, label: string): HTMLInputElement {
  const l = [...r.container.querySelectorAll('label')].find((x) => x.textContent === label);
  if (!l) throw new Error(`no ${label}`);
  return r.container.querySelector<HTMLInputElement>(`#${l.getAttribute('for')}`)!;
}

function click(el: HTMLInputElement) {
  el.checked = !el.checked;
  el.dispatchEvent(new Event('change', { bubbles: true }));
}

describe('Security scan rules (project settings, H5)', () => {
  it('starts with every rule off, turns one on in one click, and turns on the scan of every task in one click', async () => {
    let sched = off;
    let checks: CheckSettings = { secrets: true, deps: false, depsOffline: false, scan: false };
    const sets: string[] = [];
    const client: Partial<Client> = {
      securityScheduleGet: async () => sched,
      securityScheduleSet: async (_pid, r) => {
        sets.push(JSON.stringify(r));
        sched = { ...sched, ...r, setBy: 'human:kyle', setAt: Date.UTC(2026, 10, 3, 9) };
        return sched;
      },
      checksSettingsGet: async () => checks,
      checksSettingsSet: async (_pid, c) => {
        checks = c;
        return c;
      },
    };
    const r = mount(<SecuritySchedulePanel projectId="prj_1" />, testServices(client));
    await waitFor(() => expect(toggle(r, 'After a task is merged').checked).toBe(false), r);
    expect(toggle(r, 'After a sync brings new commits').checked).toBe(false);
    expect(toggle(r, 'Once a week').checked).toBe(false);
    expect(r.container.textContent).toContain('osv.dev');
    expect(r.$('[data-testid="scan-schedule-set"]')).toBe(null);

    click(toggle(r, 'After a task is merged'));
    await waitFor(() => expect(r.$('[data-testid="scan-schedule-set"]')!.textContent).toContain('Set by kyle'), r);
    expect(sets).toEqual([JSON.stringify({ afterMerge: true, afterSync: false, weekly: false })]);
    expect(toggle(r, 'After a task is merged').checked).toBe(true);

    click(toggle(r, "Also run the scan on every task's change"));
    await waitFor(() => expect(checks.scan).toBe(true), r);
    expect(checks.secrets).toBe(true); // the other checks are kept as they were
  });

  it('says which rule waits and what the last rule scan found', async () => {
    const r = mount(
      <SecuritySchedulePanel projectId="prj_1" />,
      testServices({
        securityScheduleGet: async () => ({
          ...off,
          weekly: true,
          setBy: 'human:kyle',
          waiting: 'weekly',
          waitingWhy: 'An agent is working in the project folder.',
          last: { id: 'scn_1', projectId: 'prj_1', status: 'done', by: 'human:kyle', trigger: 'after-merge', startedAt: 1, counts: { critical: 0, high: 1, medium: 0, low: 0, info: 0, total: 1 }, summary: 'Scanned in 2 s: 1 secret' },
        }),
        checksSettingsGet: async () => ({ secrets: true, deps: false, depsOffline: false, scan: true }),
      }),
    );
    await waitFor(() => expect(r.$('[data-testid="scan-schedule-waiting"]')!.textContent).toContain('The scan weekly is waiting'), r);
    expect(r.$('[data-testid="scan-schedule-last"]')!.textContent).toContain('after a merge');
    expect(r.$('[data-testid="scan-schedule-last"]')!.textContent).toContain('1 secret');
  });

  it('names the rules on the Security page, or says it runs when Scan is pressed', async () => {
    expect(rulesWords({ afterMerge: true, afterSync: false, weekly: true })).toBe('after a merge and weekly');
    expect(rulesWords(off)).toBe('');
    expect(setLine(off)).toBe('');
    let r = mount(<ScanRulesNote projectId="prj_1" />, testServices({ securityScheduleGet: async () => off }));
    await waitFor(() => expect(r.$('[data-testid="scan-rules-note"]')!.textContent).toContain('Runs when you press Scan'), r);
    cleanup();
    r = mount(<ScanRulesNote projectId="prj_1" />, testServices({ securityScheduleGet: async () => ({ ...off, afterSync: true }) }));
    await waitFor(() => expect(r.$('[data-testid="scan-rules-note"]')!.textContent).toContain('Also runs after a sync with new commits'), r);
  });
});
