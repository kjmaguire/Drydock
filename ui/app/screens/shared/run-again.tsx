// Run again as a new task (task.runAgain, P124) where a failure is shown: the task page of a failed or cancelled task and the
// Inbox's item of an agent that failed on it. It asks first with the run page's confirmation (runAgainLines: paid work starts
// again, in a fresh worktree; the ended task stays as it is), then opens the new task.

import { css, navigate, on, type Handle } from 'remix/component';
import { CopyPlus } from 'lucide';
import { routes } from '../../routes.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { runAgainLines } from '../run/run-model.ts';

const bodyCss = css({ display: 'grid', gap: t.s3, '& ul': { margin: '0', paddingLeft: '20px', display: 'grid', gap: '6px' } });

/** The button and its confirmation. `onNavigate` replaces opening the new task (tests, an embedding page). */
export function RunAgainButton(
  handle: Handle<{ taskId: string; seq: number; status: string; disabled?: boolean; title?: string; variant?: 'primary' | 'secondary'; onNavigate?: (href: string) => void }>,
) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let asking = false;
  const set = (v: boolean) => {
    asking = v;
    if (v) act.clear();
    void handle.update();
  };
  const runAgain = async () => {
    const res = await act.run('again', () => s.client.taskRunAgain(handle.props.taskId));
    if (!res) return;
    asking = false;
    void handle.update();
    const href = routes.task.href({ id: res.taskId });
    if (handle.props.onNavigate) handle.props.onNavigate(href);
    else void navigate(href);
  };
  return () => {
    const { seq, status, variant = 'primary' } = handle.props;
    // task.runAgain needs a licence that is not read-only: the button says so instead of failing after the confirmation
    const ro = s.workspace.license;
    const disabled = handle.props.disabled || ro.readOnly;
    const title = handle.props.title ?? (ro.readOnly ? ro.reason : undefined);
    return (
      <>
        <Button variant={variant} icon={CopyPlus} disabled={disabled} title={title} data-testid="run-again" mix={on('click', () => set(true))}>
          Run again as a new task
        </Button>
        <Dialog open={asking} title={`Run task #${seq} again?`} onClose={() => set(false)}>
          {asking ? (
            <div mix={bodyCss} data-testid="run-again-confirm">
              <ul>
                {runAgainLines(seq, status).map((l) => (
                  <li key={l}>{l}</li>
                ))}
              </ul>
              <ErrorNote error={act.error} what="It did not start" />
              <div mix={formActions}>
                <Button variant="primary" disabled={!!act.busy || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} mix={on('click', () => void runAgain())}>
                  {act.busy === 'again' ? 'Starting...' : 'Run it again'}
                </Button>
                <Button mix={on('click', () => set(false))}>Cancel</Button>
              </div>
            </div>
          ) : null}
        </Dialog>
      </>
    );
  };
}
