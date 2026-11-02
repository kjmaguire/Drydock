// A failed or cancelled task says so where the person looks, at the top of its page: why its run stopped (the run's
// failedReason) and Run again as a new task (task.runAgain with its confirmation), or why it cannot be run again. The run page
// keeps the same button with the other run actions.

import { css, type Handle } from 'remix/component';
import type { TaskDetail } from '../../api/types.ts';
import { visible } from '../../lib/visible.ts';
import { muted } from '../../ui/data.tsx';
import { t } from '../../ui/tokens.ts';
import { RunAgainButton } from '../shared/run-again.tsx';

const cardCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s3} ${t.s4}`,
  borderRadius: t.rLg,
  background: t.dangerSoft,
  border: `1px solid color-mix(in srgb, ${t.danger} 30%, transparent)`,
  minWidth: '0',
  '& > p': { margin: '0', overflowWrap: 'anywhere' },
  '& > p:first-child': { fontWeight: '600' },
  '& > button': { justifySelf: 'start' },
});

/** Renders nothing unless the task failed or was cancelled. */
export function EndedCard(handle: Handle<{ detail: TaskDetail; onNavigate?: (href: string) => void }>) {
  return () => {
    const { detail: d, onNavigate } = handle.props;
    const status = d.task.status;
    if (status !== 'FAILED' && status !== 'CANCELLED') return null;
    const reason = d.run?.failedReason?.trim();
    return (
      <section mix={cardCss} aria-label={status === 'FAILED' ? 'The task failed' : 'The task was cancelled'} data-testid="task-ended">
        <p>{status === 'FAILED' ? 'This task failed.' : 'This task was cancelled.'}</p>
        {reason ? <p data-testid="task-ended-reason">Why it stopped: {visible(reason)}</p> : status === 'FAILED' ? <p mix={muted}>Its run recorded no reason.</p> : null}
        {d.runAgain ? (
          <RunAgainButton taskId={d.task.id} seq={d.task.seq} status={status} onNavigate={onNavigate} />
        ) : d.runAgainWhy ? (
          <p mix={muted} data-testid="task-ended-why">
            {d.runAgainWhy}
          </p>
        ) : null}
      </section>
    );
  };
}
