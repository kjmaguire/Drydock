// Strict reading of the numbers a person types into a limit field (audit 2026-10-16, M25). An empty field means "not set". Anything
// else must be a plain number in the field's own form. A text that is not one ("5 dollars", "$5", "1,000", "1e999", "abc") is never
// turned into "no limit": the screen shows the message under the field, marks the field invalid and sends nothing.
//
// Pure: no DOM, no services. The message is the caller's, because only the field knows its unit and an example.

/** What a field holds: nothing typed (`value` is undefined), a number, or the message that says why the text is not one. */
export type Typed = { ok: true; value: number | undefined } | { ok: false; error: string };

const unset = (): Typed => ({ ok: true, value: undefined });
const bad = (error: string): Typed => ({ ok: false, error });

/** An amount in dollars: digits with an optional decimal point ("12", "12.50", ".5"), greater than zero. */
export function parseDollars(text: string, error: string): Typed {
  const s = text.trim();
  if (s === '') return unset();
  if (!/^(\d+\.?\d*|\.\d+)$/.test(s)) return bad(error);
  const n = Number(s);
  return Number.isFinite(n) && n > 0 ? { ok: true, value: n } : bad(error);
}

/** A whole number of at least `min`: digits only ("7"; not "7.5", "1,000", "1e3", "+7" or "-1"). */
export function parseWhole(text: string, min: number, error: string): Typed {
  const s = text.trim();
  if (s === '') return unset();
  if (!/^\d+$/.test(s)) return bad(error);
  const n = Number(s);
  return Number.isSafeInteger(n) && n >= min ? { ok: true, value: n } : bad(error);
}

/** A whole number that may be negative ("-3"), for a field such as a priority. */
export function parseInteger(text: string, error: string): Typed {
  const s = text.trim();
  if (s === '') return unset();
  if (!/^-?\d+$/.test(s)) return bad(error);
  const n = Number(s);
  return Number.isSafeInteger(n) ? { ok: true, value: n } : bad(error);
}

/** A whole number of percent from 1 to 99 (the point where a budget starts to warn). */
export function parsePercent(text: string, error: string): Typed {
  const t = parseWhole(text, 1, error);
  return t.ok && t.value !== undefined && t.value > 99 ? bad(error) : t;
}

/**
 * A number as the plain digits a field takes back ("0.0000001", never "1e-7"), so a value the core holds can be shown in its
 * field and saved again unchanged.
 */
export function numberText(n: number): string {
  const plain = n.toLocaleString('en-US', { useGrouping: false, maximumFractionDigits: 20 });
  return Number(plain) === n ? plain : String(n);
}
