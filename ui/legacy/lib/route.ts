export type Route =
  | { name: 'home' }
  | { name: 'needs-you' }
  | { name: 'room'; id: string }
  | { name: 'tasks' }
  | { name: 'task-new' }
  | { name: 'task'; id: string }
  | { name: 'run'; taskId: string; runId?: string }
  | { name: 'pulls' }
  | { name: 'team' }
  | { name: 'activity' }
  | { name: 'project' }
  | { name: 'project-new' }
  | { name: 'budgets' }
  | { name: 'evals' }
  | { name: 'routing' }
  | { name: 'packs' }
  | { name: 'requirements' }
  | { name: 'milestones' }
  | { name: 'skills' }
  | { name: 'memory' }
  | { name: 'plan' }
  | { name: 'integrations' }
  | { name: 'mcp' }
  | { name: 'library' }
  | { name: 'connections' }
  | { name: 'providers' }
  | { name: 'license' }
  | { name: 'backups' }
  | { name: 'data' }
  | { name: 'support' }
  | { name: 'settings' };

/** Parse location.hash. Unknown hashes fall back to home so a stale link never shows a blank page. */
export function parseRoute(hash: string): Route {
  const parts = hash.replace(/^#\/?/, '').split('/').filter(Boolean).map(decodeURIComponent);
  const [a, b] = parts;
  switch (a) {
    case 'needs-you':
      return { name: 'needs-you' };
    case 'room':
      return b ? { name: 'room', id: b } : { name: 'home' };
    case 'tasks':
      return b === 'new' ? { name: 'task-new' } : { name: 'tasks' };
    case 'task':
      if (b && parts[2] === 'run') return { name: 'run', taskId: b, runId: parts[3] };
      return b ? { name: 'task', id: b } : { name: 'tasks' };
    case 'pulls':
      return { name: 'pulls' };
    case 'team':
      return { name: 'team' };
    case 'activity':
      return { name: 'activity' };
    case 'project':
      return b === 'new' ? { name: 'project-new' } : { name: 'project' };
    case 'budgets':
    case 'evals':
    case 'routing':
    case 'packs':
    case 'requirements':
    case 'milestones':
    case 'skills':
    case 'memory':
    case 'plan':
    case 'integrations':
    case 'mcp':
      return { name: a };
    case 'connections':
      return { name: 'connections' };
    case 'providers':
      return { name: 'providers' };
    case 'library':
    case 'license':
    case 'backups':
    case 'data':
    case 'support':
      return { name: a };
    case 'settings':
      return { name: 'settings' };
    default:
      return { name: 'home' };
  }
}
