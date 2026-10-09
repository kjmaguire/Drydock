// Monaco's read-only diff editor (docs/ui.md 6.1). Imported lazily by widgets.ts, so the first paint does not carry it.
// Only the pieces a read-only diff needs are imported: the editor API, the diff editor, find, folding, copy and the
// language definitions (each language's tokenizer loads when a file of that kind is shown). Links are deliberately not
// imported: file content is untrusted and must not turn into something to click.
import * as monaco from 'monaco-editor/editor/editor.api.js';
import 'monaco-editor/features/codicon/register.js';
import 'monaco-editor/features/diffEditor/register.js';
import 'monaco-editor/features/clipboard/register.js';
import 'monaco-editor/features/contextmenu/register.js';
import 'monaco-editor/features/folding/register.js';
import 'monaco-editor/features/find/register.js';
import 'monaco-editor/languages/definitions/register.all.js';
import EditorWorker from 'monaco-editor/editor/editor.worker.js?worker';
import { isDarkTheme, onThemeChange } from '../../lib/effectiveTheme.ts';
import { toHex } from './dock-format.ts';
import { readColors, type DiffEditorFactory } from './widgets.ts';

(self as unknown as { MonacoEnvironment: { getWorker(): Worker } }).MonacoEnvironment = { getWorker: () => new EditorWorker() };

/** The Monaco language id for a path, from the registered languages' file names and extensions; plaintext otherwise. */
export function languageFor(path: string): string {
  const lower = path.toLowerCase();
  const base = lower.split('/').pop() ?? lower;
  for (const l of monaco.languages.getLanguages()) {
    if (l.filenames?.some((f) => f.toLowerCase() === base)) return l.id;
    if (l.extensions?.some((e) => lower.endsWith(e.toLowerCase()))) return l.id;
  }
  return 'plaintext';
}

/** Defines a theme on Monaco's own (vs, vs-dark) whose surfaces are Drydock's tokens, and applies it. */
function applyTheme(): void {
  const dark = isDarkTheme();
  const c = readColors({ bg: '--bg-app', fg: '--text', gutter: '--bg-app', line: '--bg-hover', faint: '--text-faint', sel: '--accent-soft', border: '--border' });
  const colors: Record<string, string> = {};
  const set = (key: string, v: string) => {
    const h = toHex(v);
    if (h) colors[key] = h;
  };
  set('editor.background', c.bg);
  set('editor.foreground', c.fg);
  set('editorGutter.background', c.gutter);
  set('editor.lineHighlightBackground', c.line);
  set('editorLineNumber.foreground', c.faint);
  set('editor.selectionBackground', c.sel);
  set('diffEditor.border', c.border);
  monaco.editor.defineTheme('drydock', { base: dark ? 'vs-dark' : 'vs', inherit: true, rules: [], colors });
  monaco.editor.setTheme('drydock');
}

export const createMonacoDiff: DiffEditorFactory = (host, init) => {
  applyTheme();
  const editor = monaco.editor.createDiffEditor(host, {
    readOnly: true,
    originalEditable: false,
    automaticLayout: true,
    renderSideBySide: init.sideBySide,
    useInlineViewWhenSpaceIsLimited: true,
    minimap: { enabled: false },
    scrollBeyondLastLine: false,
    renderOverviewRuler: true,
    fontSize: 13,
    fontFamily: getComputedStyle(document.documentElement).getPropertyValue('--font-mono').trim() || undefined,
    links: false,
    contextmenu: true,
    theme: 'drydock',
    accessibilitySupport: 'auto',
    ariaLabel: `Diff of ${init.path}`,
  });
  const setContent = (path: string, original: string, modified: string) => {
    const lang = languageFor(path);
    const prev = editor.getModel();
    editor.setModel({ original: monaco.editor.createModel(original, lang), modified: monaco.editor.createModel(modified, lang) });
    prev?.original.dispose();
    prev?.modified.dispose();
  };
  setContent(init.path, init.original, init.modified);
  const offTheme = onThemeChange(() => applyTheme());
  return {
    setContent,
    setSideBySide: (on) => editor.updateOptions({ renderSideBySide: on }),
    dispose: () => {
      offTheme();
      const m = editor.getModel();
      editor.dispose();
      m?.original.dispose();
      m?.modified.dispose();
    },
  };
};
