// Environment variable names, checked on the page before anything is sent (audit 2026-10-16, L38).
//
// A person who pastes `AWS_SECRET_ACCESS_KEY=abc...` into a box that takes names has put a secret where only a name belongs. The
// page refuses such a line itself: nothing is sent to the core, and the refusal says which line it is, never what it holds, so the
// secret is not repeated in a message, a title, an attribute or the console.
//
// Pure: no DOM, no services.

/** What the core takes as a variable name (envNameRe in the core): letters, digits and _, at most 64, not starting with a digit. */
export const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;

export const isEnvName = (s: string): boolean => ENV_NAME.test(s);

/** The rule for a name, in words, to follow a refusal. */
export const ENV_NAME_RULE = 'A name has letters, digits and _ only, at most 64, and does not start with a digit.';

/**
 * The lines of a box as the person sees them: numbered from 1, blank lines counted (a message that says "Line 3" means the third
 * line on the screen). A line's own text is returned only so the caller can check it; it must never go into a message.
 */
export function boxLines(text: string): { n: number; text: string }[] {
  return text.split('\n').map((line, i) => ({ n: i + 1, text: line.replace(/\r$/, '') }));
}
