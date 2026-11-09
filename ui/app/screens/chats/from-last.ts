// What a new chat took from the person's last choice (D2): chat.create names the fields it filled ("fromLast"), and the
// composer's chip popover says "From your last chat" the first time it is opened, then forgets it. Pure UI state, per window.

import type { Chat } from '../../api/chatClient.ts';

export type CarriedField = 'model' | 'effort' | 'permissionMode';

const carried = new Map<string, Set<CarriedField>>();

/** Keeps what a new chat took from the last choice, from chat.create's answer. */
export function noteFromLast(c: Pick<Chat, 'id' | 'fromLast'>): void {
  if (c.fromLast?.length) carried.set(c.id, new Set(c.fromLast));
}

/** Whether the chat took any of these fields from the last choice, and the popover has not said so yet. */
export function tookFromLast(id: string, fields: readonly CarriedField[]): boolean {
  const s = carried.get(id);
  return !!s && fields.some((f) => s.has(f));
}

/** The popover said it: it is not said again. */
export function forgetFromLast(id: string, fields: readonly CarriedField[]): void {
  const s = carried.get(id);
  if (!s) return;
  for (const f of fields) s.delete(f);
  if (!s.size) carried.delete(id);
}
