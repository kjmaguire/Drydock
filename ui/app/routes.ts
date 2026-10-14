// The URL contract (remix/routes). Every screen is a path; links are plain <a href={routes.x.href(...)}> and the Remix SPA
// runtime routes them in the page. The Go side serves index.html for these paths on a reload (internal/uiassets).

import { get, route } from 'remix/routes';

export const routes = route({
  home: get('/'),
  needsYou: get('/needs-you'),
  room: get('/room/:id'),
  tasks: get('/tasks'),
  taskNew: get('/tasks/new'),
  task: get('/task/:id'),
  run: get('/task/:id/run(/:runId)'),
  pulls: get('/pulls'),
  team: get('/team'),
  activity: get('/activity'),
  project: get('/project'),
  projectNew: get('/project/new'),
  // the project's own integrations: its MCP servers and grants, the approvals waiting, its app-sync assignments
  projectIntegrations: get('/project/integrations'),
  plan: get('/plan'),
  requirements: get('/requirements'),
  milestones: get('/milestones'),
  memory: get('/memory'),
  skills: get('/skills'),
  packs: get('/packs'),
  mcp: get('/mcp'),
  budgets: get('/budgets'),
  evals: get('/evals'),
  security: get('/security'),
  // workspace screens: they open without a project
  workspace: get('/home'),
  chats: get('/chats'),
  chat: get('/chats/:id'),
  library: get('/library'),
  librarySkills: get('/library/skills'),
  integrations: get('/integrations'),
  connections: get('/connections'),
  providers: get('/providers'),
  routing: get('/routing'),
  backups: get('/backups'),
  data: get('/data'),
  support: get('/support'),
  license: get('/license'),
  settings: get('/settings'),
});
