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
    }
  | { name: 'not-found' };

export type WorkspaceRouteName =
  | 'workspace'
  | 'needs-you'
  | 'my-tasks'
  | 'library'
  | 'library-skills'
  | 'integrations'
  | 'connections'
  | 'github'
  | 'security-scan'
  | 'providers'
  | 'routing'
  | 'backups'
  | 'data'
  | 'support'
  | 'license'
  | 'settings';

/** Screens about the whole workspace, not one project. They open even when no project exists yet. */
export const WORKSPACE_ROUTES: readonly WorkspaceRouteName[] = [
  'workspace',
  // the Inbox and the person's own tasks list every project's (decision P97)
  'needs-you',
  'my-tasks',
  'library',
  'library-skills',
  'integrations',
  'connections',
  'github',
  'security-scan',
  'providers',
  'routing',
  'backups',
  'data',
  'support',
  'license',
  'settings',
];

// screens whose path is their name (Home is /home, the skill library /library/skills: they are parsed below)
const SIMPLE = new Set<string>([
  'pulls', 'activity', 'plan', 'requirements', 'milestones', 'memory', 'skills', 'packs', 'mcp', 'budgets', 'evals', 'security', 'repo-map', 'rooms', 'knowledge',
  ...WORKSPACE_ROUTES.filter((n) => n !== 'workspace' && n !== 'library-skills' && n !== 'needs-you'),
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
    case 'library':
      if (parts.length === 1) return { name: 'library' };
      return b === 'skills' && parts.length === 2 ? { name: 'library-skills' } : { name: 'not-found' };
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
 * The hub a screen belongs to (Crew HQ, decision P64): one of the five tabs of a project, the gear (settings), or a
 * workspace tab. The top bar marks the hub's tab current and a hub with sub-pages shows them as a row of pills. The gear
 * holds the workspace's and the app's pages only; Project settings and the project's Integrations are pills of its Overview.
 */
export type Hub = 'overview' | 'tasks' | 'rooms' | 'crew' | 'knowledge' | 'settings' | 'home' | 'inbox' | 'chats' | 'library' | 'scan' | 'project-new' | 'none';

const HUB_OF: Record<Route['name'], Hub> = {
  home: 'overview',
  // the workspace's Inbox and the person's own tasks: every project's, no pills
  'needs-you': 'inbox',
  'my-tasks': 'inbox',
  activity: 'overview',
  budgets: 'overview',
  evals: 'overview',
  security: 'overview',
  tasks: 'tasks',
  'task-new': 'tasks',
  task: 'tasks',
  run: 'tasks',
  pulls: 'tasks',
  plan: 'tasks',
  milestones: 'tasks',
  room: 'rooms',
  rooms: 'rooms',
  team: 'crew',
  agent: 'crew',
  knowledge: 'knowledge',
  requirements: 'knowledge',
  packs: 'knowledge',
  skills: 'knowledge',
  memory: 'knowledge',
  'repo-map': 'knowledge',
  // a project's settings and integrations are the project's own (its Overview hub), not the app's
  project: 'overview',
  'project-integrations': 'overview',
  integrations: 'settings',
  mcp: 'settings',
  connections: 'settings',
  github: 'settings',
  providers: 'settings',
  routing: 'settings',
  license: 'settings',
  backups: 'settings',
  data: 'settings',
  support: 'settings',
  settings: 'settings',
  workspace: 'home',
  chats: 'chats',
  chat: 'chats',
  library: 'library',
  'library-skills': 'library',
  'security-scan': 'scan',
  'project-new': 'project-new',
  // the Guide is the "?" button's own help surface, not a page inside the Settings rail (UI review P1-8)
  guide: 'none',
  'not-found': 'none',
};

export function hubOf(r: Route): Hub {
  return HUB_OF[r.name];
}

/** Hubs that are the workspace's own (no project needed, nothing of a project shown). */
export function isWorkspaceHub(h: Hub): boolean {
  return h === 'home' || h === 'inbox' || h === 'chats' || h === 'library' || h === 'scan' || h === 'project-new';
}
