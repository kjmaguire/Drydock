// Decisions (X110): the DECISION messages of the project's rooms as a list (room.adrCandidatesForProject), each with a one-click
// task that writes it down as an ADR. Pure helpers; the room writes no files: the task does, in a worktree, with review.

import type { RoomDecision } from '../../api/runClient.ts';
import type { TaskCreateReq } from '../../api/types.ts';

/** The decisions a room's list shows: every room's in #decisions, the room's own elsewhere; newest last, as the rooms hold them. */
export function decisionsFor(all: readonly RoomDecision[], room: { id: string; kind: string; name: string }): RoomDecision[] {
  if (room.kind === 'channel' && room.name === 'decisions') return [...all];
  return all.filter((d) => d.roomId === room.id);
}

/** Where a decision was taken, in words: "#general", "a direct message". */
export const decisionWhere = (d: Pick<RoomDecision, 'roomKind' | 'roomName'>): string => (d.roomKind === 'dm' ? 'a direct message' : `#${d.roomName}`);

/** The task "Make an ADR task" creates: the decision as the description, the ADR as the acceptance, labelled adr. */
export function adrTaskRequest(projectId: string, d: RoomDecision): TaskCreateReq {
  const title = (d.title || d.decision || 'Decision').split('\n')[0]!.trim().slice(0, 120);
  const parts = [`Write an architecture decision record (ADR) for the decision taken in ${decisionWhere(d)}.`, '', `Decision: ${d.decision || d.title}`];
  if (d.context) parts.push('', `Context: ${d.context}`);
  if (d.consequences) parts.push('', `Consequences: ${d.consequences}`);
  if (d.alternatives.length) parts.push('', `Alternatives considered: ${d.alternatives.join('; ')}`);
  parts.push('', `Source messages: ${d.source_message_ids.join(', ')}`);
  return {
    projectId,
    title: `ADR: ${title}`,
    description: parts.join('\n'),
    acceptance: ['An ADR file in the repository states the context, the decision, its consequences and the alternatives considered'],
    labels: ['adr'],
  };
}
