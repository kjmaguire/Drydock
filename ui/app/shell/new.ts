// The sidebar's New button and its key (decision P140): one place says what "new" opens, so the start screen can take it over
// by changing this file alone. Today it is the new-project flow (/project/new).

import { shortcutOf } from '../lib/keys.ts';
import { routes } from '../routes.ts';

/** Where New goes. */
export function newHref(): string {
  return routes.projectNew.href();
}

/** Opens what New makes, through the router's navigate (or a test's). */
export function openNew(go: (href: string) => unknown): void {
  void go(newHref());
}

/**
 * A key press that is New: Ctrl+N (or the person's binding of it, decision P132), not while typing in a field, inside a widget
 * with its own keys, a dialog or a menu. On Chats the same key makes a new chat (the Chats screen keeps it there).
 */
export function isNewKey(e: KeyboardEvent): boolean {
  if (e.repeat || e.defaultPrevented || shortcutOf(e) !== 'new-chat') return false;
  const el = e.target as HTMLElement | null;
  return !(el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName) || el.closest?.('dialog, [role="menu"], [role="listbox"]')));
}
