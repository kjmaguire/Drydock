import { useState } from 'react';
import type { Agent, Room, RoomMember } from '../api/types';
import { formatCost } from '../lib/format';
import { useAction, numOrUndef } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote } from './common';

export const memberKey = (m: RoomMember): string => (m.member.kind === 'human' ? `human:${m.member.id}` : m.member.kind === 'tool' ? `tool:${m.member.id}` : m.member.kind === 'system' ? 'system' : m.member.id);

export const MODE_TEXT: Record<string, string> = {
  auto: 'Auto: simple rules decide who answers a message (an explicit mention first, then the task assignee, then a role match). No model chooses the speaker, and agents do not talk to each other without a bound.',
  sequential: 'Sequential: the members you list speak in that order, one turn each per round, and the debate stops at the round limit.',
  moderator: 'Moderator: the moderator you pick (you or an agent) names who speaks next. If the moderator is an agent, its choices are ordinary agent turns and are billed.',
};

/** Who answers in this room, how, and what the agents have done. Changing members or the mode is a human action. */
export function RoomAgentsPanel({ room, agents, members, lastHumanMessageId }: { room: Room; agents: readonly Agent[]; members: readonly RoomMember[]; lastHumanMessageId?: string }) {
  const client = useClient();
  const act = useAction();
  const turns = useLive(() => client.roomTurns(room.id, 20), [room.id], (e) => e.type.startsWith('room.') || e.type.startsWith('agent.'), true, 400);
  const [mode, setMode] = useState<string>(room.mode);
  const [order, setOrder] = useState<string[]>(room.modeParams?.order ?? []);
  const [moderator, setModerator] = useState(room.modeParams?.moderator ?? '');
  const [rounds, setRounds] = useState(String(room.modeParams?.max_rounds ?? 3));
  const [invite, setInvite] = useState('');
  const memberKeys = members.map(memberKey);
  const agentMembers = members.filter((m) => m.member.kind === 'agent');
  const invitable = agents.filter((a) => !memberKeys.includes(a.id));
  const name = (key: string) => (key.startsWith('human:') ? key.slice(6) : (agents.find((a) => a.id === key)?.name ?? key));
  return (
    <details className="room-agents" data-testid="room-agents">
      <summary>Agents in this room and how they answer</summary>
      <ErrorNote error={act.error ?? turns.error} />
      <p>
        Current mode: <Badge>{room.mode}</Badge>
      </p>
      <fieldset>
        <legend>Turn-taking mode</legend>
        <label>
          Mode
          <select aria-label="Mode" value={mode} onChange={(e) => setMode(e.target.value)}>
            <option value="auto">auto</option>
            <option value="sequential">sequential</option>
            <option value="moderator">moderator</option>
          </select>
        </label>
        <p className="muted" data-testid="mode-text">{MODE_TEXT[mode]}</p>
        {mode === 'sequential' ? (
          <div>
            <p>Speaking order: {order.length ? order.map(name).join(' > ') : 'not set'}</p>
            {memberKeys.filter((k) => k !== 'system').map((k) => (
              <button key={k} className="link-button" disabled={order.includes(k)} onClick={() => setOrder([...order, k])}>
                Add {name(k)}
              </button>
            ))}
            {order.length ? <button className="link-button" onClick={() => setOrder([])}>Clear order</button> : null}
          </div>
        ) : null}
        {mode === 'moderator' ? (
          <label>
            Moderator
            <select aria-label="Moderator" value={moderator} onChange={(e) => setModerator(e.target.value)}>
              <option value="">Choose a moderator</option>
              {memberKeys.map((k) => (
                <option key={k} value={k}>
                  {name(k)}
                </option>
              ))}
            </select>
          </label>
        ) : null}
        <label>
          Round limit
          <input aria-label="Round limit" inputMode="numeric" value={rounds} onChange={(e) => setRounds(e.target.value)} />
        </label>
        <button disabled={act.busy !== ''} onClick={async () => { await act.run('mode', () => client.roomSetMode({ roomId: room.id, mode, order: mode === 'sequential' ? order : [], moderator: mode === 'moderator' ? moderator : '', maxRounds: numOrUndef(rounds) ?? 0 })); }}>
          Save mode
        </button>
      </fieldset>
      <h4>Agents</h4>
      <ul>
        {agentMembers.map((m) => (
          <li key={memberKey(m)}>
            {name(m.member.id)} <span className="muted">{m.role} {m.state}</span>{' '}
            <button className="link-button" onClick={async () => { await act.run('remove', () => client.roomRemoveMember(room.id, m.member.id)); }}>
              Remove
            </button>
          </li>
        ))}
        {agentMembers.length === 0 ? <li className="muted">No agent is in this room.</li> : null}
      </ul>
      <div className="form-actions">
        <select aria-label="Invite an agent" value={invite} onChange={(e) => setInvite(e.target.value)}>
          <option value="">Invite an agent...</option>
          {invitable.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name} ({a.role})
            </option>
          ))}
        </select>
        <button disabled={!invite || act.busy !== ''} onClick={async () => { await act.run('invite', () => client.roomInvite(room.id, invite)); setInvite(''); }}>
          Invite
        </button>
        {lastHumanMessageId ? (
          <button disabled={act.busy !== ''} onClick={async () => { await act.run('reply', () => client.roomReply(lastHumanMessageId)); turns.reload(); }} title="Ask the agents to answer your latest message now">
            Ask agents to reply to my last message
          </button>
        ) : null}
      </div>
      <h4>Recent agent turns</h4>
      {(turns.data ?? []).length === 0 ? <p className="muted">No agent has answered in this room.</p> : null}
      <table className="table" aria-label="Agent turns">
        <tbody>
          {(turns.data ?? []).map((t) => (
            <tr key={t.turnId} data-testid="room-turn">
              <td>{name(t.agentId)}</td>
              <td>round {t.round}</td>
              <td>{t.reason}</td>
              <td>{t.status}{t.error ? `: ${t.error}` : ''}</td>
              <td>{t.inputTokens}/{t.outputTokens} tokens</td>
              <td>{formatCost(t.costUsd)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </details>
  );
}
