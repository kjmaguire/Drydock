// Cross-region UI state: the details panel (which tab, which agent is inspected, which thread is open), the dock and the
// command palette. Components subscribe with watch(handle, ui). Nothing here comes from the core.

import { applyTheme, loadTheme, type ThemeChoice } from '../lib/theme.ts';
import { Model } from './services.tsx';

export type DetailsTab = 'agents' | 'task' | 'thread' | 'inspector';
export type DockTab = 'terminal' | 'diff' | 'logs' | 'activity';

interface DockState {
  open: boolean;
  tab: DockTab;
  height: number;
}

const DOCK_KEY = 'drydock.dock';
const DETAILS_KEY = 'drydock.details';

function readJSON<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw ? ({ ...fallback, ...JSON.parse(raw) } as T) : fallback;
  } catch {
    return fallback;
  }
}

function writeJSON(key: string, v: unknown): void {
  try {
    localStorage.setItem(key, JSON.stringify(v));
  } catch {
    /* a convenience only */
  }
}

export class UiModel extends Model {
  tab: DetailsTab = 'agents';
  inspectId: string | undefined;
  thread: { roomId: string; rootId: string } | undefined;
  detailsOpen = readJSON(DETAILS_KEY, { open: true }).open;
  paletteOpen = false;
  theme: ThemeChoice = 'system';
  #dock: Record<string, DockState> = {};
  #project = '';

  constructor(opts: { persist?: boolean } = {}) {
    super();
    if (opts.persist !== false) {
      this.theme = loadTheme();
      this.#dock = readJSON<Record<string, DockState>>(DOCK_KEY, {});
    }
  }

  setTab(t: DetailsTab): void {
    this.tab = t;
    this.changed();
  }

  openInspector(agentId: string): void {
    this.inspectId = agentId;
    this.tab = 'inspector';
    this.setDetails(true);
  }

  openThread(roomId: string, rootId: string): void {
    this.thread = { roomId, rootId };
    this.tab = 'thread';
    this.setDetails(true);
  }

  closeThread(): void {
    this.thread = undefined;
    this.tab = 'agents';
    this.changed();
  }

  setDetails(open: boolean): void {
    this.detailsOpen = open;
    writeJSON(DETAILS_KEY, { open });
    this.changed();
  }

  setPalette(open: boolean): void {
    this.paletteOpen = open;
    this.changed();
  }

  setTheme(t: ThemeChoice): void {
    this.theme = t;
    applyTheme(t);
    this.changed();
  }

  // --- dock: open state, tab and height are kept per project ---

  setProject(id: string | undefined): void {
    if ((id ?? '') === this.#project) return;
    this.#project = id ?? '';
    this.changed();
  }

  get dock(): DockState {
    return { open: false, tab: 'diff', height: 280, ...this.#dock[this.#project] };
  }

  setDock(p: Partial<DockState>, persist = true): void {
    this.#dock[this.#project] = { ...this.dock, ...p };
    if (persist) writeJSON(DOCK_KEY, this.#dock);
    this.changed();
  }

  /** Open the dock on a tab (the task view, the command palette, Ctrl+Shift+D). */
  openDock(tab: DockTab): void {
    this.setDock({ open: true, tab });
  }

  toggleDock(): void {
    this.setDock({ open: !this.dock.open });
  }
}
