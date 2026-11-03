// When the project's security scan runs without the Scan button (lane B batch B7, H5): after a task's work is merged, after
// Sync now brings new commits, once a week (security.schedule.get/set). Every rule is off until the person turns it on: the
// scan's dependency engine sends package names and versions to osv.dev. A rule's scan waits while an agent works in the
// project's folder. New high findings of a rule's scan reach the Inbox. The last switch turns on the scan check of every
// task's change (checks.settings, the same switch as in Verification checks) in one click.

import { type Handle } from 'remix/component';
import type { ScanSchedule } from '../../api/securityTypes.ts';
import { formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { triggerWords } from '../security/security.ts';
import { action, query, services } from '../../state/services.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { SettingsGroup, ToggleRow, errorPad, noteRow } from './settings-kit.tsx';

type Rule = Pick<ScanSchedule, 'afterMerge' | 'afterSync' | 'weekly'>;

/** Who set the rule and when, in a line ("Set by kyle on ..."); '' when every rule is off. */
export function setLine(s: ScanSchedule): string {
  if (!(s.afterMerge || s.afterSync || s.weekly) || !s.setBy) return '';
  const who = s.setBy.replace(/^human:/, '');
  return `Set by ${who}${s.setAt ? ` on ${formatDateTime(s.setAt)}` : ''}. Scans it starts are in ${who}'s name.`;
}

export function SecuritySchedulePanel(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  const pid = () => handle.props.projectId;
  const sched = query(handle, {
    load: () => s.client.securityScheduleGet(pid()),
    key: pid,
    when: (e) => e.projectId === pid() && (e.type === 'security.schedule' || e.type.startsWith('security.scan.')),
  });
  const checks = query(handle, {
    load: () => s.client.checksSettingsGet(pid()),
    key: pid,
    when: (e) => e.type === 'checks.settings_changed' && e.projectId === pid(),
  });
  const act = action(handle);
  let said = '';

  const setRule = async (patch: Partial<Rule>) => {
    const cur = sched.data;
    if (!cur) return;
    said = '';
    const r = await act.run('rule', () => s.client.securityScheduleSet(pid(), { afterMerge: cur.afterMerge, afterSync: cur.afterSync, weekly: cur.weekly, ...patch }));
    if (r) {
      said = 'Saved.';
      sched.reload();
    }
    void handle.update();
  };
  const setScanCheck = async (v: boolean) => {
    const cur = checks.data;
    if (!cur) return;
    said = '';
    const r = await act.run('check', () => s.client.checksSettingsSet(pid(), { ...cur, scan: v }));
    if (r) {
      said = 'Saved.';
      checks.reload();
    }
    void handle.update();
  };

  return () => {
    const v = sched.data;
    const c = checks.data;
    const busy = !!act.busy;
    const set = v ? setLine(v) : '';
    return (
      <SettingsGroup
        title="Security scan rules"
        label="Security scan rules"
        testId="scan-schedule"
        description={
          <>
            Run the project's <a href={routes.security.href()}>security scan</a> without pressing Scan. All are off until you turn them on: the dependency
            check sends package names and versions to osv.dev.
          </>
        }
      >
        {!v ? (
          <div mix={errorPad}>
            <ErrorNote error={sched.error} what="Could not load the scan rules" />
            {sched.error ? null : <Loading />}
          </div>
        ) : (
          <>
            {act.error ? (
              <div mix={errorPad}>
                <ErrorNote error={act.error} what="Not saved" />
              </div>
            ) : null}
            <ToggleRow label="After a task is merged" description="Scans the project's folder once a task's work is merged. For a pull request merged on GitHub, it runs once Sync now brings the merge in." checked={v.afterMerge} disabled={busy} onChange={(x) => void setRule({ afterMerge: x })} />
            <ToggleRow label="After a sync brings new commits" description="Scans once Sync now has updated the branch." checked={v.afterSync} disabled={busy} onChange={(x) => void setRule({ afterSync: x })} />
            <ToggleRow
              label="Once a week"
              description={
                v.weekly && v.nextWeeklyAt
                  ? `A week after the last scan. Next: ${v.nextWeeklyAt <= Date.now() + 60_000 ? 'within a few minutes' : formatDateTime(v.nextWeeklyAt)}.`
                  : 'A week after the last scan; at once when the project was never scanned.'
              }
              checked={v.weekly}
              disabled={busy}
              onChange={(x) => void setRule({ weekly: x })}
            />
            <ToggleRow
              label="Also run the scan on every task's change"
              description="Turns on Security scan of the change in Verification checks: each task's change is scanned before review."
              checked={!!c?.scan}
              disabled={busy || !c}
              onChange={(x) => void setScanCheck(x)}
            />
            <p mix={noteRow}>A scan waits while an agent works in the project's folder. New high findings go to the Inbox.</p>
            {v.waiting ? (
              <p mix={noteRow} data-testid="scan-schedule-waiting">
                The scan {triggerWords(v.waiting)} is waiting. {v.waitingWhy}
              </p>
            ) : null}
            {v.last ? (
              <p mix={noteRow} data-testid="scan-schedule-last">
                Last scan by a rule: {triggerWords(v.last.trigger)}, {formatDateTime(v.last.startedAt)}
                {v.last.summary ? `. ${v.last.summary}` : '.'}
              </p>
            ) : null}
            {set ? (
              <p mix={noteRow} data-testid="scan-schedule-set">
                {set}
              </p>
            ) : null}
            {said ? (
              <p mix={noteRow} role="status">
                {said}
              </p>
            ) : null}
          </>
        )}
      </SettingsGroup>
    );
  };
}
