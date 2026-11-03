// The project's join rules on a room's tab (decision P130, room-protocol s10 (c); X17): one summary line per agent that joins task
// rooms by rule, in words, with Change opening that agent's editor, where the rule is set (Joins task rooms: never, every task,
// or tasks that touch a folder of the repository). The rules belong to the crew, not to a room, so the room only says them.

import { css, type Handle } from 'remix/component';
import type { Agent } from '../../api/types.ts';
import { query, services } from '../../state/services.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { tightList } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { joinChoiceOf, joinWords } from '../team/join-choice.ts';
import { routes } from '../../routes.ts';

const cardCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s4,
  borderRadius: t.rLg,
  background: t.bgApp,
  minWidth: '0',
  '& h3': { margin: '0', fontSize: t.textMd },
  '& .muted': { margin: '0', color: t.textMuted, fontSize: t.textSm },
});
const rowCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, padding: '4px 8px', fontSize: t.textSm, '& .what': { flex: '1', minWidth: '0' } });

export function JoinRules(handle: Handle<{ projectId: string; agents: readonly Agent[] }>) {
  const s = services(handle);
  const rules = query(handle, {
    load: () => s.client.roomJoinRules(handle.props.projectId),
    when: (e) => e.type.startsWith('room.join_rule'),
    key: () => handle.props.projectId,
  });

  return () => {
    const list = rules.data ?? [];
    const withRule = [...new Set(list.map((r) => r.agentId))];
    const name = (id: string) => handle.props.agents.find((a) => a.id === id)?.name ?? id;
    return (
      <section mix={cardCss} aria-label="Join rules" data-testid="join-rules">
        <h3>Join rules</h3>
        <ErrorNote error={rules.error} />
        {withRule.length ? (
          <ul mix={tightList}>
            {withRule.map((id) => (
              <li key={id} mix={rowCss} data-testid="join-rule">
                <span class="what">{joinWords(name(id), joinChoiceOf(list, id))}</span>
                <a href={`${routes.team.href()}?edit=${encodeURIComponent(id)}`} aria-label={`Change the join rule of ${name(id)}`}>
                  Change
                </a>
              </li>
            ))}
          </ul>
        ) : rules.data ? (
          <p class="muted" data-testid="join-rules-none">
            No agent joins task rooms by rule. Set it on an agent's editor, under More settings.
          </p>
        ) : null}
      </section>
    );
  };
}
