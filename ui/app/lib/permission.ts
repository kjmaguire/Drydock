// Permission modes that take the approval step out of an agent's work (core chats.SkipsApprovals; audit 2026-10-16, M1). The core
// refuses to give an agent, a template or a crew member such a mode unless the request carries the person's explicit
// confirmation (`confirmSkipsApprovals`). These helpers are the UI's side of that: which modes need it, the confirmation a form
// keeps, and the refusal the core answers with when a request still arrives without it. Pure, so they are tested in Node.

/** The words next to the confirmation box (the label the accessibility contract and the end-to-end suite rely on). */
export const SKIP_CONFIRM_LABEL = 'I understand this agent will act without asking me first';

/** The permission modes that let an agent act without asking (bypassPermissions, auto, dontAsk). */
export const SKIP_MODES: ReadonlySet<string> = new Set(['bypassPermissions', 'auto', 'dontAsk']);

/** Whether this permission mode lets an agent act without asking. */
export const skipsApprovals = (mode: string | undefined): boolean => !!mode && SKIP_MODES.has(mode);

/**
 * Whether a form still needs the person's confirmation: its mode skips approvals and the box was not ticked for this mode.
 * `confirmedMode` is the mode the box was ticked for ('' when it is not ticked), so choosing another mode asks again.
 */
export const needsConfirmation = (mode: string | undefined, confirmedMode: string | undefined): boolean => skipsApprovals(mode) && confirmedMode !== mode;

/**
 * What a form adds to its request: `confirmSkipsApprovals: true` only when the mode skips approvals and the box was ticked for
 * exactly this mode; nothing otherwise (the core refuses the one and ignores a flag on the other).
 */
export function confirmationFor(mode: string | undefined, confirmedMode: string | undefined): { confirmSkipsApprovals?: true } {
  return skipsApprovals(mode) && confirmedMode === mode ? { confirmSkipsApprovals: true } : {};
}

/** The text a refused request starts with (core.NeedsConfirmationError). */
export const NEEDS_CONFIRMATION = 'needs your confirmation';

/** Whether this is the core's refusal of a permission mode that was not confirmed. */
export const isNeedsConfirmation = (error: unknown): boolean => error instanceof Error && error.message.trim().toLowerCase().startsWith(NEEDS_CONFIRMATION);

/**
 * Splits a failed action's error: the refusal that belongs next to the confirmation box, and any other failure for the form's
 * general error note.
 */
export function splitConfirmation(error: Error | undefined): { confirm: Error | undefined; other: Error | undefined } {
  return error && isNeedsConfirmation(error) ? { confirm: error, other: undefined } : { confirm: undefined, other: error };
}
