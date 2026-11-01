// Rooms, decision P130 (internal/core/ext_rooms.go, room.stop in ext_agents.go): stopping the agent answering in a room, muting
// a room, the stored join rules a person makes and the answer to an agent's request to join a channel. One function per method
// in the core registry; timestamps are unix milliseconds.

import type { Transport } from './transport.ts';
import type { Unread } from './types.ts';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

/** room.JoinRule: adds its agent to each task room created after it whose task names a file under pathPrefix ('' for all). */
export interface JoinRule {
  id: string;
  projectId: string;
  agentId: string;
  pathPrefix?: string;
  /** The member state the agent joins with: 'observer' (default) or 'active'. */
  state: string;
  createdBy: string;
  createdAt: number;
}

/** room.JoinRequest: an agent's request to join a channel, answered by a person. */
export interface JoinRequest {
  id: string;
  projectId: string;
  roomId: string;
  agentId: string;
  reason?: string;
  status: 'requested' | 'granted' | 'declined' | string;
  requestedAt: number;
  decidedAt?: number;
  decidedBy?: string;
}

export interface RoomClient {
  /** Stops the agents answering in a room now (Esc Esc in its composer); how many turns it stopped. */
  roomStop(roomId: string): Promise<number>;
  /** Mutes or unmutes a room for the person; the room's unread row as it is now. */
  roomSetMuted(roomId: string, muted: boolean): Promise<Unread>;
  roomJoinRules(projectId: string): Promise<JoinRule[]>;
  roomJoinRuleAdd(rule: { projectId: string; agentId: string; pathPrefix?: string; state?: string }): Promise<JoinRule>;
  roomJoinRuleRemove(id: string): Promise<void>;
  /** Lets the agent into the channel as an observer (allow) or declines its request. */
  roomAnswerJoin(id: string, allow: boolean): Promise<JoinRequest>;
}

export function createRoomClient(t: Transport): RoomClient {
  return {
    roomStop: async (roomId) => (await t.call<{ stopped: number }>('room.stop', { roomId }))?.stopped ?? 0,
    roomSetMuted: (roomId, muted) => t.call<Unread>('room.setMuted', { roomId, muted }),
    roomJoinRules: async (projectId) => arr(await t.call<JoinRule[] | null>('room.joinRules', { projectId })),
    roomJoinRuleAdd: (rule) => t.call<JoinRule>('room.joinRule.add', { pathPrefix: '', state: '', ...rule }),
    roomJoinRuleRemove: async (id) => {
      await t.call('room.joinRule.remove', { id });
    },
    roomAnswerJoin: (id, allow) => t.call<JoinRequest>('room.answerJoin', { id, allow }),
  };
}
