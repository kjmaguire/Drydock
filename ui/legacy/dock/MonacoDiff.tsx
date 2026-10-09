// Monaco's read-only diff editor (docs/ui.md 6.1). Loaded on first use (React.lazy) so the first paint does not carry it.
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
import { useEffect, useRef } from 'react';
import { isDarkTheme, onThemeChange } from '../lib/effectiveTheme';

(self as unknown as { MonacoEnvironment: { getWorker(): Worker } }).MonacoEnvironment = { getWorker: () => new EditorWorker() };

/** The Monaco language id for a path, from the registered languages' extensions; plaintext when none matches. */
export function languageFor(path: string): string {
  const lower = path.toLowerCase();
  const base = lower.split('/').pop() ?? lower;
  for (const l of monaco.languages.getLanguages()) {
    if (l.filenames?.some((f) => f.toLowerCase() === base)) return l.id;
    if (l.extensions?.some((e) => lower.endsWith(e.toLowerCase()))) return l.id;
  }
  return 'plaintext';
}

export interface MonacoDiffProps {
  path: string;
  original: string;
  modified: string;
  sideBySide: boolean;
}

export default function MonacoDiff({ path, original, modified, sideBySide }: MonacoDiffProps) {
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<monaco.editor.IStandaloneDiffEditor | null>(null);

  useEffect(() => {
    if (!host.current) return;
    const e = monaco.editor.createDiffEditor(host.current, {
      readOnly: true,
      originalEditable: false,
      automaticLayout: true,
      renderSideBySide: sideBySide,
      useInlineViewWhenSpaceIsLimited: true,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      renderOverviewRuler: true,
      fontSize: 13,
      links: false,
      contextmenu: true,
      theme: isDarkTheme() ? 'vs-dark' : 'vs',
      accessibilitySupport: 'auto',
      ariaLabel: `Diff of ${path}`,
    });
    editor.current = e;
    const off = onThemeChange((dark) => monaco.editor.setTheme(dark ? 'vs-dark' : 'vs'));
    return () => {
      off();
      const m = e.getModel();
      e.dispose();
      m?.original.dispose();
      m?.modified.dispose();
      editor.current = null;
    };
    // the editor is made once per mount; content and options are applied below
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const e = editor.current;
    if (!e) return;
    const lang = languageFor(path);
    const prev = e.getModel();
    e.setModel({ original: monaco.editor.createModel(original, lang), modified: monaco.editor.createModel(modified, lang) });
    prev?.original.dispose();
    prev?.modified.dispose();
  }, [path, original, modified]);

  useEffect(() => {
    editor.current?.updateOptions({ renderSideBySide: sideBySide });
  }, [sideBySide]);

  return <div ref={host} className="monaco-host" data-testid="monaco-diff" />;
}
