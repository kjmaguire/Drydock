// Text that an agent, a repository or a remote service wrote, shown where a person decides something (an approval card, a
// file list, a path, a command). Some characters change how text is drawn without being drawn themselves: the right-to-left
// overrides and isolates (U+202A-202E, U+2066-2069) reorder what follows, zero-width characters and joiners hide, the soft
// hyphen and the tag characters disappear, and other control characters move or erase what was printed. Text with them can
// read as a different file or command from the one it is (audit 2026-10-16, H8). `visible` shows each as a marker such as
// `\u{202e}`, so the person reads what is there. Everything else, including text in right-to-left scripts, is left alone.

/** Control (Cc), format (Cf), line and paragraph separator (Zl, Zp) and lone surrogate (Cs) characters, except tab, line feed and carriage return. */
const HIDDEN = /[^\P{Cc}\t\n\r]|[\p{Cf}\p{Zl}\p{Zp}\p{Cs}]/gu;

/** The text with every character that is drawn as nothing, or that reorders text, written out as `\u{hex}`. */
export function visible(text: string | undefined | null): string {
  if (!text) return '';
  return text.replace(HIDDEN, (c) => `\\u{${(c.codePointAt(0) ?? 0).toString(16)}}`);
}

/** Whether the text holds a character `visible` would write out. */
export function hasHidden(text: string | undefined | null): boolean {
  if (!text) return false;
  HIDDEN.lastIndex = 0;
  return HIDDEN.test(text);
}
