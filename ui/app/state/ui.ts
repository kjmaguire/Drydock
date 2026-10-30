// Cross-region UI state: the details panel (which tab, which agent is inspected, which thread is open), the dock and the
// command palette. Components subscribe with watch(handle, ui). Nothing here comes from the core but the key bindings, which it
// keeps and setKeys applies (decision P132).

import { useBindings } from '../lib/keys.ts';
import { applyTheme, loadTheme, type ThemeChoice } from '../lib/theme.ts';
import type { SkillPick } from '../api/extTypes.ts';
import { isRecord } from '../api/guards.ts';
import { Model } from './services.tsx';

export type DetailsTab = 'agents' | 'room' | 'task' | 'thread' | 'inspector';
export type DockTab = 'terminal' | 'diff' | 'logs' | 'activity';
/**
 * The kind of page the details panel sits beside: a room or a run ('conversation'), or any other page (the task page has its own
 * properties panel, graphite P97, so the details panel starts closed beside it).
 */
export type DetailsPage = 'conversation' | 'other';

interface DockState {
  open: boolean;
  tab: DockTab;
  height: number;
}

const DOCK_KEY = 'drydock.dock';
const DETAILS_KEY = 'drydock.details';
const SIDEBAR_KEY = 'drydock.sidebar';

const DOCK_TABS: readonly DockTab[] = ['terminal', 'diff', 'logs', 'activity'];
const DOCK_DEFAULT: DockState = { open: false, tab: 'diff', height: 280 };
const DOCK_MIN_HEIGHT = 120;
const DOCK_MAX_HEIGHT = 2400;
const DETAILS_DEFAULT = { open: true, other: false };

/** Below this width the details panel floats over the page instead of taking its width, and starts closed. */
export const NARROW_DETAILS = '(max-width: 1199px)';
/** Below this width the Rooms and Settings rails and the chat list are drawers a button opens. */
export const NARROW_RAILS = '(max-width: 899px)';

/**
 * Below this width the sidebar starts folded to its rail (graphite, P97); a choice the person made stays. 1200, not 1024 (the UI
 * audit of 2026-10-26): at 1024 the open sidebar left a list-and-detail page (Models, Agents, Integrations) a detail of 390 px.
 */
export const NARROW_SIDEBAR = '(max-width: 1199px)';

/** The sidebar's stored choice: 'rail' or 'full', or undefined when the person has not chosen (the window's width decides). */
export function parseSidebar(raw: unknown): 'rail' | 'full' | undefined {
  return raw === 'rail' || raw === 'full' ? raw : undefined;
}

/** The window's answer to a media query (a test passes its own, so a test's layout does not depend on its runner's window). */
export type MediaMatch = (query: string) => Pick<MediaQueryList, 'matches' | 'addEventListener'> | undefined;

/** A window wider than every breakpoint (tests that do not test a narrow window). */
export const wideWindow: MediaMatch = () => ({ matches: false, addEventListener: () => {} });

const media: MediaMatch = (q) => {
  try {
    return typeof matchMedia === 'function' ? matchMedia(q) : undefined;
  } catch {
    return undefined;
  }
};

/**
 * The dock states of the projects as stored in localStorage (audit 2026-10-16, L43). The stored text is whatever an older
 * version, another tool or a person left there: each field is checked, a bad one is the default, and an entry that is not an
 * object is left out. The keys are project ids, so the result has no prototype to collide with them.
 */
export function parseDock(raw: unknown): Record<string, DockState> {
  const out: Record<string, DockState> = Object.create(null);
  if (!isRecord(raw)) return out;
  for (const [id, v] of Object.entries(raw)) {
    if (!isRecord(v)) continue;
    out[id] = {
      open: typeof v.open === 'boolean' ? v.open : DOCK_DEFAULT.open,
      tab: DOCK_TABS.includes(v.tab as DockTab) ? (v.tab as DockTab) : DOCK_DEFAULT.tab,
      height: typeof v.height === 'number' && Number.isFinite(v.height) ? Math.min(DOCK_MAX_HEIGHT, Math.max(DOCK_MIN_HEIGHT, Math.round(v.height))) : DOCK_DEFAULT.height,
    };
  }
  return out;
}

/** Whether the details panel is open, per kind of page, as stored; a field that is not a boolean is the default. */
export function parseDetails(raw: unknown): { open: boolean; other: boolean } {
  const v = isRecord(raw) ? raw : {};
  return { open: typeof v.open === 'boolean' ? v.open : DETAILS_DEFAULT.open, other: typeof v.other === 'boolean' ? v.other : DETAILS_DEFAULT.other };
}

function readStored<T>(key: string, parse: (raw: unknown) => T): T {
  let raw: unknown;
  try {
    const text = localStorage.getItem(key);
    raw = text ? JSON.parse(text) : undefined;
  } catch {
    raw = undefined; // unreadable storage, or text that is not JSON: the defaults
  }
  return parse(raw);
}

function writeJSON(key: string, v: unknown): void {
  try {
    localStorage.setItem(key, JSON.stringify(v));
  } catch {
    /* a convenience only */
  }
}

/** The Inbox's tabs. */
export type InboxTab = 'needs' | 'running' | 'done';

/** A one-time note one page leaves for the next. */
export interface Flash {
  text: string;
  tone: 'ok' | 'warn';
}

export class UiModel extends Model {
  tab: DetailsTab = 'agents';
  inspectId: string | undefined;
  thread: { roomId: string; rootId: string } | undefined;
  // open or closed is remembered per kind of page: on a room, a task or a run the panel has the context of the page (the
  // task, the thread, who is talking) and starts open; elsewhere it only repeats the team, so it starts as the slim strip
  // and leaves the page its width (owner, 2026-10-13: pages read cramped at 1366-1536 px)
  #open = readStored(DETAILS_KEY, parseDetails);
  #page: DetailsPage = 'conversation';
  // in a narrow window (a Windows half-screen beside an editor) the panel would take a third of the width: it starts closed
  // and, opened, floats over the page; that choice lasts for the session and is not stored over the wide window's
  #narrowOpen = false;
  #narrowDetails: ReturnType<MediaMatch>;
  #narrowRails: ReturnType<MediaMatch>;
  #narrowSidebar: ReturnType<MediaMatch>;
  #sidebar: 'rail' | 'full' | undefined;
  /** A rail or the chat list shown as a drawer in a narrow window (closed again by a navigation). */
  drawerOpen = false;
  paletteOpen = false;
  /** The Inbox's tab (Needs you, Running, Done today): the sidebar's Inbox or Running is current with it. */
  inboxTab: InboxTab = 'needs';
  theme: ThemeChoice = 'system';
  #newChat = false;
  #dock: Record<string, DockState> = {};
  #project = '';

  constructor(opts: { persist?: boolean; media?: MediaMatch } = {}) {
    super();
    const m = opts.media ?? media;
    this.#narrowDetails = m(NARROW_DETAILS);
    this.#narrowRails = m(NARROW_RAILS);
    this.#narrowSidebar = m(NARROW_SIDEBAR);
    this.#narrowSidebar?.addEventListener('change', () => this.changed());
    // crossing a width redraws the shell (a drawer left open closes when the window widens past it)
    const redraw = () => {
      if (!this.narrowRails) this.drawerOpen = false;
      this.changed();
    };
    this.#narrowDetails?.addEventListener('change', redraw);
    this.#narrowRails?.addEventListener('change', redraw);
    if (opts.persist !== false) {
      this.theme = loadTheme();
      this.#sidebar = readStored(SIDEBAR_KEY, parseSidebar);
      this.#dock = readStored(DOCK_KEY, parseDock);
    }
  }

  /** Whether the sidebar is folded to its rail: the person's choice, else a narrow window folds it. */
  get sidebarRail(): boolean {
    return this.#sidebar ? this.#sidebar === 'rail' : !!this.#narrowSidebar?.matches;
  }

  /** Fold the sidebar to its rail or open it again; the choice is remembered. */
  toggleSidebar(): void {
    this.#sidebar = this.sidebarRail ? 'full' : 'rail';
    writeJSON(SIDEBAR_KEY, this.#sidebar);
    this.changed();
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

  /** The room's members and mode (the Room tab, UI review P1-5). */
  openRoomPanel(): void {
    this.tab = 'room';
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

  /** Whether the details panel is open on the kind of page showing now. */
  get detailsOpen(): boolean {
    if (this.narrowDetails) return this.#narrowOpen;
    return this.#page === 'conversation' ? this.#open.open : this.#open.other;
  }

  /** The window is narrow enough that the details panel floats over the page. */
  get narrowDetails(): boolean {
    return !!this.#narrowDetails?.matches;
  }

  /** The window is narrow enough that the rails and the chat list are drawers. */
  get narrowRails(): boolean {
    return !!this.#narrowRails?.matches;
  }

  /** Open or close the drawer (a rail or the chat list) of a narrow window. */
  setDrawer(open: boolean): void {
    if (this.drawerOpen === open) return;
    this.drawerOpen = open;
    this.changed();
  }

  /** The panel says which kind of page it sits beside (no change event: it is read while rendering). */
  detailsPage(page: DetailsPage): void {
    this.#page = page;
  }

  /** Open or close the details panel for the kind of page showing now (the other kind keeps its own choice). */
  setDetails(open: boolean): void {
    if (this.narrowDetails) {
      this.#narrowOpen = open;
      this.changed();
      return;
    }
    if (this.#page === 'conversation') this.#open.open = open;
    else this.#open.other = open;
    writeJSON(DETAILS_KEY, this.#open);
    this.changed();
  }

  setInboxTab(tab: InboxTab): void {
    if (this.inboxTab === tab) return;
    this.inboxTab = tab;
    this.changed();
  }

  setPalette(open: boolean): void {
    this.paletteOpen = open;
    this.changed();
  }

  /** The palette's New chat: the Chats screen takes it (once) and opens its dialog. */
  requestNewChat(): void {
    this.#newChat = true;
    this.changed();
  }

  /** Whether New chat was asked for since the last call; clears it (no change event: read while rendering). */
  takeNewChat(): boolean {
    const asked = this.#newChat;
    this.#newChat = false;
    return asked;
  }

  #flash: Flash | undefined;

  /** A one-time note for the next page (the workspace Home says which project was deleted). */
  flash(text: string, tone: Flash['tone'] = 'ok'): void {
    this.#flash = { text, tone };
  }

  /** The note left for this page, once; clears it (no change event: read when the page mounts). */
  takeFlash(): Flash | undefined {
    const f = this.#flash;
    this.#flash = undefined;
    return f;
  }

  #picks: { projectId: string; picks: SkillPick[] } | undefined;

  /** Skills picked in the workspace Library for a project: its Skills page takes them and offers to add them or ask for advice. */
  handSkillPicks(projectId: string, picks: SkillPick[]): void {
    this.#picks = { projectId, picks };
  }

  /** The picks handed to this project, once (another project's picks stay for it). */
  takeSkillPicks(projectId: string): SkillPick[] {
    if (this.#picks?.projectId !== projectId) return [];
    const p = this.#picks.picks;
    this.#picks = undefined;
    return p;
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
    return { ...DOCK_DEFAULT, ...this.#dock[this.#project] };
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

  #terminalAsk: { taskId?: string; projectId?: string } | undefined;

  /**
   * Open a new terminal in the dock, in a task's worktree or the project's repository (the task page's "Open the worktree"):
   * the dock's terminal panel opens it as its own "New terminal" button does (takeTerminalAsk).
   */
  openTerminal(where: { taskId?: string; projectId?: string }): void {
    this.#terminalAsk = { ...where };
    this.openDock('terminal');
  }

  /** The terminal asked for by openTerminal, once: the terminal panel takes it. */
  takeTerminalAsk(): { taskId?: string; projectId?: string } | undefined {
    const w = this.#terminalAsk;
    this.#terminalAsk = undefined;
    return w;
  }

  toggleDock(): void {
    this.setDock({ open: !this.dock.open });
  }

  /** Ctrl+` (decision P132): the dock opens on its terminal, or closes when it shows the terminal already. */
  toggleTerminal(): void {
    const d = this.dock;
    this.setDock(d.open && d.tab === 'terminal' ? { open: false } : { open: true, tab: 'terminal' });
  }

  /** The person's key bindings (decision P132), applied to lib/keys.ts; the change redraws what shows a shortcut's keys. */
  setKeys(b: Partial<Record<string, string>> | null | undefined): void {
    useBindings(b);
    this.changed();
  }
}
