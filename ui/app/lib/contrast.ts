// WCAG 2.x contrast arithmetic over the design tokens in styles/app.css. Pure: the token test (styles/contrast.test.ts)
// reads the stylesheet, resolves each light-dark() pair for one scheme and checks the pairs the UI uses as text.

export type Rgba = readonly [r: number, g: number, b: number, a: number];
export type Scheme = 'light' | 'dark';

/** Parse #rgb, #rrggbb, rgb(r g b) or rgb(r g b / a). Returns undefined for anything else. */
export function parseColor(v: string): Rgba | undefined {
  const s = v.trim().toLowerCase();
  let m = /^#([0-9a-f]{3})$/.exec(s);
  if (m) {
    const [r, g, b] = [...m[1]!].map((c) => parseInt(c + c, 16)) as [number, number, number];
    return [r, g, b, 1];
  }
  m = /^#([0-9a-f]{6})$/.exec(s);
  if (m) return [parseInt(m[1]!.slice(0, 2), 16), parseInt(m[1]!.slice(2, 4), 16), parseInt(m[1]!.slice(4, 6), 16), 1];
  m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)\s*(?:[/,]\s*([\d.]+))?\s*\)$/.exec(s);
  if (m) return [Number(m[1]), Number(m[2]), Number(m[3]), m[4] === undefined ? 1 : Number(m[4])];
  return undefined;
}

/** Split a CSS value on top-level commas (commas inside parentheses stay). */
function splitTop(v: string): string[] {
  const out: string[] = [];
  let depth = 0;
  let cur = '';
  for (const ch of v) {
    if (ch === '(') depth++;
    if (ch === ')') depth--;
    if (ch === ',' && depth === 0) {
      out.push(cur.trim());
      cur = '';
    } else cur += ch;
  }
  out.push(cur.trim());
  return out;
}

/** The custom properties declared in the first `:root {` block of the stylesheet, resolved for one scheme. */
export function rootTokens(css: string, scheme: Scheme): Record<string, string> {
  const start = css.search(/:root\s*\{/);
  if (start < 0) return {};
  const body = css.slice(css.indexOf('{', start) + 1, css.indexOf('}', start));
  const out: Record<string, string> = {};
  for (const decl of body.replace(/\/\*[\s\S]*?\*\//g, '').split(';')) {
    const m = /^\s*(--[\w-]+)\s*:\s*([\s\S]+?)\s*$/.exec(decl);
    if (!m) continue;
    let v = m[2]!;
    const ld = /^light-dark\(([\s\S]*)\)$/.exec(v);
    if (ld) {
      const parts = splitTop(ld[1]!);
      v = scheme === 'light' ? parts[0]! : parts[1]!;
    }
    out[m[1]!] = v;
  }
  return out;
}

/** Paint a possibly translucent colour over an opaque one. */
export function over(fg: Rgba, bg: Rgba): Rgba {
  const a = fg[3];
  return [fg[0] * a + bg[0] * (1 - a), fg[1] * a + bg[1] * (1 - a), fg[2] * a + bg[2] * (1 - a), 1];
}

function channel(c: number): number {
  const x = c / 255;
  return x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
}

export function luminance(c: Rgba): number {
  return 0.2126 * channel(c[0]) + 0.7152 * channel(c[1]) + 0.0722 * channel(c[2]);
}

/** The WCAG contrast ratio of two opaque colours (1 to 21). */
export function contrast(a: Rgba, b: Rgba): number {
  const x = luminance(a);
  const y = luminance(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

/**
 * Resolve a stack of token names, top first (`['--success', '--success-soft', '--bg-surface']` is success text on its soft
 * tint over a surface), into the opaque colour each layer paints. The last layer must be opaque.
 */
export function layers(tokens: Record<string, string>, names: readonly string[]): Rgba {
  const cols = names.map((n) => {
    const c = parseColor(tokens[n] ?? '');
    if (!c) throw new Error(`token ${n} is not a colour: ${tokens[n]}`);
    return c;
  });
  let acc = cols[cols.length - 1]!;
  if (acc[3] !== 1) throw new Error(`the bottom layer ${names[names.length - 1]} must be opaque`);
  for (let i = cols.length - 2; i >= 0; i--) acc = over(cols[i]!, acc);
  return acc;
}
