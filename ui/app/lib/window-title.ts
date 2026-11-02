// The window's title (the taskbar, Alt-Tab): "(N) <page> · <project> · Drydock". Only the page's fixed name and the project's
// name go in it, never a task's, a chat's or an agent's title (those are text an agent or a person wrote, and the title is shown
// outside the app, in the taskbar and on a shared screen).

import { isWorkspacePage, type Route } from './route.ts';

const PAGE_NAMES: Record<Route['name'], string> = {
  home: 'Work',
  room: 'Room',
  rooms: 'Rooms',
  knowledge: 'Knowledge',
  tasks: 'Tasks',
  'task-new': 'New task',
  task: 'Task',
  run: 'Run',
  project: 'Project settings',
  'project-new': 'New project',
  'project-integrations': 'Integrations',
  agent: 'Agent',
  chats: 'Chats',
  chat: 'Chat',
  guide: 'Guide',
  pulls: 'Pull requests',
  team: 'Crew',
  activity: 'Activity',
  plan: 'Plan',
  requirements: 'Requirements',
  milestones: 'Milestones',
  memory: 'Memory',
  skills: 'Skills',
  packs: 'Expert packs',
  mcp: 'Integrations',
  budgets: 'Budgets',
  evals: 'Evals',
  security: 'Security',
  'repo-map': 'Repo map',
  workspace: 'Home',
  'needs-you': 'Inbox',
  'my-tasks': 'Inbox',
  crew: 'Crew',
  library: 'Agents',
  'library-starters': 'Agents',
  'library-skills': 'Skills',
  integrations: 'Integrations',
  agents: 'Agent CLIs',
  connections: 'Agent CLIs',
  github: 'GitHub',
  'security-scan': 'Security scan',
  providers: 'Models',
  models: 'Models',
  routing: 'Routing',
  backups: 'Backups',
  data: 'Export and import',
  support: 'Support bundle',
  license: 'License',
  settings: 'Settings',
  'settings-shortcuts': 'Shortcuts',
  'settings-about': 'About',
  'not-found': 'Not found',
};

/** The page's fixed name, as the window title says it. */
export const pageName = (r: Route): string => PAGE_NAMES[r.name] ?? '';

/** The window title: what waits on the person, the page, the project when the page is one of a project's, then Drydock. */
export function windowTitle(r: Route, projectName: string | undefined, needs: number): string {
  const parts = [pageName(r), isWorkspacePage(r) ? '' : (projectName ?? '').trim(), 'Drydock'].filter(Boolean);
  return `${needs > 0 ? `(${needs}) ` : ''}${parts.join(' · ')}`;
}
