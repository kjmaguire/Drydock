// The team lead in the UI: "Run stand-up" (the lead posts a status round-up in #general) and the list of the lead's
// interventions (when it stepped in on a task, what it decided and why). The lead's message and reason are agent output:
// shown as inert text. The list re-reads on lead.intervened.

import { css, on, type Handle } from 'remix/component';
import { ClipboardCheck, Megaphone } from 'lucide';
import type { LeadIntervention, StandupResult } from '../../api/leadClient.ts';
import type { Agent } from '../../api/types.ts';
import { agentName, formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';

/** The project's team lead: the first agent whose role is "lead". */
export function leadOf(agents: readonly Agent[]): Agent | undefined {
  return agents.find((a) => a.role.trim().toLowerCase() === 'lead');
}

export const TRIGGER_LABEL: Record<string, string> = {
  watchdog: 'watchdog trip',
  iteration_limit: 'loop budget used up',
  repeated_revise: 'same review feedback again',
  stalled: 'no progress',
  unassigned: 'no worker or reviewer',
};

const doneCss = css({ display: 'inline-flex', alignItems: 'center', gap: t.s2, color: t.textMuted, fontSize: t.textSm, '& a': { fontWeight: '500' } });

/** "Run stand-up": lead.standup, then a status line with a link to #general. */
export function RunStandup(handle: Handle<{ projectId: string; lead: Agent; size?: 'sm' | 'md' }>) {
  const s = services(handle);
  watch(handle, s.project);
  watch(handle, s.workspace);
  const act = action(handle);
  let done: StandupResult | undefined;

  const run = async () => {
    done = undefined;
    const r = await act.run('standup', () => s.client.leadStandup(handle.props.projectId));
    if (r) done = r;
    void handle.update();
  };

  return () => {
    const { lead, size = 'sm' } = handle.props;
    const general = s.project.roomList.find((r) => r.kind === 'channel' && r.name === 'general');
    const ro = s.workspace.license;
    return (
      <div style={{ display: 'grid', gap: 'var(--space-2)', justifyItems: 'start' }} data-testid="run-standup">
        <Button
          size={size}
          variant="secondary"
          icon={Megaphone}
          disabled={!!act.busy || ro.readOnly}
          title={ro.readOnly ? ro.reason : `${lead.name} posts a stand-up in #general: done, in progress, blocked, and what needs you`}
          mix={on('click', () => void run())}
        >
          {act.busy ? 'Running stand-up...' : 'Run stand-up'}
        </Button>
        <ErrorNote error={act.error} what="Stand-up not posted" />
        {done ? (
          <span mix={doneCss} role="status">
            Posted in general{done.tasks ? ` (${done.tasks} ${done.tasks === 1 ? 'task' : 'tasks'})` : ''}.
            {general ? <a href={routes.room.href({ id: general.id })}>Open general</a> : null}
          </span>
        ) : null}
      </div>
    );
  };
}

const listCss = css({ display: 'grid', margin: '0', padding: '0', listStyle: 'none', border: `1px solid ${t.border}`, borderRadius: t.rLg, background: t.bgSurface });
const itemCss = css({
  display: 'grid',
  gap: '4px',
  padding: `${t.s3} ${t.s4}`,
  borderBottom: `1px solid ${t.border}`,
  minWidth: '0',
  '&:last-child': { borderBottom: '0' },
  '& .head': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, fontSize: t.textSm, color: t.textMuted },
  '& .head strong': { color: t.text, fontWeight: '600' },
  '& .msg': { margin: '0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: t.textMd },
  '& .why': { margin: '0', color: t.textMuted, fontSize: t.textSm, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
  '& .when': { marginLeft: 'auto', color: t.textFaint, fontSize: t.textXs },
});

/** The lead's interventions in the project, or on one task. */
export function Interventions(handle: Handle<{ projectId: string; taskId?: string; limit?: number }>) {
  const s = services(handle);
  watch(handle, s.project);
  const q = query(handle, {
    load: () => s.client.leadInterventions(handle.props.projectId, handle.props.taskId),
    when: (e) => e.type === 'lead.intervened' && (!handle.props.taskId || e.taskId === handle.props.taskId),
    key: () => [handle.props.projectId, handle.props.taskId ?? ''],
    enabled: () => !!handle.props.projectId,
  });

  return () => {
    const { taskId, limit = 50 } = handle.props;
    const agents = s.project.agentList;
    const tasks = s.project.taskList;
    if (q.loading && !q.data) return <Loading label="Loading interventions..." />;
    const list = [...(q.data ?? [])].sort((a, b) => b.createdAt - a.createdAt).slice(0, limit);
    return (
      <div data-testid="lead-interventions">
        <ErrorNote error={q.error} what="Could not load the team lead's interventions" />
        {q.data && list.length === 0 ? (
          <Empty title="No interventions yet" icon={ClipboardCheck}>
            <p>{taskId ? 'The team lead has not stepped in on this task.' : 'When the team lead steps in on a task (a loop, a stall, a watchdog trip), it is listed here.'}</p>
          </Empty>
        ) : null}
        {list.length > 0 ? (
          <ul mix={listCss} aria-label="Team lead interventions">
            {list.map((x: LeadIntervention) => {
              const task = tasks.find((tk) => tk.id === x.taskId);
              return (
                <li key={x.id} mix={itemCss} data-testid="intervention" data-action={x.action} data-trigger={x.trigger}>
                  <div class="head">
                    <strong>{agentName(x.leadAgentId, agents) || 'Team lead'}</strong>
                    <Badge tone={x.action === 'escalate' || x.action === 'stop' ? 'warn' : 'accent'}>{x.action || 'note'}</Badge>
                    <span>on {TRIGGER_LABEL[x.trigger] ?? x.trigger}</span>
                    {x.targetAgent ? <span>to {agentName(x.targetAgent, agents) || x.targetAgent}</span> : null}
                    {!taskId && x.taskId ? (
                      <a href={routes.task.href({ id: x.taskId })}>{task ? `#${task.seq} ${task.title}` : 'the task'}</a>
                    ) : null}
                    {x.invalid ? (
                      <Badge tone="err" title="The lead's answer could not be applied as given; it was recorded, not acted on">
                        not applied
                      </Badge>
                    ) : null}
                    <span class="when">{formatDateTime(x.createdAt)}</span>
                  </div>
                  {x.message ? <p class="msg">{x.message}</p> : null}
                  {x.reason ? <p class="why">Why: {x.reason}</p> : null}
                  {x.createdTasks.length > 0 ? (
                    <p class="why">
                      Created{' '}
                      {x.createdTasks.map((id, i) => {
                        const tk = tasks.find((y) => y.id === id);
                        return (
                          <span key={id}>
                            {i > 0 ? ', ' : ''}
                            <a href={routes.task.href({ id })}>{tk ? `#${tk.seq} ${tk.title}` : id}</a>
                          </span>
                        );
                      })}
                    </p>
                  ) : null}
                </li>
              );
            })}
          </ul>
        ) : null}
      </div>
    );
  };
}
