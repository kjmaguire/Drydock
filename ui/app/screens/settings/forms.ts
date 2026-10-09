// Pure form helpers shared by the workspace screens.

/** Parse a number field: '' means unset. NaN is returned as undefined so the core never sees it. */
export function numOrUndef(s: string): number | undefined {
  const v = s.trim();
  if (v === '') return undefined;
  const n = Number(v);
  return Number.isFinite(n) ? n : undefined;
}
