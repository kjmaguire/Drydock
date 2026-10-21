// A bounded look at a payload (audit 2026-10-16, M22). An event's data, or a tool's result, can be megabytes; what a row of the
// Activity page needs is the start of it on one line. JSON.stringify would write all of it first.

/**
 * The start of `data` as JSON text, at most `max` characters, and whether more was left out. It reads only as much of the
 * payload as it writes, so a payload of any size costs about `max` characters of work. Plain JSON data comes out as
 * JSON.stringify writes it; what JSON can not hold (undefined, functions, symbols) is left out, as there, and a value nested
 * deeper than 10 levels (or a cycle) is written as "…".
 */
export function previewOf(data: unknown, max: number): { text: string; cut: boolean } {
  if (data === undefined) return { text: '', cut: false };
  const out: string[] = [];
  let len = 0;
  let cut = false;
  // writes s and says whether there is room for more
  const put = (s: string): boolean => {
    if (cut) return false;
    if (len + s.length > max) {
      out.push(s.slice(0, max - len));
      len = max;
      cut = true;
      return false;
    }
    out.push(s);
    len += s.length;
    return true;
  };
  const skipped = (v: unknown) => v === undefined || typeof v === 'function' || typeof v === 'symbol';
  const walk = (v: unknown, depth: number): boolean => {
    if (v === null) return put('null');
    switch (typeof v) {
      case 'string':
        return put(JSON.stringify(v.length > max ? v.slice(0, max + 1) : v));
      case 'number':
        return put(Number.isFinite(v) ? String(v) : 'null');
      case 'boolean':
        return put(v ? 'true' : 'false');
      case 'bigint':
        return put(`"${v}"`);
      case 'object':
        break;
      default:
        return put('null');
    }
    if (depth >= 10) return put('"…"');
    if (Array.isArray(v)) {
      if (!put('[')) return false;
      for (let i = 0; i < v.length; i++) {
        if (i > 0 && !put(',')) return false;
        if (!(skipped(v[i]) ? put('null') : walk(v[i], depth + 1))) return false;
      }
      return put(']');
    }
    const o = v as Record<string, unknown>;
    if (!put('{')) return false;
    let first = true;
    for (const k of Object.keys(o)) {
      const x = o[k];
      if (skipped(x)) continue;
      if (!first && !put(',')) return false;
      first = false;
      if (!put(JSON.stringify(k)) || !put(':') || !walk(x, depth + 1)) return false;
    }
    return put('}');
  };
  try {
    walk(data, 0);
  } catch {
    return { text: out.join(''), cut: true }; // a getter that throws: what was read is the preview
  }
  return { text: out.join(''), cut };
}
