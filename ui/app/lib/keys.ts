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

/**
 * The shell's own shortcuts with Ctrl (decision P129): Ctrl+B folds and unfolds the sidebar (as "[" does), Ctrl+. opens the
 * Agent Inspector for the agent whose page is open. Neither fires inside a widget that owns its keys, as for Ctrl+K and Ctrl+J.
 */
export function shellKey(e: Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey' | 'target'>): 'sidebar' | 'inspect' | undefined {
  if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey || ownsKeys(e.target)) return undefined;
  if (e.key.toLowerCase() === 'b') return 'sidebar';
  if (e.key === '.') return 'inspect';
  return undefined;
}

/** How soon a second Esc must follow the first to stop the agent that is answering (Esc Esc in a chat's or a room's message box). */
export const ESC_AGAIN_MS = 1500;

/** Whether an Esc at now is the second of two (the first at firstAt, 0 for none) close enough to stop the agent. */
export function escapesTwice(firstAt: number, now: number): boolean {
  return firstAt > 0 && now - firstAt <= ESC_AGAIN_MS;
}

/** One line of the keyboard map: the keys (each a <kbd>; "then" between keys pressed one after the other), what they do, where. */
export interface KeyLine {
  keys: string[];
  action: string;
  where: string;
}

/**
 * The keyboard map as built, which Settings > About shows (read-only: rebinding is not built). It lists only the shortcuts that
 * exist: a line here is a promise the app keeps.
 */
export const KEYMAP: readonly KeyLine[] = [
  { keys: ['Ctrl', 'K'], action: 'Open the command menu', where: 'Everywhere' },
  { keys: ['G', 'then', 'I'], action: 'Go to the Inbox (G then T: Tasks, G then C: Crew)', where: 'Everywhere, not while typing' },
  { keys: ['Ctrl', 'B'], action: 'Fold or unfold the sidebar (also [ when not typing)', where: 'Everywhere' },
  { keys: ['Ctrl', 'J'], action: 'Show or hide the dock', where: "A project's pages" },
  { keys: ['Ctrl', 'Shift', 'D'], action: 'Open the dock on the diff', where: "A project's pages" },
  { keys: ['Ctrl', '.'], action: 'Open the Agent Inspector for the agent', where: "An agent's page, a direct message" },
  { keys: ['Ctrl', 'N'], action: 'Start a new chat', where: 'Chats' },
  { keys: ['Enter'], action: 'Send the message (Shift+Enter: a new line)', where: "A chat's message box" },
  { keys: ['Esc', 'Esc'], action: 'Stop the agent that is answering', where: "A chat's or a room's message box" },
  { keys: ['Ctrl', 'Enter'], action: 'Send the message', where: "A room's message box, a thread, a chat" },
  { keys: ['@', '#', '/'], action: 'Suggest agents, tasks and commands', where: "A room's message box" },
  { keys: ['/'], action: 'Search the list', where: 'Agents, Integrations, Models' },
  { keys: ['J', 'K'], action: 'Next and previous item (Enter opens it, E marks it done)', where: 'Inbox' },
  { keys: ['N'], action: 'New agent', where: 'Crew' },
  { keys: ['C'], action: 'New task', where: 'Tasks' },
  { keys: ['Esc'], action: 'Close a dialog, a menu or a popover', where: 'Everywhere' },
];
