// The two imperative widgets of the dock, behind small interfaces: Monaco's read-only diff editor and an xterm.js
// terminal. The real implementations (monaco-diff.ts, xterm-view.ts) are imported lazily, on first use, so neither lands
// in the main chunk. Tests swap the loaders for labelled TEST DOUBLES (the browser test runner cannot compile Monaco's
// worker or CSS imports); the real widgets are covered by the end-to-end suite.

export interface DiffEditorInit {
  path: string;
  original: string;
  modified: string;
  sideBySide: boolean;
}

export interface DiffEditorView {
  setContent(path: string, original: string, modified: string): void;
  setSideBySide(sideBySide: boolean): void;
  dispose(): void;
}

/** Creates a diff editor inside an EMPTY host element. */
export type DiffEditorFactory = (host: HTMLElement, init: DiffEditorInit) => DiffEditorView;

export interface TerminalView {
  readonly cols: number;
  readonly rows: number;
  write(data: string | Uint8Array): void;
  /** Keystrokes and pastes, as the shell should receive them. */
  onData(fn: (data: string) => void): void;
  /** Fit the grid to the host; throws while the host is not laid out. */
  fit(): void;
  focus(): void;
  dispose(): void;
}

/** Creates a terminal inside an EMPTY host element. */
export type TerminalFactory = (host: HTMLElement) => TerminalView;

type Loader<T> = () => Promise<T>;

const realDiff: Loader<DiffEditorFactory> = () => import('./monaco-diff.ts').then((m) => m.createMonacoDiff);
const realTerminal: Loader<TerminalFactory> = () => import('./xterm-view.ts').then((m) => m.createXterm);

let diffLoader = realDiff;
let terminalLoader = realTerminal;

export const loadDiffEditor = (): Promise<DiffEditorFactory> => diffLoader();
export const loadTerminal = (): Promise<TerminalFactory> => terminalLoader();

/** Replace the diff editor (tests: a labelled test double). Pass undefined to restore Monaco. */
export function setDiffEditorLoader(l: Loader<DiffEditorFactory> | undefined): void {
  diffLoader = l ?? realDiff;
}

/** Replace the terminal (tests: a labelled test double). Pass undefined to restore xterm.js. */
export function setTerminalLoader(l: Loader<TerminalFactory> | undefined): void {
  terminalLoader = l ?? realTerminal;
}

/**
 * The computed value of color tokens. The tokens are `light-dark(...)` pairs, which a custom property keeps unresolved,
 * so each is applied to a probe element's `color` and read back as `rgb(...)`.
 */
export function readColors<K extends string>(vars: Record<K, string>, parent: HTMLElement = document.body): Record<K, string> {
  const probe = document.createElement('span');
  probe.style.display = 'none';
  parent.appendChild(probe);
  const out = {} as Record<K, string>;
  try {
    for (const k of Object.keys(vars) as K[]) {
      probe.style.color = `var(${vars[k]})`;
      out[k] = getComputedStyle(probe).color;
    }
  } finally {
    probe.remove();
  }
  return out;
}
