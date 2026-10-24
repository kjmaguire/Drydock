// Global shortcuts (Ctrl+K, Ctrl+J, Ctrl+Shift+D) are window listeners. Inside an embedded editor they must not fire:
// in the terminal Ctrl+J is a newline and Ctrl+K kills to the end of the line, in Monaco Ctrl+K starts a chord. A host
// that owns its keys carries data-own-keys; the widgets' own roots are recognised too.

const OWNERS = '[data-own-keys], .xterm, .monaco-editor, .monaco-diff-editor';

/** True when the key event's target sits inside a widget that handles its own keys (the terminal, the diff editor). */
export function ownsKeys(target: EventTarget | null | undefined): boolean {
  const el = target as { closest?: (sel: string) => unknown } | null | undefined;
  return !!el && typeof el.closest === 'function' && el.closest(OWNERS) != null;
}

/**
 * True while the person types or a widget handles its own keys, so a page's single-letter shortcuts ("/", "j") leave the key
 * alone: Ctrl, Alt or Meta held, a field, box or editor, an open dialog, a menu, a listbox or a combobox.
 */
export function typing(e: KeyboardEvent): boolean {
  if (e.ctrlKey || e.metaKey || e.altKey || ownsKeys(e.target)) return true;
  const el = e.target as HTMLElement | null;
  if (!el || typeof el.closest !== 'function') return false;
  return el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName) || el.closest('dialog[open], [role="menu"], [role="listbox"], [role="combobox"]') !== null;
}
