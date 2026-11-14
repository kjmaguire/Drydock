// The Decisions section of a room's tab (X110): the decisions of the project's rooms in #decisions, a room's own elsewhere, each
// with where it was taken and "Make an ADR task". Creating the task is the person's click; the room writes no files.

import { css, on, type Handle } from 'remix/component';
import { FilePlus2, Gavel } from 'lucide';
import type { Room } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { adrTaskRequest, decisionsFor, decisionWhere } from './decisions.ts';

const sectionCss = css({
  display: 'grid',
  gap: t.s2,
  '& h3': { display: 'flex', alignItems: 'center', gap: t.s2, margin: '0', fontSize: t.textMd, fontWeight: '600' },
  '& ul': { display: 'grid', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' },
  '& li': { display: 'grid', gap: '2px', padding: `${t.s2} 0`, borderTop: `1px solid ${t.border}` },
  '& .title': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', fontSize: t.textMd },
  '& .where': { color: t.textMuted, fontSize: t.textSm },
  '& .row': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& p.muted': { margin: '0', color: t.textMuted, fontSize: t.textSm },
});

export function RoomDecisions(handle: Handle<{ room: Room }>) {
  const s = services(handle);
  const act = action(handle);
  /** The tasks made from decisions here, by the decision's message id. */
  const made = new Map<string, { id: string; seq: number }>();
  const list = query(handle, {
    load: () => s.client.roomDecisions(handle.props.room.projectId),
    when: (e) => e.type === 'room.message_posted' || e.type === 'room.archived',
    key: () => handle.props.room.projectId,
    debounceMs: 300,
  });
  return () => {
    const { room } = handle.props;
    const shown = decisionsFor(list.data ?? [], room);
    const everywhere = room.kind === 'channel' && room.name === 'decisions';
    return (
      <section mix={sectionCss} aria-label="Decisions" data-testid="room-decisions">
        <h3>
          <Icon icon={Gavel} size={15} />
          Decisions
        </h3>
        <ErrorNote error={list.error ?? act.error} what="Not done" />
        {list.data && !shown.length ? (
          <p class="muted">{everywhere ? "No decisions yet. A /decision or a [DECISION] line in any of the project's rooms shows up here." : 'No decisions in this room yet.'}</p>
        ) : null}
        {shown.length ? (
          <ul>
            {shown.map((d) => {
              const task = made.get(d.messageId);
              return (
                <li key={d.messageId} data-testid="room-decision">
                  <span class="title">
                    <strong>{d.title || d.decision}</strong>
                    {d.status ? <Badge tone={d.status === 'accepted' ? 'ok' : 'info'}>{d.status}</Badge> : null}
                  </span>
                  {everywhere ? (
                    <a class="where" href={routes.room.href({ id: d.roomId })}>
                      in {decisionWhere(d)}
                      {d.archived ? ' (task closed)' : ''}
                    </a>
                  ) : null}
                  <span class="row">
                    {task ? (
                      <a href={routes.task.href({ id: task.id })} data-testid="room-decision-task">
                        ADR task {task.seq ? s.workspace.keyOf(task) : ''} made
                      </a>
                    ) : (
                      <Button
                        size="sm"
                        icon={FilePlus2}
                        disabled={!!act.busy}
                        data-testid="room-decision-make-task"
                        title="Make a task that writes this decision down as an ADR."
                        mix={[
                          on('click', async () => {
                            const created = await act.run(d.messageId, () => s.client.taskCreate(adrTaskRequest(room.projectId, d)));
                            if (created) made.set(d.messageId, { id: created.id, seq: created.seq });
                            void handle.update();
                          }),
                        ]}
                      >
                        {act.busy === d.messageId ? 'Making the task...' : 'Make an ADR task'}
                      </Button>
                    )}
                  </span>
                </li>
              );
            })}
          </ul>
        ) : null}
      </section>
    );
  };
}
