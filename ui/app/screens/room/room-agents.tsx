// Who answers in this room, how, and what the agents have done (room-protocol turn-taking). It is the Room tab of the details
// panel on the right (UI review P1-5: it no longer opens over the messages; the channel header keeps the avatar stack and a
// mode chip that opens this tab). The turns' tokens and cost are folded away under "Recent agent turns". Changing members or the
// mode is a human action, and the core has the last word on every change.

import { css, on, type Handle } from 'remix/component';
import { MessageSquareReply, Trash2, UserPlus } from 'lucide';
import type { Agent, Room, RoomMember } from '../../api/types.ts';
import { formatCost } from '../../lib/format.ts';
import { parseWhole } from '../../lib/limits.ts';
import { action, query, services } from '../../state/services.tsx';
import { ConfirmAction } from '../shared/confirm-action.tsx';
import { ExplainText } from '../shared/explain.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, Badge } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { errorAttrs, Field } from '../../ui/form.tsx';
import { Select, selectValues } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { lastHumanMessageId, memberKey, MODE_TEXT } from './messages.ts';
import { tightList } from '../../ui/layout.tsx';

const railCss = css({ display: 'grid', gap: t.s3, minWidth: '0' });
const turnsCss = css({
  display: 'grid',
  gap: t.s2,
  minWidth: '0',
  '& > summary': { cursor: 'pointer', color: t.textMuted, fontSize: t.textSm, fontWeight: '500' },
  '& > summary:hover': { color: t.text },
});
const cardCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  borderRadius: t.rLg,
  background: t.bgApp,
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
const listCss = tightList;
/** What the round limit says when its text is not a whole number (M25): a typo is never turned into the default. */
export const ROUNDS_ERROR = 'Enter the round limit as a whole number greater than zero, for example 3, or leave it empty for the default.';
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
  let roundsError: string | undefined;
  let invite = '';
  let explainOf: string | undefined; // the decision whose "Why this model?" is open
  const update = () => void handle.update();

  const name = (key: string) => (key.startsWith('human:') ? key.slice(6) : (handle.props.agents.find((a) => a.id === key)?.name ?? key));

  const saveMode = (button: HTMLElement) => {
    // empty is the core's default (0); text that is not a whole number is told under the field and nothing is sent
    const limit = parseWhole(rounds, 1, ROUNDS_ERROR);
    if (!limit.ok) {
      roundsError = limit.error;
      act.clear();
      handle.queueTask(() => button.closest('[data-testid="room-agents"]')?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
      return undefined;
    }
    roundsError = undefined;
    return act.run('mode', () =>
      s.client.roomSetMode({
        roomId: handle.props.room.id,
        mode,
        order: mode === 'sequential' ? order : [],
        moderator: mode === 'moderator' ? moderator : '',
        maxRounds: limit.value ?? 0,
      }),
    );
  };

  return () => {
    const { room, agents, members, lastHumanMessageId } = handle.props;
    const memberKeys = members.map(memberKey);
    const agentMembers = members.filter((m) => m.member.kind === 'agent');
    const invitable = agents.filter((a) => !memberKeys.includes(a.id));
    const list = turns.data ?? [];
    const busy = act.busy !== '';
    return (
      <div mix={railCss} class="room-agents" data-testid="room-agents">
        <ErrorNote error={act.error ?? turns.error} />
        <section mix={cardCss} aria-label="Turn-taking mode">
          <h3>Turn-taking mode</h3>
          <p class="muted">
            Current mode: <Badge tone="accent">{room.mode}</Badge>
          </p>
          <Field label="Mode">
            {(id) => (
              <Select
                id={id}
                label="Mode"
                value={mode}
                options={selectValues(['auto', 'sequential', 'moderator'])}
                onChange={(v) => {
                  mode = v;
                  update();
                }}
              />
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
                    <Select
                      id={id}
                      label="Moderator"
                      value={moderator}
                      options={[{ value: '', label: 'Choose a moderator' }, ...memberKeys.map((k) => ({ value: k, label: name(k) }))]}
                      onChange={(v) => {
                        moderator = v;
                        update();
                      }}
                    />
                  )}
                </Field>
              </div>
            ) : null}
            <div style={roundsError ? { flex: '1 1 280px' } : { width: '120px' }}>
              <Field label="Round limit" error={roundsError}>
                {(id) => (
                  <input
                    id={id}
                    aria-label="Round limit"
                    inputMode="numeric"
                    value={rounds}
                    {...errorAttrs(id, roundsError)}
                    mix={[
                      on('input', (e) => {
                        rounds = (e.currentTarget as HTMLInputElement).value;
                        roundsError = undefined;
                        update();
                      }),
                    ]}
                  />
                )}
              </Field>
            </div>
            <Button variant="primary" disabled={busy} mix={[on('click', (e) => void saveMode(e.currentTarget))]}>
              {act.busy === 'mode' ? 'Saving...' : 'Save mode'}
            </Button>
          </div>
        </section>
        <section mix={cardCss} aria-label="Agents">
          <h3>Agents</h3>
          <ul mix={listCss}>
            {agentMembers.map((m) => (
              <li key={memberKey(m)} mix={memberRow}>
                <Avatar id={m.member.id} name={name(m.member.id)} size={28} kind="agent" />
                <div class="who">
                  <strong>{name(m.member.id)}</strong>
                  <span>
                    {m.role} {m.state}
                  </span>
                </div>
                <ConfirmAction
                  label="Remove"
                  icon={Trash2}
                  yes="Yes, remove"
                  yesName={`Yes, remove ${name(m.member.id)} from this room`}
                  keepName={`Keep ${name(m.member.id)} in this room`}
                  question={`Remove ${name(m.member.id)} from this room? It stops taking part here, and inviting it again starts it fresh.`}
                  disabled={busy}
                  testId="room-agent-remove"
                  onConfirm={() => act.run('remove', () => s.client.roomRemoveMember(room.id, m.member.id))}
                />
              </li>
            ))}
            {agentMembers.length === 0 ? (
              <li class="muted" style={{ padding: '6px 8px' }}>
                No agent is in this room.
              </li>
            ) : null}
          </ul>
          <div mix={inlineRow}>
            <Select
              label="Invite an agent"
              value={invite}
              style={{ flex: '1', minWidth: '160px', width: 'auto' }}
              options={[{ value: '', label: 'Invite an agent...' }, ...invitable.map((a) => ({ value: a.id, label: `${a.name} (${a.role})` }))]}
              onChange={(v) => {
                invite = v;
                update();
              }}
            />
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
        <details mix={turnsCss} data-testid="room-turns" aria-label="Recent agent turns">
          <summary>Recent agent turns{list.length ? ` (${list.length})` : ''}</summary>
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
        </details>
      </div>
    );
  };
}

/** The Room tab of the details panel: reads the room, its members and its newest messages itself, so it follows the room open. */
export function RoomTab(handle: Handle<{ roomId: string }>) {
  const s = services(handle);
  const room = query(handle, {
    load: () => s.client.roomGet(handle.props.roomId),
    when: (e) => e.type.startsWith('room.'),
    key: () => handle.props.roomId,
    debounceMs: 200,
  });
  const members = query(handle, {
    load: () => s.client.roomMembers(handle.props.roomId),
    when: (e) => e.type.startsWith('room.member'),
    key: () => handle.props.roomId,
    debounceMs: 200,
  });
  const recent = query(handle, {
    load: () => s.client.roomMessagesLatest(handle.props.roomId, 50),
    when: (e) => e.type === 'room.message_posted',
    key: () => handle.props.roomId,
    debounceMs: 300,
  });
  return () => {
    const r = room.data?.id === handle.props.roomId ? room.data : undefined;
    if (!r) return room.error ? <ErrorNote error={room.error} what="Could not load the room" /> : <Loading label="Loading the room..." />;
    if (r.archived) return <p style={{ margin: '0', color: 'var(--text-muted)', fontSize: 'var(--text-sm)' }}>This channel is archived: its members and mode cannot change.</p>;
    return (
      <>
        <ErrorNote error={members.error} what="Could not load the members" />
        <RoomAgentsPanel key={r.id} room={r} agents={s.project.agentList} members={members.data ?? []} lastHumanMessageId={lastHumanMessageId(recent.data ?? [])} />
      </>
    );
  };
}
