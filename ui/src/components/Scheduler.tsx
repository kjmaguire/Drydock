import { useState } from 'react';
import type { TickResult } from '../api/extTypes';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote } from './common';

/** The scheduler starts READY tasks within the parallelism limits. It is automatic only when the project opts in. */
export function SchedulerPanel({ projectId }: { projectId: string }) {
  const client = useClient();
  const q = useLive(() => client.schedulerStatus(projectId), [projectId], (e) => e.type.startsWith('task.') || e.type.startsWith('scheduler.') || e.type.startsWith('workflow.'), true, 300);
  const act = useAction();
  const [tick, setTick] = useState<TickResult | undefined>();
  const st = q.data;
  return (
    <section aria-label="Scheduler" data-testid="scheduler">
      <h2>Scheduler</h2>
      <ErrorNote error={q.error ?? act.error} what="Scheduler" />
      {st ? (
        <p>
          <Badge tone={st.settings.autoSchedule ? 'info' : 'muted'}>{st.settings.autoSchedule ? 'automatic' : 'manual'}</Badge> up to {st.settings.maxParallel || 'the default number of'} task(s) in parallel; {st.activeRuns} run(s)
          executing, {st.agentSlotsInUse} agent slot(s) in use (peak {st.peakAgentSlots}). Change these under Project settings.
        </p>
      ) : null}
      <button disabled={act.busy !== ''} onClick={async () => { const r = await act.run('tick', () => client.schedulerTick(projectId)); if (r) { setTick(r); q.reload(); } }}>
        Start ready tasks now
      </button>
      {tick ? (
        <div role="status" data-testid="tick-result">
          <p>
            Started {(tick.started ?? []).length}, promoted {(tick.promoted ?? []).length} to ready; {tick.active} active, capacity {tick.capacity} of {tick.max}.
          </p>
          {(tick.skipped ?? []).length > 0 ? (
            <ul>
              {(tick.skipped ?? []).map((s) => (
                <li key={s.taskId}>
                  <a href={`#/task/${encodeURIComponent(s.taskId)}`}>{s.taskId}</a> not started: {s.reason}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
