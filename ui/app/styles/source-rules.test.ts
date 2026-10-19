import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';

// The type rules of the UI audit of 2026-10-19 (docs/ui-audit-2026-10-19.md, findings B1-B3): text sizes come from the tokens in
// app.css, four font weights are in use, and the code font has one size. A new screen that breaks one fails here.

const ROOT = new URL('..', import.meta.url).pathname;

function sources(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) sources(p, out);
    else if (/\.(ts|tsx|css)$/.test(name) && !/\.test\./.test(name)) out.push(p);
  }
  return out;
}

const FILES = sources(ROOT).map((p) => ({ path: relative(ROOT, p), text: readFileSync(p, 'utf8') }));

// text whose size is computed from a pixel box (avatar initials), handed to an editor widget, or drawn by the start-up failure
// page with inline styles because the stylesheet may be what failed: none of them is a page's type scale
const SIZE_EXEMPT = new Set(['ui/data.tsx', 'shell/project-switcher.tsx', 'screens/dock/xterm-view.ts', 'screens/dock/monaco-diff.ts', 'lib/failure.ts']);

describe('the type rules the UI audit set (B1-B3)', () => {
  it('sets every font size from a token, not a literal', () => {
    const bad: string[] = [];
    for (const f of FILES) {
      if (SIZE_EXEMPT.has(f.path)) continue;
      f.text.split('\n').forEach((line, i) => {
        if (/fontSize\s*:\s*['"`]?\d/.test(line) || /font-size\s*:\s*\d/.test(line)) bad.push(`${f.path}:${i + 1}: ${line.trim().slice(0, 90)}`);
      });
    }
    expect(bad).toEqual([]);
  });

  it('uses the four font weights 400, 500, 600 and 700', () => {
    const bad: string[] = [];
    for (const f of FILES) {
      f.text.split('\n').forEach((line, i) => {
        for (const m of line.matchAll(/font-?[wW]eight\s*:\s*['"]?(\d+)/g)) {
          if (!['400', '500', '600', '700'].includes(m[1]!)) bad.push(`${f.path}:${i + 1}: weight ${m[1]}`);
        }
      });
    }
    expect(bad).toEqual([]);
  });

  it('gives code, kbd, pre and samp one size, the small text token', () => {
    const css = FILES.find((f) => f.path === 'styles/app.css')!.text;
    const block = css.match(/code,\s*kbd,\s*pre,\s*samp\s*\{[^}]*\}/);
    expect(block).not.toBeNull();
    expect(block![0]).toContain('font-size: var(--text-sm)');
  });

  it('defines the title size as a token and the scale has no other sizes', () => {
    const css = FILES.find((f) => f.path === 'styles/app.css')!.text;
    const sizes = [...css.matchAll(/--text-([a-z0-9]+):\s*(\d+)px/g)].map((m) => `${m[1]}=${m[2]}`);
    expect(sizes).toEqual(['xs=12', 'sm=13', 'md=14', 'lg=16', 'xl=20', '2xl=24', 'title=28']);
  });
});
