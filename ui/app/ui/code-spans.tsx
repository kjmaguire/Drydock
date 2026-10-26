import type { RemixNode } from 'remix/component';

/**
 * A sentence from the core with its commands in backticks ("run `gh auth login`") as text with those parts in <code>: shown as
 * plain text, the backticks were printed as they are (the UI audit of 2026-10-26). Only paired backticks count; a sentence with an
 * odd number of them is left as it is. The text is never parsed as HTML or Markdown.
 */
export function codeSpans(text: string): RemixNode[] {
  const parts = text.split('`');
  if (parts.length % 2 === 0) return [text];
  return parts.map((p, i) => (i % 2 === 1 ? <code key={i}>{p}</code> : p)).filter((p) => p !== '');
}

/** The sentence with its first letter in capitals: an error the core words for the middle of a sentence, shown as its own. */
export function sentence(text: string): string {
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : text;
}
