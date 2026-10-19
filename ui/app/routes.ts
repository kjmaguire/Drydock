// The URL contract (remix/routes). Every screen is a path; links are plain <a href={routes.x.href(...)}> and the Remix SPA
// runtime routes them in the page. The Go side serves index.html for these paths on a reload (internal/uiassets).

import { get, route } from 'remix/routes';

export const routes = route({
  home: get('/'),
  needsYou: get('/needs-you'),
  room: get('/room/:id'),
  // the Rooms tab: opens the project's general channel (decision P64)
  rooms: get('/rooms'),
  tasks: get('/tasks'),
  taskNew: get('/tasks/new'),
  task: get('/task/:id'),
  run: get('/task/:id/run(/:runId)'),
  pulls: get('/pulls'),
  team: get('/team'),
  // one agent's page (Crew HQ, decision P64): who it is, what it is doing, its skills, tools, memory and history
  agent: get('/team/:id'),
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
  // the project's repo map: its most important code, ranked (decision P62)
  repoMap: get('/repo-map'),
  // the Knowledge tab: what the crew knows and where it came from (decision P64)
  knowledge: get('/knowledge'),
  // workspace screens: they open without a project
  workspace: get('/home'),
  chats: get('/chats'),
  chat: get('/chats/:id'),
  library: get('/library'),
  librarySkills: get('/library/skills'),
  integrations: get('/integrations'),
  connections: get('/connections'),
  // everything Drydock does with GitHub, on one settings page: your gh sign-in, the connection, the MCP server, the CI runners
  github: get('/github'),
  // scan any folder on this computer, or one of the projects (decision P61)
  securityScan: get('/security-scan'),
  providers: get('/providers'),
  routing: get('/routing'),
  backups: get('/backups'),
  data: get('/data'),
  support: get('/support'),
  license: get('/license'),
  settings: get('/settings'),
  // the Guide: explanations the pages leave out; one topic per path (the UI audit of 2026-10-19)
  guide: get('/guide(/:topic)'),
});
