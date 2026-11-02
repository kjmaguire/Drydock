// The scheduler starts READY tasks within the parallelism limits. It is automatic only when the project opts in.

import { css, on, type Handle } from 'remix/component';
import { Gauge, Play } from 'lucide';
import type { TickResult } from '../../api/extTypes.ts';
import { routes } from '../../routes.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { plural } from '../../lib/format.ts';

const panelCss = css({
  display: 'grid',
  gap: t.s3,
  padding: `${t.s3} ${t.s4}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap' },
  '& h2': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, fontWeight: '600' },
  '& h2 svg': { color: t.textMuted },
  '& .facts': { flex: '1', minWidth: '240px', color: t.textMuted, fontSize: t.textSm, margin: '0' },
  '& .tick': { display: 'grid', gap: t.s1, padding: `${t.s2} ${t.s3}`, borderRadius: t.rMd, background: t.bgApp, fontSize: t.textSm },
  '& .tick ul': { margin: '0', paddingLeft: '18px' },
});

export function SchedulerPanel(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const q = query(handle, {
    load: () => s.client.schedulerStatus(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => e.type.startsWith('task.') || e.type.startsWith('scheduler.') || e.type.startsWith('workflow.'),
    debounceMs: 300,
  });
  const act = action(handle);
  let tick: TickResult | undefined;

  const runTick = async () => {
    const r = await act.run('tick', () => s.client.schedulerTick(handle.props.projectId));
    if (r) {
      tick = r;
      q.reload();
      void handle.update();
    }
  };

  return () => {
    const st = q.data;
    const ro = s.workspace.license;
    return (
      <section mix={panelCss} aria-label="Scheduler" data-testid="scheduler">
        <div class="head">
          <h2>
            <Icon icon={Gauge} size={15} />
            Scheduler
          </h2>
          {st ? (
            // one plain line (decision P144); the agent slots, a detail for diagnosis, are its tooltip
            <p class="facts" data-testid="scheduler-facts" title={`${plural(st.agentSlotsInUse, 'agent slot')} in use (peak ${st.peakAgentSlots})`}>
              <Badge tone={st.settings.autoSchedule ? 'info' : 'muted'}>{st.settings.autoSchedule ? 'automatic' : 'manual'}</Badge> up to{' '}
              {st.settings.maxParallel ? plural(st.settings.maxParallel, 'task') : 'the default number of tasks'} at once, {st.activeRuns} running now ·{' '}
              <a href={routes.project.href()} data-testid="scheduler-change">
                Change
              </a>
            </p>
          ) : (
            <span class="facts" />
          )}
          <Button size="sm" icon={Play} disabled={act.busy !== '' || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} mix={on('click', () => void runTick())}>
            Start ready tasks now
          </Button>
        </div>
        <ErrorNote error={q.error ?? act.error} what="Scheduler" />
        <ReadOnlyNote />
        {tick ? (
          <div class="tick" role="status" data-testid="tick-result">
            <p>
              Started {(tick.started ?? []).length}, promoted {(tick.promoted ?? []).length} to ready; {tick.active} active, capacity {tick.capacity} of {tick.max}.
            </p>
            {(tick.skipped ?? []).length > 0 ? (
              <ul>
                {(tick.skipped ?? []).map((sk) => (
                  <li key={sk.taskId}>
                    <a href={routes.task.href({ id: sk.taskId })}>{sk.taskId}</a> <span mix={muted}>not started: {sk.reason}</span>
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
        ) : null}
      </section>
    );
  };
}
