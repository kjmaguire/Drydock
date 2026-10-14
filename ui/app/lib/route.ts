// Which screen a URL shows, for the shell (navigation highlight, the task in context, whether a project is needed, and which
// sidebar shows: the workspace's or the project's).

export type Route =
  | { name: 'home' }
  | { name: 'needs-you' }
  | { name: 'room'; id: string }
  | { name: 'tasks' }
  | { name: 'task-new' }
  | { name: 'task'; id: string }
  | { name: 'run'; taskId: string; runId?: string }
  | { name: 'project' }
  | { name: 'project-new' }
  | { name: 'project-integrations' }
  | { name: 'chats' }
  | { name: 'chat'; id: string }
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
        | WorkspaceRouteName;
    }
  | { name: 'not-found' };

export type WorkspaceRouteName = 'workspace' | 'library' | 'library-skills' | 'integrations' | 'connections' | 'providers' | 'routing' | 'backups' | 'data' | 'support' | 'license' | 'settings';

/** Screens about the whole workspace, not one project. They open even when no project exists yet. */
export const WORKSPACE_ROUTES: readonly WorkspaceRouteName[] = ['workspace', 'library', 'library-skills', 'integrations', 'connections', 'providers', 'routing', 'backups', 'data', 'support', 'license', 'settings'];

// screens whose path is their name (Home is /home, the skill library /library/skills: they are parsed below)
const SIMPLE = new Set<string>([
  'pulls', 'team', 'activity', 'plan', 'requirements', 'milestones', 'memory', 'skills', 'packs', 'mcp', 'budgets', 'evals', 'security',
  ...WORKSPACE_ROUTES.filter((n) => n !== 'workspace' && n !== 'library-skills'),
]);

export function isWorkspaceRoute(r: Route): boolean {
  // chats open without a project too (a chat may have none); /mcp is an alias of Integrations (decision P47)
  return (WORKSPACE_ROUTES as readonly string[]).includes(r.name) || r.name === 'chats' || r.name === 'chat' || r.name === 'mcp';
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
    case 'home':
      return parts.length === 1 ? { name: 'workspace' } : { name: 'not-found' };
    case 'library':
      if (parts.length === 1) return { name: 'library' };
      return b === 'skills' && parts.length === 2 ? { name: 'library-skills' } : { name: 'not-found' };
    case 'project':
      if (parts.length === 1) return { name: 'project' };
      if (b === 'integrations' && parts.length === 2) return { name: 'project-integrations' };
      return b === 'new' && parts.length === 2 ? { name: 'project-new' } : { name: 'not-found' };
    default:
      return SIMPLE.has(a) && parts.length === 1 ? ({ name: a } as Route) : { name: 'not-found' };
  }
}

/** Old hash links (#/task/x) from before the UI routed by path. */
export function hashToPath(hash: string): string | undefined {
  if (!hash.startsWith('#/')) return undefined;
  return hash.slice(1) || '/';
}
