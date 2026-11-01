// The sidebar's New button and its key (decisions P140, P142): one place says what "new" opens. It is the start screen, Home,
// with the cursor in its box; the five-step wizard (/project/new) stays behind its "More options...". On Chats the key keeps
// making a new chat (the Chats screen handles it there).

import { shortcutOf } from '../lib/keys.ts';
import { routes } from '../routes.ts';

/** Where New goes. */
export function newHref(): string {
  return routes.workspace.href();
}

/** Puts the cursor in the start box once it is on the page (tried for about half a second while the page renders). */
export function focusStart(tries = 30): void {
  const el = typeof document === 'undefined' ? null : document.querySelector<HTMLTextAreaElement>('[data-testid="start-text"]');
  if (el) {
    el.focus();
    return;
  }
  if (tries > 0 && typeof requestAnimationFrame === 'function') requestAnimationFrame(() => focusStart(tries - 1));
}

/** Opens what New makes, through the router's navigate (or a test's), and puts the cursor in its box. */
export function openNew(go: (href: string) => unknown): void {
  void Promise.resolve(go(newHref()))
    .catch(() => undefined)
    .then(() => focusStart());
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
