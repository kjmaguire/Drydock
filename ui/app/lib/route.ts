// Which screen a URL shows, for the shell (navigation highlight, the task in context, whether a project is needed, and which
// top bar and hub show: the workspace's or the project's).

export type Route =
  | { name: 'home' }
  | { name: 'room'; id: string }
  | { name: 'rooms' }
  | { name: 'knowledge' }
  | { name: 'tasks' }
  | { name: 'task-new' }
  | { name: 'task'; id: string }
  | { name: 'run'; taskId: string; runId?: string }
  | { name: 'project' }
  | { name: 'project-new' }
  | { name: 'project-integrations' }
  | { name: 'agent'; id: string }
  | { name: 'chats' }
  | { name: 'chat'; id: string }
  | { name: 'guide'; topic?: string }
  | {
      name:
        | 'pulls'
        | 'team'
        | 'activity'
        | 'plan'
        | 'requirements'
        | 'milestones'
        | 'memory'
        | 'skills'
        | 'packs'
        | 'mcp'
        | 'budgets'
        | 'evals'
        | 'security'
        | 'repo-map'
        | WorkspaceRouteName;
      /** The item a list-and-detail page opens (/integrations/linear, /agents/claude, /models/this-pc). */
      key?: string;
    }
  | { name: 'not-found' };

export type WorkspaceRouteName =
  | 'workspace'
  | 'needs-you'
  | 'my-tasks'
  | 'crew'
  | 'library'
  | 'library-starters'
  | 'library-skills'
  | 'integrations'
  | 'agents'
  | 'connections'
  | 'github'
  | 'security-scan'
  | 'providers'
  | 'models'
  | 'routing'
  | 'backups'
  | 'data'
  | 'support'
  | 'license'
  | 'settings'
  | 'settings-shortcuts'
  | 'settings-about';

/** Screens about the whole workspace, not one project. They open even when no project exists yet. */
export const WORKSPACE_ROUTES: readonly WorkspaceRouteName[] = [
  'workspace',
  // the Inbox and the person's own tasks list every project's (decision P97)
  'needs-you',
  'my-tasks',
  'crew',
  'library',
  'library-starters',
  'library-skills',
  'integrations',
  'agents',
  'connections',
  'github',
  'security-scan',
  'providers',
  // Models on this PC: a workspace page of its own, no project open in the sidebar (decision P97)
  'models',
  'routing',
  'backups',
  'data',
  'support',
  'license',
  'settings',
  'settings-shortcuts',
  'settings-about',
];

// screens whose path is their name (Home is /home, the skill library /library/skills: they are parsed below)
const SIMPLE = new Set<string>([
  'pulls', 'activity', 'plan', 'requirements', 'milestones', 'memory', 'skills', 'packs', 'mcp', 'budgets', 'evals', 'security', 'repo-map', 'rooms', 'knowledge',
  ...WORKSPACE_ROUTES.filter((n) => !['workspace', 'library-starters', 'library-skills', 'settings-shortcuts', 'settings-about', 'needs-you'].includes(n)),
]);

export function isWorkspaceRoute(r: Route): boolean {
  // chats open without a project too (a chat may have none); /mcp is an alias of Integrations (decision P47)
  return (WORKSPACE_ROUTES as readonly string[]).includes(r.name) || r.name === 'chats' || r.name === 'chat' || r.name === 'mcp' || r.name === 'guide';
}

/**
 * Whether the page belongs to the workspace rather than to one project: the shell then shows the workspace sidebar (Home,
 * the library, connections, the app) instead of the project's. Creating a project is a workspace page too.
 */
export function isWorkspacePage(r: Route): boolean {
  return isWorkspaceRoute(r) || r.name === 'project-new';
}

/** Parse a pathname. Unknown paths are 'not-found'. */
export function parseRoute(pathname: string): Route {
  const parts = pathname.split('/').filter(Boolean).map((p) => {
    try {
      return decodeURIComponent(p);
    } catch {
      return p;
    }
  });
  const [a, b, c, d] = parts;
  if (!a) return { name: 'home' };
  switch (a) {
    case 'needs-you':
      return parts.length === 1 ? { name: 'needs-you' } : { name: 'not-found' };
    case 'room':
      return b && parts.length === 2 ? { name: 'room', id: b } : { name: 'not-found' };
    case 'tasks':
      if (parts.length === 1) return { name: 'tasks' };
      return b === 'new' && parts.length === 2 ? { name: 'task-new' } : { name: 'not-found' };
    case 'task':
      if (b && c === 'run' && parts.length <= 4) return d ? { name: 'run', taskId: b, runId: d } : { name: 'run', taskId: b };
      return b && parts.length === 2 ? { name: 'task', id: b } : { name: 'not-found' };
    case 'chats':
      if (parts.length === 1) return { name: 'chats' };
      return b && parts.length === 2 ? { name: 'chat', id: b } : { name: 'not-found' };
    case 'guide':
      if (parts.length === 1) return { name: 'guide' };
      return b && parts.length === 2 ? { name: 'guide', topic: b } : { name: 'not-found' };
    case 'home':
      return parts.length === 1 ? { name: 'workspace' } : { name: 'not-found' };
    // the list-and-detail pages (decision P98): the page, or one of its items
    case 'agents':
    case 'integrations':
    case 'models':
      if (parts.length === 1) return { name: a };
      return b && parts.length === 2 ? { name: a, key: b } : { name: 'not-found' };
    case 'library':
      if (parts.length === 1) return { name: 'library' };
      if (parts.length !== 2) return { name: 'not-found' };
      return b === 'skills' ? { name: 'library-skills' } : b === 'starters' ? { name: 'library-starters' } : { name: 'not-found' };
    case 'settings':
      if (parts.length === 1) return { name: 'settings' };
      if (parts.length !== 2) return { name: 'not-found' };
      return b === 'shortcuts' ? { name: 'settings-shortcuts' } : b === 'about' ? { name: 'settings-about' } : { name: 'not-found' };
    case 'team':
      if (parts.length === 1) return { name: 'team' };
      return b && parts.length === 2 ? { name: 'agent', id: b } : { name: 'not-found' };
    case 'project':
      if (parts.length === 1) return { name: 'project' };
      if (b === 'integrations' && parts.length === 2) return { name: 'project-integrations' };
      return b === 'new' && parts.length === 2 ? { name: 'project-new' } : { name: 'not-found' };
    default:
      return SIMPLE.has(a) && parts.length === 1 ? ({ name: a } as Route) : { name: 'not-found' };
  }
}

/**
 * Old hash links (#/task/x) from before the UI routed by path: the path they name, or undefined for a hash that is not one
 * or that could name another place. "#//host/x" and "#/\host" are cross-origin URLs once they are a path (history.replaceState
 * throws a SecurityError for them), and a control character has no place in a link. A bad percent escape ("%E0%A4%A") is not
 * a problem: the path is never decoded here, and parseRoute keeps it as written.
 */
export function hashToPath(hash: string): string | undefined {
  if (!hash.startsWith('#/')) return undefined;
  const path = hash.slice(1);
  // eslint-disable-next-line no-control-regex
  if (path.startsWith('//') || path.includes('\\') || /[\u0000-\u001f\u007f]/.test(path)) return undefined;
  try {
    if (new URL(path, 'http://drydock.invalid').origin !== 'http://drydock.invalid') return undefined;
  } catch {
    return undefined;
  }
  return path;
}

/**
 * Opens the screen an old hash link names, keeping the query (?token= for the dev transport). Never throws: a link it can not
 * use is left alone, and so is a browser that refuses the new address. Returns whether it moved.
 */
export function redirectLegacyHash(loc: Pick<Location, 'hash' | 'search'>, history: Pick<History, 'replaceState'>): boolean {
  const legacy = hashToPath(loc.hash);
  if (!legacy) return false;
  try {
    history.replaceState(null, '', legacy + loc.search);
    return true;
  } catch (err) {
    console.warn('An old link could not be opened; showing the start page instead.', err);
    return false;
  }
}

/**
 * The hub a screen belongs to: one of a project's three tabs (decision P141: Work, Crew, Settings), or a workspace place (P140:
 * Home, Inbox, Chats, the Agents library, Settings). The project's tab bar marks the hub's tab current; the project's Settings
 * tab and the workspace's Settings show their sections in a rail, and a section of more than one page shows them as pills.
 */
export type Hub =
  | 'work'
  | 'crew'
  | 'project-settings'
  | 'settings'
  | 'home'
  | 'inbox'
  | 'chats'
  | 'library'
  | 'project-new'
  | 'none';

const HUB_OF: Record<Route['name'], Hub> = {
  // Work (decision P141): the project's tasks and chats in one list beside the one open; the board, a task's own page, the pull
  // requests and the rooms are reached from it and keep their addresses
  home: 'work',
  tasks: 'work',
  'task-new': 'work',
  task: 'work',
  run: 'work',
  pulls: 'work',
  room: 'work',
  rooms: 'work',
  // the workspace's Inbox; My tasks is its Mine tab (decision P140)
  'needs-you': 'inbox',
  'my-tasks': 'inbox',
  team: 'crew',
  agent: 'crew',
  // the project's Settings tab (decision P141): its own settings and integrations, and the pages that left the project's tabs, as
  // sections (shell/project-sections.ts)
  project: 'project-settings',
  'project-integrations': 'project-settings',
  activity: 'project-settings',
  budgets: 'project-settings',
  evals: 'project-settings',
  security: 'project-settings',
  plan: 'project-settings',
  milestones: 'project-settings',
  knowledge: 'project-settings',
  requirements: 'project-settings',
  packs: 'project-settings',
  skills: 'project-settings',
  memory: 'project-settings',
  'repo-map': 'project-settings',
  // one Settings (decision P140): every page that configures the workspace or the app is a section of it, under its old address
  // (shell/settings-sections.ts); the old aliases belong where they lead
  agents: 'settings',
  connections: 'settings',
  integrations: 'settings',
  mcp: 'settings',
  github: 'settings',
  models: 'settings',
  providers: 'settings',
  routing: 'settings',
  'security-scan': 'settings',
  license: 'settings',
  backups: 'settings',
  data: 'settings',
  support: 'settings',
  settings: 'settings',
  'settings-shortcuts': 'settings',
  'settings-about': 'settings',
  workspace: 'home',
  chats: 'chats',
  chat: 'chats',
  // one Agents library (decision P140): My agents, Starters, Skills, and every project's crew
  library: 'library',
  'library-starters': 'library',
  'library-skills': 'library',
  crew: 'library',
  'project-new': 'project-new',
  // the Guide is the help surface of its own (Settings > Help & about links it), not a page inside the Settings frame
  guide: 'none',
  'not-found': 'none',
};

export function hubOf(r: Route): Hub {
  return HUB_OF[r.name];
}

/** The project's three tabs (decision P141). */
export function isProjectHub(h: Hub): boolean {
  return h === 'work' || h === 'crew' || h === 'project-settings';
}

/** Hubs that are the workspace's own (no project needed, nothing of a project shown). */
export function isWorkspaceHub(h: Hub): boolean {
  return h === 'home' || h === 'inbox' || h === 'chats' || h === 'library' || h === 'settings' || h === 'project-new';
}

/** The item a project's Work view shows in its centre (decision P141): a task or a chat, named in the URL as ?item=task:<id>. */
export interface WorkItemRef {
  kind: 'task' | 'chat';
  id: string;
}

/** The ?item= value of Work ("task:tsk_1", "chat:chat_2"), or undefined for none or a value that names neither. */
export function parseWorkItem(v: string | null | undefined): WorkItemRef | undefined {
  const m = /^(task|chat):(.+)$/.exec(v ?? '');
  return m ? { kind: m[1] as WorkItemRef['kind'], id: m[2]! } : undefined;
}

/** The value of ?item= for an item ("task:tsk_1"). */
export const workItemKey = (r: WorkItemRef): string => `${r.kind}:${r.id}`;

/** Work with one item open: "/?item=task%3Atsk_1". Reload and Back keep it. */
export function workItemHref(r: WorkItemRef): string {
  return `/?item=${encodeURIComponent(workItemKey(r))}`;
}
