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
 * Both follow the person's bindings (decision P132).
 */
export function shellKey(e: KeyPress): 'sidebar' | 'inspect' | undefined {
  const id = shortcutOf(e);
  return id === 'sidebar' || id === 'inspect' ? id : undefined;
}

/** How soon a second Esc must follow the first to stop the agent that is answering (Esc Esc in a chat's or a room's message box). */
export const ESC_AGAIN_MS = 1500;

/** Whether an Esc at now is the second of two (the first at firstAt, 0 for none) close enough to stop the agent. */
export function escapesTwice(firstAt: number, now: number): boolean {
  return firstAt > 0 && now - firstAt <= ESC_AGAIN_MS;
}

/** The shortcuts a person can bind to other keys (decision P132): each is a line of KEYMAP with its id. */
export type ShortcutId =
  | 'palette'
  | 'inbox'
  | 'sidebar'
  | 'focus-sidebar'
  | 'focus-main'
  | 'focus-details'
  | 'focus-dock'
  | 'dock'
  | 'terminal'
  | 'diff'
  | 'inspect'
  | 'room-prev'
  | 'room-next'
  | 'new-chat';

/**
 * One line of the keyboard map: the keys (each a <kbd>; "then" between keys pressed one after the other), what they do, where.
 * A line with an id can be bound to other keys; its keys are the default.
 */
export interface KeyLine {
  id?: ShortcutId;
  keys: string[];
  action: string;
  where: string;
}

/**
 * The keyboard map as built, which Settings > About shows with the keys in use. It lists only the shortcuts that exist: a line
 * here is a promise the app keeps. The internal/core/ext_keys.go list of ids and defaults is checked against it by a Go test.
 */
export const KEYMAP: readonly KeyLine[] = [
  { id: 'palette', keys: ['Ctrl', 'K'], action: 'Open the command menu', where: 'Everywhere' },
  { id: 'inbox', keys: ['Ctrl', 'Shift', 'K'], action: 'Open the Inbox', where: 'Everywhere' },
  { keys: ['G', 'then', 'I'], action: 'Go to the Inbox (G then T: Tasks, G then C: Crew)', where: 'Everywhere, not while typing' },
  { id: 'sidebar', keys: ['Ctrl', 'B'], action: 'Fold or unfold the sidebar (also [ when not typing)', where: 'Everywhere' },
  { id: 'focus-sidebar', keys: ['Ctrl', '1'], action: 'Move the keyboard to the sidebar', where: 'Everywhere' },
  { id: 'focus-main', keys: ['Ctrl', '2'], action: 'Move the keyboard to the page', where: 'Everywhere' },
  { id: 'focus-details', keys: ['Ctrl', '3'], action: 'Move the keyboard to the details panel', where: "A project's pages" },
  { id: 'focus-dock', keys: ['Ctrl', '4'], action: 'Move the keyboard to the dock', where: "A project's pages" },
  { id: 'dock', keys: ['Ctrl', 'J'], action: 'Show or hide the dock', where: "A project's pages" },
  { id: 'terminal', keys: ['Ctrl', '`'], action: "Show or hide the dock's terminal", where: "A project's pages" },
  { id: 'diff', keys: ['Ctrl', 'Shift', 'D'], action: 'Open the dock on the diff', where: "A project's pages" },
  { id: 'inspect', keys: ['Ctrl', '.'], action: 'Open the Agent Inspector for the agent', where: "An agent's page, a direct message" },
  { id: 'room-prev', keys: ['Alt', 'Up'], action: 'Go to the room or chat above', where: 'A room, a chat' },
  { id: 'room-next', keys: ['Alt', 'Down'], action: 'Go to the room or chat below', where: 'A room, a chat' },
  { id: 'new-chat', keys: ['Ctrl', 'N'], action: 'Start a new chat', where: 'Chats' },
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

/** The lines a person can bind to other keys, in the map's order. */
export const SHORTCUTS: readonly (KeyLine & { id: ShortcutId })[] = KEYMAP.filter((l): l is KeyLine & { id: ShortcutId } => !!l.id);

/** A shortcut's default keys as one combination ("Ctrl+Shift+K"). */
export const defaultCombo = (id: ShortcutId): string => SHORTCUTS.find((l) => l.id === id)?.keys.join('+') ?? '';

// --- the person's bindings (decision P132): kept by the core per workspace (keys.get, keys.set), applied here ---

let bound: Partial<Record<ShortcutId, string>> = {};

/** Applies the person's bindings (the ones that differ from the defaults; an unknown id is ignored). */
export function useBindings(b: Partial<Record<string, string>> | null | undefined): void {
  const next: Partial<Record<ShortcutId, string>> = {};
  for (const l of SHORTCUTS) {
    const c = b?.[l.id];
    if (c && c !== defaultCombo(l.id)) next[l.id] = c;
  }
  bound = next;
}

/** The person's bindings that differ from the defaults. */
export const bindings = (): Partial<Record<ShortcutId, string>> => ({ ...bound });

/** The combination a shortcut has now: the person's, else the default. */
export const comboFor = (id: ShortcutId): string => bound[id] ?? defaultCombo(id);

/** The keys of a line of the map as they are now (a bound line shows the person's keys). */
export const keysOf = (l: KeyLine): string[] => (l.id ? comboFor(l.id).split(/\+(?!$)/) : l.keys);

/** What a key handler reads of a key press (a plain object in tests). */
export type KeyPress = Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey' | 'target'> & { code?: string };

// a key by its place on the keyboard where the character changes with Shift or the layout makes it a dead key (Ctrl+` on a
// German layout); letters are read as the layout names them
const CODES: Record<string, string> = {
  Backquote: '`',
  Period: '.',
  Comma: ',',
  Slash: '/',
  Semicolon: ';',
  Quote: "'",
  BracketLeft: '[',
  BracketRight: ']',
  Minus: '-',
  Equal: '=',
  Backslash: '\\',
};
const NAMED: Record<string, string> = { ArrowUp: 'Up', ArrowDown: 'Down', ArrowLeft: 'Left', ArrowRight: 'Right' };
const MODIFIERS = new Set(['Control', 'Shift', 'Alt', 'Meta', 'AltGraph', 'OS']);

/** The key of a press as a binding names it: a letter in capitals, a digit, a punctuation key, Up, F5 ("" for a modifier alone). */
export function keyName(e: Pick<KeyPress, 'key' | 'code'>): string {
  const code = e.code ?? '';
  if (/^Digit[0-9]$/.test(code)) return code.slice(5);
  if (CODES[code]) return CODES[code];
  if (MODIFIERS.has(e.key) || e.key === 'Dead' || e.key === '') return '';
  if (e.key.length === 1) return e.key.toUpperCase();
  return NAMED[e.key] ?? e.key;
}

/** A key press as a combination: "Ctrl+Shift+K", "Alt+Up" (Cmd counts as Ctrl); "" for a modifier alone. */
export function comboOf(e: KeyPress): string {
  const k = keyName(e);
  if (!k) return '';
  return [e.ctrlKey || e.metaKey ? 'Ctrl' : '', e.altKey ? 'Alt' : '', e.shiftKey ? 'Shift' : '', k].filter(Boolean).join('+');
}

/** The shortcut a key press is (by the bindings in use), unless the press is inside a widget that owns its keys. */
export function shortcutOf(e: KeyPress): ShortcutId | undefined {
  if (ownsKeys(e.target)) return undefined;
  const c = comboOf(e);
  if (!c) return undefined;
  return SHORTCUTS.find((l) => comboFor(l.id) === c)?.id;
}

/**
 * Combinations a binding may not take (decision P132): the window's own (reload, find, print, developer tools, close; WebView2
 * acts on them before the page, docs/ui.md 14), the editing keys every field needs, and the keys the message boxes send with.
 */
export const RESERVED: readonly string[] = [
  'Ctrl+R',
  'Ctrl+Shift+R',
  'F5',
  'F12',
  'Ctrl+Shift+I',
  'Ctrl+Shift+J',
  'Ctrl+P',
  'Ctrl+F',
  'Ctrl+W',
  'Alt+F4',
  'Ctrl+A',
  'Ctrl+C',
  'Ctrl+V',
  'Ctrl+X',
  'Ctrl+Y',
  'Ctrl+Z',
  'Ctrl+Enter',
];

const KEY_OK = /^([A-Z0-9`.,/;'[\]\-=\\]|Up|Down|Left|Right|Home|End|PageUp|PageDown|F([1-9]|1[0-2]))$/;

/**
 * Why a combination cannot be the binding of a shortcut, or "" when it can: it needs Ctrl or Alt (a plain key would fire while
 * typing), a key the map can name, nothing the window or the fields keep, and no other shortcut on it. The core checks the same.
 */
export function bindingProblem(id: ShortcutId, combo: string, b: Partial<Record<ShortcutId, string>> = bound): string {
  const parts = combo.split(/\+(?!$)/);
  const key = parts[parts.length - 1] ?? '';
  const mods = parts.slice(0, -1);
  if (!combo || !KEY_OK.test(key) || mods.some((m) => !['Ctrl', 'Alt', 'Shift'].includes(m))) return 'Choose a letter, a digit, a punctuation key, an arrow or F1 to F12.';
  if (!mods.includes('Ctrl') && !mods.includes('Alt')) return 'Hold Ctrl or Alt with it, so it never fires while you type.';
  if (RESERVED.includes(combo)) return `${combo} is kept by the window or by the text fields.`;
  const other = SHORTCUTS.find((l) => l.id !== id && (b[l.id] ?? defaultCombo(l.id)) === combo);
  return other ? `${combo} already does this: ${other.action}.` : '';
}
