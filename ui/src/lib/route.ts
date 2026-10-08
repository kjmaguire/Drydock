export type Route =
  | { name: 'home' }
  | { name: 'needs-you' }
  | { name: 'room'; id: string }
  | { name: 'tasks' }
  | { name: 'task-new' }
  | { name: 'task'; id: string }
  | { name: 'pulls' }
  | { name: 'team' }
  | { name: 'activity' }
  | { name: 'project' }
  | { name: 'project-new' }
  | { name: 'connections' }
  | { name: 'providers' }
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
      return b ? { name: 'task', id: b } : { name: 'tasks' };
    case 'pulls':
      return { name: 'pulls' };
    case 'team':
      return { name: 'team' };
    case 'activity':
      return { name: 'activity' };
    case 'project':
      return b === 'new' ? { name: 'project-new' } : { name: 'project' };
    case 'connections':
      return { name: 'connections' };
    case 'providers':
      return { name: 'providers' };
    case 'settings':
      return { name: 'settings' };
    default:
      return { name: 'home' };
  }
}
