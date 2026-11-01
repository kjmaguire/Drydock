// The shell's regions and the keys that move between them (decision P132). The shell is four regions, each marked with
// data-region: the sidebar (Ctrl+1), the page (Ctrl+2), the details panel beside it (Ctrl+3) and the dock under it (Ctrl+4).
// Alt+Up and Alt+Down go to the room or chat above or below the open one, in the order the Rooms rail or the chat list shows.

import type { Room } from '../api/types.ts';
import type { ShortcutId } from '../lib/keys.ts';

export type Region = 'sidebar' | 'main' | 'details' | 'dock';

/** The region each Ctrl+number shortcut moves the keyboard to. */
export const REGION_OF: Partial<Record<ShortcutId, Region>> = {
  'focus-sidebar': 'sidebar',
  'focus-main': 'main',
  'focus-details': 'details',
  'focus-dock': 'dock',
};

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * Moves the keyboard into a region: in the sidebar the link of the page open (else its first), in the details panel and the
 * dock their chosen tab (else their first control), the page itself for the page (the target of "Skip to content"). False when
 * the region is not on this page (a workspace page has no details panel and no dock) or holds nothing to focus.
 */
export function focusRegion(r: Region, root: ParentNode = document): boolean {
  const region = root.querySelector<HTMLElement>(`[data-region="${r}"]`);
  if (!region) return false;
  const target =
    r === 'main'
      ? region
      : (region.querySelector<HTMLElement>('[aria-current="page"]') ?? region.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]') ?? region.querySelector<HTMLElement>(FOCUSABLE));
  if (!target) return false;
  target.focus();
  return true;
}

/** The rooms as the Rooms rail lists them: team rooms, then task rooms, then direct messages, none archived. */
export function roomOrder(rooms: readonly Room[]): Room[] {
  const open = rooms.filter((r) => !r.archived);
  return [...open.filter((r) => r.kind === 'channel'), ...open.filter((r) => r.kind === 'task'), ...open.filter((r) => r.kind === 'dm')];
}

/** The item above (-1) or below (1) the one with id in a list; none past either end, or when id is not in it. */
export function neighbour<T extends { id: string }>(list: readonly T[], id: string, dir: 1 | -1): T | undefined {
  const i = list.findIndex((x) => x.id === id);
  return i < 0 ? undefined : list[i + dir];
}

/** The chats the chat list shows, in its order (their ids, read from its rows). */
export function chatOrder(root: ParentNode = document): { id: string }[] {
  return Array.from(root.querySelectorAll<HTMLElement>('[data-testid="chat-row"][data-chat-id]')).map((r) => ({ id: r.dataset.chatId ?? '' }));
}
