// Who answers in this room, how, and what the agents have done (room-protocol turn-taking). A collapsible bar under the
// channel header; changing members or the mode is a human action, and the core has the last word on every change.

import { css, on, type Handle } from 'remix/component';
import { Bot, ChevronDown, MessageSquareReply, Trash2, UserPlus } from 'lucide';
import type { Agent, Room, RoomMember } from '../../api/types.ts';
import { formatCost } from '../../lib/format.ts';
import { action, query, services } from '../../state/services.tsx';
import { numOrUndef } from '../shared/budget-pause.tsx';
import { ExplainText } from '../shared/explain.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, Badge } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { memberKey, MODE_TEXT } from './messages.ts';

const barCss = css({
  flex: 'none',
  borderBottom: `1px solid ${t.border}`,
  background: t.bgApp,
  '& > summary': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    height: '36px',
    padding: `0 ${t.s5}`,
    listStyle: 'none',
    color: t.textMuted,
    fontSize: t.textSm,
    userSelect: 'none',
  },
  '& > summary::-webkit-details-marker': { display: 'none' },
  '& > summary:hover': { background: t.bgHover, color: t.text },
  '& > summary .chev': { display: 'inline-flex', marginLeft: 'auto', transition: `transform ${t.fast} ${t.ease}` },
  '&[open] > summary .chev': { transform: 'rotate(180deg)' },
  '&[open] > summary': { color: t.text },
});
const stackCss = css({ display: 'inline-flex', alignItems: 'center', marginLeft: t.s2, '& > span': { marginLeft: '-6px', boxShadow: `0 0 0 2px ${t.bgApp}`, borderRadius: '50%' }, '& > span:first-child': { marginLeft: '0' } });
const bodyCss = css({
  display: 'grid',
  gap: t.s4,
  maxHeight: '55vh',
  overflowY: 'auto',
  padding: `${t.s3} ${t.s5} ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  background: t.bgSidebar,
});
const colsCss = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: t.s4, alignItems: 'start' });
const cardCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& h3': { display: 'flex', alignItems: 'center', gap: t.s2, margin: '0', fontSize: t.textMd },
  '& .muted': { margin: '0', color: t.textMuted, fontSize: t.textSm },
});
const modeText = css({ margin: '0', padding: '8px 10px', borderRadius: t.rMd, background: t.bgActive, color: t.textMuted, fontSize: t.textSm, lineHeight: '1.5' });
const orderCss = css({ display: 'flex', flexWrap: 'wrap', gap: '6px', alignItems: 'center', fontSize: t.textSm });
const inlineRow = css({ display: 'flex', flexWrap: 'wrap', gap: t.s2, alignItems: 'flex-end' });
const memberRow = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: '6px 8px',
  borderRadius: t.rMd,
  '&:hover': { background: t.bgHover },
  '& .who': { flex: '1', minWidth: '0', display: 'grid' },
  '& .who strong': { fontSize: t.textMd, fontWeight: '600' },
  '& .who span': { color: t.textMuted, fontSize: t.textSm },
});
const listCss = css({ display: 'grid', gap: '2px', margin: '0', padding: '0', listStyle: 'none' });
const tableWrap = css({ border: `1px solid ${t.border}`, borderRadius: t.rLg, background: t.bgSurface, overflow: 'auto', '& tr:last-child td': { borderBottom: '0' } });

/** Who answers in this room, how, and what the agents have done. Changing members or the mode is a human action. */
export function RoomAgentsPanel(handle: Handle<{ room: Room; agents: readonly Agent[]; members: readonly RoomMember[]; lastHumanMessageId?: string }>) {
  const s = services(handle);
  const act = action(handle);
  const turns = query(handle, {
    load: () => s.client.roomTurns(handle.props.room.id, 20),
    when: (e) => e.type.startsWith('room.') || e.type.startsWith('agent.'),
    key: () => handle.props.room.id,
    debounceMs: 400,
  });
  const init = handle.props.room;
  let mode: string = init.mode;
  let order: string[] = init.modeParams?.order ?? [];
  let moderator = init.modeParams?.moderator ?? '';
  let rounds = String(init.modeParams?.max_rounds ?? 3);
  let invite = '';
  let explainOf: string | undefined; // the decision whose "Why this model?" is open
  const update = () => void handle.update();

  const name = (key: string) => (key.startsWith('human:') ? key.slice(6) : (handle.props.agents.find((a) => a.id === key)?.name ?? key));

  const saveMode = () =>
    act.run('mode', () =>
      s.client.roomSetMode({
        roomId: handle.props.room.id,
        mode,
        order: mode === 'sequential' ? order : [],
        moderator: mode === 'moderator' ? moderator : '',
        maxRounds: numOrUndef(rounds) ?? 0,
      }),
    );

  return () => {
    const { room, agents, members, lastHumanMessageId } = handle.props;
    const memberKeys = members.map(memberKey);
    const agentMembers = members.filter((m) => m.member.kind === 'agent');
    const invitable = agents.filter((a) => !memberKeys.includes(a.id));
    const list = turns.data ?? [];
    const busy = act.busy !== '';
    return (
      <details mix={barCss} class="room-agents" data-testid="room-agents">
        <summary>
          <Icon icon={Bot} size={15} />
          <span>Agents in this room and how they answer</span>
          {agentMembers.length ? (
            <span mix={stackCss} aria-hidden="true">
              {agentMembers.slice(0, 5).map((m) => (
                <Avatar key={m.member.id} id={m.member.id} name={name(m.member.id)} size={20} />
              ))}
            </span>
          ) : null}
          <span class="chev" aria-hidden="true">
            <Icon icon={ChevronDown} size={15} />
          </span>
        </summary>
        <div mix={bodyCss}>
          <ErrorNote error={act.error ?? turns.error} />
          <div mix={colsCss}>
            <section mix={cardCss} aria-label="Turn-taking mode">
              <h3>Turn-taking mode</h3>
              <p class="muted">
                Current mode: <Badge tone="accent">{room.mode}</Badge>
              </p>
              <Field label="Mode">
                {(id) => (
                  <select
                    id={id}
                    aria-label="Mode"
                    value={mode}
                    mix={[
                      on('change', (e) => {
                        mode = (e.currentTarget as HTMLSelectElement).value;
                        update();
                      }),
                    ]}
                  >
                    <option value="auto" selected={mode === 'auto'}>
                      auto
                    </option>
                    <option value="sequential" selected={mode === 'sequential'}>
                      sequential
                    </option>
                    <option value="moderator" selected={mode === 'moderator'}>
                      moderator
                    </option>
                  </select>
                )}
              </Field>
              <p mix={modeText} data-testid="mode-text">
                {MODE_TEXT[mode]}
              </p>
              {mode === 'sequential' ? (
                <div style={{ display: 'grid', gap: '8px' }}>
                  <p class="muted">Speaking order: {order.length ? order.map(name).join(' > ') : 'not set'}</p>
                  <div mix={orderCss}>
                    {memberKeys
                      .filter((k) => k !== 'system')
                      .map((k) => (
                        <Button
                          key={k}
                          size="sm"
                          disabled={order.includes(k)}
                          mix={[
                            on('click', () => {
                              order = [...order, k];
                              update();
                            }),
                          ]}
                        >
                          Add {name(k)}
                        </Button>
                      ))}
                    {order.length ? (
                      <Button
                        size="sm"
                        variant="ghost"
                        mix={[
                          on('click', () => {
                            order = [];
                            update();
                          }),
                        ]}
                      >
                        Clear order
                      </Button>
                    ) : null}
                  </div>
                </div>
              ) : null}
              <div mix={inlineRow}>
                {mode === 'moderator' ? (
                  <div style={{ flex: '1', minWidth: '180px' }}>
                    <Field label="Moderator">
                      {(id) => (
                        <select
                          id={id}
                          aria-label="Moderator"
                          value={moderator}
                          mix={[
                            on('change', (e) => {
                              moderator = (e.currentTarget as HTMLSelectElement).value;
                              update();
                            }),
                          ]}
                        >
                          <option value="" selected={moderator === ''}>
                            Choose a moderator
                          </option>
                          {memberKeys.map((k) => (
                            <option key={k} value={k} selected={moderator === k}>
                              {name(k)}
                            </option>
                          ))}
                        </select>
                      )}
                    </Field>
                  </div>
                ) : null}
                <div style={{ width: '120px' }}>
                  <Field label="Round limit">
                    {(id) => (
                      <input
                        id={id}
                        aria-label="Round limit"
                        inputMode="numeric"
                        value={rounds}
                        mix={[
                          on('input', (e) => {
                            rounds = (e.currentTarget as HTMLInputElement).value;
                            update();
                          }),
                        ]}
                      />
                    )}
                  </Field>
                </div>
                <Button variant="primary" disabled={busy} mix={[on('click', () => void saveMode())]}>
                  {act.busy === 'mode' ? 'Saving...' : 'Save mode'}
                </Button>
              </div>
            </section>
            <section mix={cardCss} aria-label="Agents">
              <h3>Agents</h3>
              <ul mix={listCss}>
                {agentMembers.map((m) => (
                  <li key={memberKey(m)} mix={memberRow}>
                    <Avatar id={m.member.id} name={name(m.member.id)} size={28} />
                    <div class="who">
                      <strong>{name(m.member.id)}</strong>
                      <span>
                        {m.role} {m.state}
                      </span>
                    </div>
                    <Button size="sm" variant="ghost" icon={Trash2} disabled={busy} mix={[on('click', () => void act.run('remove', () => s.client.roomRemoveMember(room.id, m.member.id)))]}>
                      Remove
                    </Button>
                  </li>
                ))}
                {agentMembers.length === 0 ? (
                  <li class="muted" style={{ padding: '6px 8px' }}>
                    No agent is in this room.
                  </li>
                ) : null}
              </ul>
              <div mix={inlineRow}>
                <select
                  aria-label="Invite an agent"
                  value={invite}
                  style={{ flex: '1', minWidth: '160px', width: 'auto' }}
                  mix={[
                    on('change', (e) => {
                      invite = (e.currentTarget as HTMLSelectElement).value;
                      update();
                    }),
                  ]}
                >
                  <option value="" selected={invite === ''}>
                    Invite an agent...
                  </option>
                  {invitable.map((a) => (
                    <option key={a.id} value={a.id} selected={invite === a.id}>
                      {a.name} ({a.role})
                    </option>
                  ))}
                </select>
                <Button
                  icon={UserPlus}
                  disabled={!invite || busy}
                  mix={[
                    on('click', async () => {
                      const id = invite;
                      await act.run('invite', () => s.client.roomInvite(room.id, id));
                      invite = '';
                      update();
                    }),
                  ]}
                >
                  Invite
                </Button>
              </div>
              {lastHumanMessageId ? (
                <Button
                  icon={MessageSquareReply}
                  disabled={busy}
                  title="Ask the agents to answer your latest message now"
                  mix={[
                    on('click', async () => {
                      await act.run('reply', () => s.client.roomReply(lastHumanMessageId));
                      turns.reload();
                    }),
                  ]}
                >
                  Ask agents to reply to my last message
                </Button>
              ) : null}
            </section>
          </div>
          <section style={{ display: 'grid', gap: '8px' }} aria-label="Recent agent turns">
            <h3 style={{ margin: '0', fontSize: 'var(--text-md)' }}>Recent agent turns</h3>
            {list.length === 0 ? <p style={{ margin: '0', color: 'var(--text-muted)', fontSize: 'var(--text-sm)' }}>No agent has answered in this room.</p> : null}
            {list.length ? (
              <div mix={tableWrap}>
                <table aria-label="Agent turns">
                  <thead>
                    <tr>
                      <th>Agent</th>
                      <th>Round</th>
                      <th>Reason</th>
                      <th>Status</th>
                      <th>Model</th>
                      <th>Tokens</th>
                      <th>Cost</th>
                    </tr>
                  </thead>
                  <tbody>
                    {list.map((tr) => (
                      <tr key={tr.turnId} data-testid="room-turn">
                        <td>{name(tr.agentId)}</td>
                        <td>round {tr.round}</td>
                        <td>{tr.reason}</td>
                        <td>
                          {tr.status}
                          {tr.error ? `: ${tr.error}` : ''}
                        </td>
                        <td data-testid="room-turn-model">
                          {tr.model ? <code>{tr.model}</code> : <span style={{ color: 'var(--text-muted)' }}>not recorded</span>}
                          {tr.decisionId ? (
                            <Button
                              variant="link"
                              size="sm"
                              aria-expanded={explainOf === tr.decisionId}
                              mix={[
                                on('click', () => {
                                  explainOf = explainOf === tr.decisionId ? undefined : tr.decisionId;
                                  update();
                                }),
                              ]}
                            >
                              Why this model?
                            </Button>
                          ) : null}
                        </td>
                        <td>
                          {tr.inputTokens}/{tr.outputTokens} tokens
                        </td>
                        <td>{formatCost(tr.costUsd)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : null}
            {explainOf && list.some((tr) => tr.decisionId === explainOf) ? <ExplainText key={explainOf} decisionId={explainOf} /> : null}
          </section>
        </div>
      </details>
    );
  };
}
