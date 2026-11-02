// Turn-taking in words (X14): the order the core uses when none is set, the moderator a new moderator room starts with, and
// what the header's mode chip says about who answers. Pure, so the panel, the composer's /mode and the chip agree.

import type { Agent, Room, RoomMember } from '../../api/types.ts';
import { findLead } from '../../shell/palette-items.ts';
import { memberKey } from './messages.ts';

/** The order a sequential room uses when none is set (room.nextSequential): its active agents, in the order they joined. */
export function defaultOrder(members: readonly RoomMember[]): string[] {
  return members.filter((m) => m.member.kind === 'agent' && (m.state === 'active' || m.state === '')).map(memberKey);
}

/** "Ada", "Ada, then Bo", "Ada, then Bo, then Cy". */
export function inOrder(names: readonly string[]): string {
  return names.join(', then ');
}

/** The moderator a room starts with in moderator mode: the team lead when it is in the room, otherwise the person. */
export function defaultModerator(members: readonly RoomMember[], agents: readonly Agent[]): string {
  const keys = members.map(memberKey);
  const lead = findLead(agents);
  if (lead && keys.includes(lead.id)) return lead.id;
  return keys.find((k) => k.startsWith('human:')) ?? '';
}

/** What the header's chip says about who answers in the room. */
export function modeWords(room: Pick<Room, 'mode' | 'modeParams'>, agents: readonly Agent[]): string {
  switch (room.mode) {
    case 'sequential':
      return 'Agents take turns';
    case 'moderator': {
      const m = room.modeParams?.moderator ?? '';
      if (!m) return 'A moderator picks who answers';
      if (m.startsWith('human:')) return 'You pick who answers';
      return `${agents.find((a) => a.id === m)?.name ?? m} picks who answers`;
    }
    default:
      return 'Agents answer when mentioned';
  }
}
