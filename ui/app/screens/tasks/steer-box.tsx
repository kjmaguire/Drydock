// The steer box at the foot of the task page (the graphite mockup "Task", decision P97): a message to the running agent, read
// before its next step (task.steer), sent with Enter or Ctrl+Enter, and Pause / Resume (task.pause, task.resume). Only while a
// run is going; otherwise it is off and says why. The core's refusal (nothing runs, the run waits for you) is shown as it came.

import { css, on, type Handle } from 'remix/component';
import { Pause, Play } from 'lucide';
import type { SteerResult } from '../../api/controlClient.ts';
import type { TaskDetail } from '../../api/types.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { isRunActive, isTerminal } from './tasks.ts';

const wrapCss = css({
  position: 'sticky',
  bottom: '0',
  zIndex: '1',
  display: 'grid',
  gap: t.s2,
  padding: `${t.s2} 28px 20px`,
  background: t.bgApp,
  '& > .note': { margin: '0', color: t.textFaint, fontSize: t.textSm },
});

const boxCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: '10px',
  margin: '0',
  padding: '10px 12px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  '&:focus-within': { borderColor: t.borderStrong },
  '& input': { flex: '1', minWidth: '0', minHeight: '28px', border: '0', outline: '0', padding: '0', background: 'transparent', color: t.text, font: 'inherit', boxShadow: 'none' },
  '& input:disabled': { cursor: 'not-allowed' },
  '& .keys': { display: 'flex', alignItems: 'center', gap: '4px', flex: 'none', color: t.textFaint, fontSize: t.textXs },
  '&[data-off="true"]': { opacity: '0.7' },
});

const sentText = (r: SteerResult, name: string): string =>
  r.delivered === 'now' ? `Sent. ${name} reads it before its next step.` : 'Sent. It goes with the next turn.';

export function SteerBox(handle: Handle<{ detail: TaskDetail; agentName: string; onChanged: (d?: TaskDetail) => void }>) {
  const s = services(handle);
  const act = action(handle);
  let text = '';
  let sent = '';
  const input = (v: string) => {
    text = v;
    if (sent) sent = '';
    void handle.update();
  };
  const steer = async () => {
    const { detail, agentName } = handle.props;
    const msg = text.trim();
    if (!msg || act.busy) return;
    const r = await act.run('steer', () => s.client.taskSteer(detail.task.id, msg));
    if (r) {
      text = '';
      sent = sentText(r, agentName);
      void handle.update();
    }
  };
  const pause = async () => {
    const { detail, onChanged } = handle.props;
    const d = await act.run(detail.paused ? 'resume' : 'pause', () => (detail.paused ? s.client.taskResume(detail.task.id) : s.client.taskPause(detail.task.id)));
    if (d) onChanged(d);
  };

  return () => {
    const { detail, agentName } = handle.props;
    const task = detail.task;
    const going = isRunActive(detail.run) && !isTerminal(task.status);
    const off = !going ? (isTerminal(task.status) ? 'The task is closed: there is no run to steer.' : 'Start the task to steer its agent.') : '';
    const busy = act.busy !== '';
    const paused = !!detail.paused;
    return (
      <div mix={wrapCss} data-testid="steer">
        <ErrorNote error={act.error} />
        <form
          mix={[
            boxCss,
            on('submit', (e) => {
              e.preventDefault();
              void steer();
            }),
          ]}
          aria-label="Steer the agent"
          data-off={off ? 'true' : undefined}
        >
          <label for="steer-input" class="sr-only">
            Message the agent
          </label>
          <input
            id="steer-input"
            type="text"
            autocomplete="off"
            maxLength={4000}
            value={text}
            disabled={!going || busy}
            title={off || undefined}
            placeholder={`Steer ${agentName || 'the agent'}: it reads this before its next step`}
            mix={[
              on('input', (e) => input(e.currentTarget.value)),
              on('keydown', (e) => {
                if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                  e.preventDefault();
                  void steer();
                }
              }),
            ]}
          />
          <span class="keys" aria-hidden="true">
            <kbd>Ctrl</kbd>
            <kbd>↵</kbd>
          </span>
          <Button
            size="sm"
            icon={paused ? Play : Pause}
            disabled={!going || busy}
            title={off || (paused ? 'Go on from where it was' : 'Stop the run where it is; the worktree stays')}
            mix={on('click', () => void pause())}
          >
            {act.busy === 'pause' ? 'Pausing...' : act.busy === 'resume' ? 'Resuming...' : paused ? 'Resume' : 'Pause'}
          </Button>
        </form>
        {off ? <p class="note">{off}</p> : sent ? <p class="note" role="status">{sent}</p> : null}
      </div>
    );
  };
}
