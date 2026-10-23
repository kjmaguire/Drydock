import { readdirSync, readFileSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';

// The type rules of the UI audit of 2026-10-19 (docs/ui-audit-2026-10-19.md, findings B1-B3): text sizes come from the tokens in
// app.css, four font weights are in use, and the code font has one size. A new screen that breaks one fails here.

// fileURLToPath, not .pathname: on Windows the pathname is "/D:/a/...", which is not a folder
const ROOT = fileURLToPath(new URL('..', import.meta.url));

// one recursive walk; the paths it returns are relative to ui/app with forward slashes on every OS, so the lookups below
// ('styles/app.css', the test/ prefix, SIZE_EXEMPT) hold on Windows too
function sources(): string[] {
  return readdirSync(ROOT, { recursive: true, withFileTypes: true })
    .filter((e) => e.isFile() && /\.(ts|tsx|css)$/.test(e.name) && !/\.test\./.test(e.name))
    .map((e) => relative(ROOT, join(e.parentPath, e.name)).replaceAll('\\', '/'));
}

const FILES = sources().map((path) => ({ path, text: readFileSync(join(ROOT, path), 'utf8') }));

function appCss(): string {
  // a named failure, not "cannot read properties of undefined", when the walk does not reach the stylesheet
  expect(FILES.map((f) => f.path)).toContain('styles/app.css');
  return FILES.find((f) => f.path === 'styles/app.css')!.text;
}

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
    const css = appCss();
    const block = css.match(/code,\s*kbd,\s*pre,\s*samp\s*\{[^}]*\}/);
    expect(block).not.toBeNull();
    expect(block![0]).toContain('font-size: var(--text-sm)');
  });

  it('counts things with their noun ("1 file", "2 files"), never "file(s)" (B5)', () => {
    const bad: string[] = [];
    for (const f of FILES) {
      if (f.path.startsWith('test/')) continue; // fixtures stand in for what the core sends
      f.text.split('\n').forEach((line, i) => {
        if (/^\s*(\/\/|\/\*|\*)/.test(line)) return;
        if (/\b(file|task|run|item|day|slot|model|hit|result|passage|match|backup|project|call|review|value|alternative|secret)\((s|es)\)/.test(line)) bad.push(`${f.path}:${i + 1}: ${line.trim().slice(0, 90)}`);
      });
    }
    expect(bad).toEqual([]);
  });

  it('defines the title size as a token and the scale has no other sizes', () => {
    const css = appCss();
    const sizes = [...css.matchAll(/--text-([a-z0-9]+):\s*([\d.]+)px/g)].map((m) => `${m[1]}=${m[2]}`);
    expect(sizes).toEqual(['xs=12', 'sm=12.5', 'md=13', 'lg=15', 'xl=18', '2xl=22', 'title=22']);
  });
});

describe('the shape rules of Graphite (decision P97)', () => {
  it('rounds no corner more than the largest radius token (14px): the 16-26px cards of Crew HQ are gone', () => {
    const bad: string[] = [];
    for (const f of FILES) {
      f.text.split('\n').forEach((line, i) => {
        const m = /(?:borderRadius\s*:|border-radius\s*:)\s*['"`]?([^'"`;,}]*)/.exec(line);
        if (!m) return;
        if (m[1]!.match(/\d+(?:\.\d+)?px/g)?.some((v) => parseFloat(v) > 14)) bad.push(`${f.path}:${i + 1}: ${line.trim().slice(0, 90)}`);
      });
    }
    expect(bad).toEqual([]);
  });
});
