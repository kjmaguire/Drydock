// The design tokens meet WCAG 2.1 AA (4.5:1) for every pair the UI paints as text, in both themes. The pairs are read
// from styles/app.css itself, so a token change that breaks contrast fails here (audit F7).
import { readFileSync } from 'node:fs';
import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { contrast, layers, parseColor, rootTokens, type Scheme } from '../lib/contrast.ts';

const css = readFileSync(new URL('./app.css', import.meta.url), 'utf8');

/** Text tokens shown on plain surfaces: body, secondary, faint, links, status words, badges, chips, diff counts. */
const TEXT = ['--text', '--text-muted', '--text-faint', '--accent-text', '--success', '--warning', '--danger', '--info'];
/** The opaque surfaces text sits on, and the translucent hover/selection tints over them. */
const SURFACES: string[][] = [
  ['--bg-app'],
  ['--bg-surface'],
  ['--bg-sidebar'],
  ['--bg-raised'],
  ['--bg-overlay'],
  ['--bg-input'],
  ['--bg-hover', '--bg-sidebar'],
  ['--bg-active', '--bg-sidebar'],
  ['--bg-hover', '--bg-app'],
  ['--bg-active', '--bg-surface'],
];
/** Tone chips, badges, banners: the tone's text on its own soft tint (ui/tokens.ts `tone`). */
const TONES: [fg: string, bg: string][] = [
  ['--success', '--success-soft'],
  ['--warning', '--warning-soft'],
  ['--danger', '--danger-soft'],
  ['--info', '--info-soft'],
  ['--accent-text', '--accent-soft'],
  ['--text-muted', '--bg-active'],
  ['--text', '--mention-bg'],
];
const UNDER = ['--bg-app', '--bg-surface', '--bg-sidebar'];
/** White-on-colour: primary buttons, the danger button on hover, plan step dots, unread pills. */
const SOLID: [fg: string, bg: string][] = [
  ['--text-on-accent', '--accent'],
  ['--text-on-accent', '--accent-hover'],
  ['--text-on-accent', '--danger-solid'],
  ['--text-on-accent', '--success-solid'],
];

function pairs(): { name: string; fg: string[]; bg: string[] }[] {
  const out: { name: string; fg: string[]; bg: string[] }[] = [];
  for (const fg of TEXT) for (const bg of SURFACES) out.push({ name: `${fg} on ${bg.join(' over ')}`, fg: [fg, ...bg], bg });
  for (const [fg, tint] of TONES) {
    for (const u of UNDER) out.push({ name: `${fg} on ${tint} over ${u}`, fg: [fg, tint, u], bg: [tint, u] });
  }
  for (const [fg, bg] of SOLID) out.push({ name: `${fg} on ${bg}`, fg: [fg, bg], bg: [bg] });
  return out;
}

function ratios(scheme: Scheme) {
  const tokens = rootTokens(css, scheme);
  return pairs().map((p) => ({ name: p.name, ratio: contrast(layers(tokens, p.fg), layers(tokens, p.bg)) }));
}

describe('contrast arithmetic', () => {
  it('matches the WCAG reference values', () => {
    expect(contrast(parseColor('#000')!, parseColor('#fff')!).toFixed(1)).toBe('21.0');
    expect(contrast(parseColor('#777777')!, parseColor('#ffffff')!).toFixed(2)).toBe('4.48');
    expect(parseColor('rgb(15 17 25 / 0.08)')).toEqual([15, 17, 25, 0.08]);
  });

  it('resolves light-dark() per scheme', () => {
    const t = rootTokens(':root {\n --a: light-dark(#111111, rgb(1 2 3 / 0.5));\n --b: #fff; }', 'dark');
    expect(t['--a']).toBe('rgb(1 2 3 / 0.5)');
    expect(t['--b']).toBe('#fff');
    expect(rootTokens(':root { --a: light-dark(#111111, #222222); }', 'light')['--a']).toBe('#111111');
  });
});

describe('focus indicator', () => {
  it('keeps an outline on :focus-visible, which forced colours paint even though they drop the shadow ring', () => {
    const base = /\n  :focus-visible \{([^}]*)\}/.exec(css)?.[1] ?? '';
    expect(base).toContain('outline: 2px solid transparent');
    expect(base).not.toContain('outline: none');
  });

  it('draws a Highlight outline in forced-colours mode from the last layer, over component styles', () => {
    const last = css.slice(css.lastIndexOf('@layer app {'));
    expect(css.lastIndexOf('@layer app {')).toBeGreaterThan(css.lastIndexOf('@layer base {'));
    expect(last).toMatch(/@media \(forced-colors: active\)\s*\{\s*:focus-visible\s*\{\s*outline: 2px solid Highlight !important/);
  });
});

for (const scheme of ['light', 'dark'] as const) {
  describe(`${scheme} theme tokens`, () => {
    it('every text pair is at least 4.5:1', () => {
      const failing = ratios(scheme)
        .filter((r) => r.ratio < 4.5)
        .map((r) => `${r.name}: ${r.ratio.toFixed(2)}`);
      expect(failing).toEqual([]);
    });
  });
}
