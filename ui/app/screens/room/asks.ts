// Asking an agent to answer (X12): what the room says when nobody answered the person's message, which agents can be asked,
// and which SYSTEM and ERROR lines offer Ask again. Pure functions of what the room already read (the responder's last outcome,
// the members, the messages), so the words are tested without a page. Asking is always the person's click; nothing here asks.

import type { ReplyOutcome } from '../../api/extTypes.ts';
import type { Message, RoomMember } from '../../api/types.ts';

/** The codes of the lines that say an agent's turn on a person's message gave no answer: each offers Ask again. */
export const ASK_AGAIN_CODES: ReadonlySet<string> = new Set(['agent_turn_failed', 'agent_turn_interrupted', 'empty_reply', 'agent_turn_stopped']);

/** The turn statuses that gave no answer: the turns table offers Ask again on them when the turn's trigger is the person's. */
export const NO_ANSWER_STATUSES: ReadonlySet<string> = new Set(['failed', 'empty', 'interrupted', 'stopped']);

/** Whether a message is one the person wrote and an agent can be asked to answer (not a steer to a running task agent). */
export function isAskableMessage(m: Pick<Message, 'type' | 'author' | 'payload'> | undefined): boolean {
  return !!m && m.author.kind === 'human' && m.type === 'CHAT' && m.payload?.via !== 'steer';
}

/**
 * The person's message and the agent a SYSTEM or ERROR line is about, when the line offers Ask again; undefined otherwise.
 * In a chain a turn's trigger is often another agent's reply, and the core asks an agent about a person's message only, so
 * the line offers Ask again only when the message it answers is loaded and is the person's (messageOf looks it up).
 */
export function askAgainOf(
  m: Pick<Message, 'type' | 'author' | 'reply_to_id' | 'payload'>,
  messageOf: (id: string) => Pick<Message, 'type' | 'author' | 'payload'> | undefined,
): { messageId: string; agentId: string } | undefined {
  if (m.author.kind !== 'system' || (m.type !== 'SYSTEM' && m.type !== 'ERROR') || !m.reply_to_id) return undefined;
  const code = typeof m.payload?.code === 'string' ? m.payload.code : '';
  const agentId = typeof m.payload?.agent_id === 'string' ? m.payload.agent_id : '';
  if (!ASK_AGAIN_CODES.has(code) || !agentId) return undefined;
  if (!isAskableMessage(messageOf(m.reply_to_id))) return undefined;
  return { messageId: m.reply_to_id, agentId };
}

/** The agents of the room a person can ask to answer: every agent member that is not muted (an observer answers when asked). */
export function askableAgents(members: readonly RoomMember[]): string[] {
  return members.filter((m) => m.member.kind === 'agent' && m.state !== 'muted').map((m) => m.member.id);
}

/** The id of the newest top-level message the person wrote in the list (a steer to a running task agent is not a question). */
export function newestHumanMessage(msgs: readonly Pick<Message, 'id' | 'author' | 'type' | 'payload'>[]): string | undefined {
  for (let i = msgs.length - 1; i >= 0; i--) {
    const m = msgs[i]!;
    if (isAskableMessage(m)) return m.id;
  }
  return undefined;
}

/**
 * Why the person's message got no answer, in words, from the responder's outcome (Outcome.why when the scheduler chose nobody,
 * Outcome.stopped when the chain ended early); empty when agents answered or the reason has no words here.
 */
export function outcomeWords(o: Pick<ReplyOutcome, 'turns' | 'why' | 'stopped' | 'bounded'> | null | undefined): string {
  if (!o) return '';
  const answered = (o.turns ?? []).length > 0;
  const stopped = o.stopped ?? '';
  if (/switched off/.test(stopped)) return "No agent answered: agents do not answer in this project's rooms.";
  if (/archived/.test(stopped)) return 'No agent answered: the room is archived.';
  if (/newer human message/.test(stopped)) return answered ? 'A newer message took over before every agent answered.' : 'A newer message took over before an agent answered.';
  if (/hard cap/.test(stopped)) return 'The agents stopped after the most turns one message may start.';
  if (stopped === 'cancelled') return 'The answer was cancelled.';
  if (answered) return o.bounded ? 'The agents reached the round limit: your next message starts a new round.' : '';
  if (o.bounded) return 'No agent answered: the agents reached the round limit. Your next message starts a new round.';
  switch (o.why ?? '') {
    case 'nobody addressed':
      return 'No agent answered: nobody was mentioned.';
    case 'only invited speakers speak':
    case 'human message in moderated room':
      return 'No agent answered: in this room the moderator picks who answers.';
    case '':
      return '';
    default:
      return 'No agent answered this message.';
  }
}
