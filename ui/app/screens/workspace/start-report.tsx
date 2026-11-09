// The line Home shows after Drydock starts again (X511): what the core found the last stop left behind (app.info's startReport),
// in one sentence, with a link to each run it resumed or could not resume and to the Support page when it kept a crash report.
// Nothing is said after a clean close that left nothing to resume. Dismissing it is remembered for that start only
// (localStorage, a convenience): the next start that has something to say says it again.

import { on, type Handle } from 'remix/component';
import { X } from 'lucide';
import type { StartReport, StartRun } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Notice } from '../../ui/feedback.tsx';

const DISMISSED_KEY = 'drydock.startReport.dismissed';

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** Whether the report has anything to say. */
export function worthSaying(r: StartReport | undefined): r is StartReport {
  return !!r && (r.unexpected || r.resumed.length > 0 || r.notResumed.length > 0 || (r.resuming?.length ?? 0) > 0 || (r.crashes ?? 0) > 0);
}

/** The sentence: how Drydock stopped (only "unexpectedly" when the core knows), then what this start did about it. */
export function startSentence(r: StartReport): string {
  const parts: string[] = [];
  if (r.resumed.length) parts.push(`${plural(r.resumed.length, 'run was', 'runs were')} resumed`);
  if (r.notResumed.length) parts.push(`${plural(r.notResumed.length, 'run', 'runs')} could not be resumed`);
  if (r.resuming?.length) parts.push(`${plural(r.resuming.length, 'run is', 'runs are')} being resumed`);
  if (r.agentRunsClosed) parts.push(`${plural(r.agentRunsClosed, 'agent turn was', 'agent turns were')} stopped`);
  const lead = r.unexpected ? 'Drydock was closed unexpectedly' : 'Drydock started again';
  return parts.length ? `${lead}; ${parts.join(', ')}.` : `${lead}.`;
}

const readDismissed = (): number => {
  try {
    return Number(localStorage.getItem(DISMISSED_KEY) ?? 0) || 0;
  } catch {
    return 0;
  }
};

const runLink = (r: StartRun) =>
  r.taskId ? (
    <a key={r.runId} href={routes.run.href({ id: r.taskId, runId: r.runId })} title={r.runId}>
      {r.title || 'Untitled task'}
    </a>
  ) : (
    <span key={r.runId}>{r.runId}</span>
  );

const links = (runs: readonly StartRun[]) => runs.flatMap((r, i) => (i === 0 ? [runLink(r)] : [', ', runLink(r)]));

export function StartReportLine(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  let dismissed = readDismissed();
  const dismiss = (at: number) => {
    dismissed = at;
    try {
      localStorage.setItem(DISMISSED_KEY, String(at));
    } catch {
      /* a convenience only: it is gone for this visit anyway */
    }
    void handle.update();
  };
  return () => {
    const r = s.workspace.info.data?.startReport;
    if (!worthSaying(r) || dismissed === r.startedAt) return null;
    return (
      <Notice tone={r.unexpected || r.notResumed.length ? 'warn' : 'info'} role="status" testId="start-report">
        <span>{startSentence(r)}</span>
        {r.resumed.length ? <span data-testid="start-report-resumed"> Resumed: {links(r.resumed)}.</span> : null}
        {r.notResumed.length ? <span data-testid="start-report-not-resumed"> Could not be resumed: {links(r.notResumed)}.</span> : null}
        {r.resuming?.length ? <span data-testid="start-report-resuming"> Being resumed: {links(r.resuming)}.</span> : null}
        {r.crashes ? (
          <span>
            {' '}
            <a href={routes.support.href()}>{r.crashes === 1 ? 'The crash report is on the Support page.' : 'The crash reports are on the Support page.'}</a>
          </span>
        ) : null}{' '}
        <Button size="sm" variant="ghost" icon={X} aria-label="Dismiss this note about the last stop" mix={on('click', () => dismiss(r.startedAt))}>
          Dismiss
        </Button>
      </Notice>
    );
  };
}
