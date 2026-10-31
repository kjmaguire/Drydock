import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { bindingProblem, bindings, comboFor, comboOf, defaultCombo, KEYMAP, keyName, keysOf, RESERVED, shellKey, SHORTCUTS, shortcutOf, useBindings, type KeyPress } from './keys.ts';

afterEach(() => useBindings({}));

const key = (k: string, mods: Partial<KeyPress> = {}): KeyPress => ({ key: k, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, target: null, ...mods });

describe('keyboard shortcuts and their bindings (decision P132)', () => {
  it('names a key press as a combination: modifiers in order, letters in capitals, keys by their place where Shift changes them', () => {
    expect(comboOf(key('k', { ctrlKey: true }))).toBe('Ctrl+K');
    expect(comboOf(key('K', { ctrlKey: true, shiftKey: true }))).toBe('Ctrl+Shift+K');
    expect(comboOf(key('ArrowUp', { altKey: true }))).toBe('Alt+Up');
    expect(comboOf(key('`', { ctrlKey: true, code: 'Backquote' }))).toBe('Ctrl+`');
    // a German layout makes ` a dead key; the place on the keyboard still names it
    expect(comboOf(key('Dead', { ctrlKey: true, code: 'Backquote' }))).toBe('Ctrl+`');
    expect(comboOf(key('!', { ctrlKey: true, shiftKey: true, code: 'Digit1' }))).toBe('Ctrl+Shift+1');
    expect(comboOf(key('1', { metaKey: true, code: 'Digit1' }))).toBe('Ctrl+1');
    expect(comboOf(key('Control', { ctrlKey: true }))).toBe('');
    expect(keyName({ key: 'F5' })).toBe('F5');
  });

  it('every shortcut of the map has the default keys of its line, and the shell finds each by its keys', () => {
    expect(SHORTCUTS.map((l) => l.id)).toEqual([
      'palette',
      'inbox',
      'sidebar',
      'focus-sidebar',
      'focus-main',
      'focus-details',
      'focus-dock',
      'dock',
      'terminal',
      'diff',
      'inspect',
      'room-prev',
      'room-next',
      'new-chat',
    ]);
    expect([defaultCombo('inbox'), defaultCombo('terminal'), defaultCombo('room-next')]).toEqual(['Ctrl+Shift+K', 'Ctrl+`', 'Alt+Down']);
    expect(shortcutOf(key('K', { ctrlKey: true, shiftKey: true }))).toBe('inbox');
    expect(shortcutOf(key('3', { ctrlKey: true, code: 'Digit3' }))).toBe('focus-details');
    expect(shortcutOf(key('ArrowDown', { altKey: true }))).toBe('room-next');
    expect(shortcutOf(key('x', { ctrlKey: true }))).toBe(undefined);
    // nothing inside a widget that owns its keys
    const term = { closest: (sel: string) => (sel.includes('.xterm') ? {} : null) } as unknown as EventTarget;
    expect(shortcutOf(key('`', { ctrlKey: true, code: 'Backquote', target: term }))).toBe(undefined);
    expect(shellKey(key('b', { ctrlKey: true }))).toBe('sidebar');
  });

  it("a person's binding replaces the default: the shell follows it, the map shows it, the default keys are free", () => {
    useBindings({ inbox: 'Ctrl+Alt+I', palette: 'Ctrl+K', nonsense: 'Ctrl+Q' });
    expect(bindings()).toEqual({ inbox: 'Ctrl+Alt+I' });
    expect(comboFor('inbox')).toBe('Ctrl+Alt+I');
    expect(shortcutOf(key('i', { ctrlKey: true, altKey: true }))).toBe('inbox');
    expect(shortcutOf(key('K', { ctrlKey: true, shiftKey: true }))).toBe(undefined);
    expect(keysOf(KEYMAP.find((l) => l.id === 'inbox')!)).toEqual(['Ctrl', 'Alt', 'I']);
    expect(keysOf(KEYMAP.find((l) => !l.id)!)).toEqual(['G', 'then', 'I']);
    useBindings({ sidebar: 'Alt+B' });
    expect([shellKey(key('b', { altKey: true })), shellKey(key('b', { ctrlKey: true }))]).toEqual(['sidebar', undefined]);
  });

  it('refuses keys a shortcut cannot take, and says why in a sentence', () => {
    expect(bindingProblem('inbox', 'Ctrl+Alt+I')).toBe('');
    expect(bindingProblem('inbox', 'Shift+I')).toBe('Hold Ctrl or Alt with it, so it never fires while you type.');
    expect(bindingProblem('inbox', 'Ctrl+Space')).toBe('Choose a letter, a digit, a punctuation key, an arrow or F1 to F12.');
    expect(bindingProblem('inbox', 'Ctrl+R')).toBe('Ctrl+R is kept by the window or by the text fields.');
    expect(bindingProblem('inbox', 'Ctrl+B')).toBe('Ctrl+B already does this: Fold or unfold the sidebar (also [ when not typing).');
    // its own default is no conflict, and keys another shortcut was moved off are free
    expect(bindingProblem('dock', 'Ctrl+J')).toBe('');
    expect(bindingProblem('inbox', 'Ctrl+J', { dock: 'Ctrl+Alt+J' })).toBe('');
    expect(bindingProblem('inbox', 'Ctrl+Alt+J', { dock: 'Ctrl+Alt+J' })).toBe('Ctrl+Alt+J already does this: Show or hide the dock.');
    expect(RESERVED.includes('Ctrl+Enter')).toBe(true);
  });
});
