// The URL contract (remix/routes). Every screen is a path; links are plain <a href={routes.x.href(...)}> and the Remix SPA
// runtime routes them in the page. The Go side serves index.html for these paths on a reload (internal/uiassets).

import { get, route } from 'remix/routes';

export const routes = route({
  home: get('/'),
  // the Inbox (decision P97): what needs the person, what runs and what was done today, in every project (?tab=running|done)
  needsYou: get('/needs-you'),
  // the tasks assigned to the person, in every project
  myTasks: get('/my-tasks'),
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
  // every project's agents as one crew (graphite, decision P97): the same page as a project's Crew, the cards grouped by project
  crew: get('/crew'),
  chats: get('/chats'),
  chat: get('/chats/:id'),
  // Agents (decision P140): one library with the tabs My agents (/library, the agent templates), Starters (the prebuilt
  // agents) and Skills
  library: get('/library'),
  libraryStarters: get('/library/starters'),
  librarySkills: get('/library/skills'),
  // everything Drydock connects to, once for the whole workspace (decision P98): services, agent tools, your own MCP servers;
  // /integrations/<key> opens one of them
  integrations: get('/integrations(/:key)'),
  // the agent CLIs the crew runs on (decision P98); /agents/<connection key> opens one
  agents: get('/agents(/:key)'),
  // an old address: Connections became Agents (decision P98)
  connections: get('/connections'),
  // everything Drydock does with GitHub, on one settings page: your gh sign-in, the connection, the MCP server, the CI runners
  github: get('/github'),
  // scan any folder on this computer, or one of the projects (decision P61)
  securityScan: get('/security-scan'),
  // an old address: Providers merged into Models (decision P98)
  providers: get('/providers'),
  // Models (decisions P65, P97, P98): this PC's engine and models, and the model providers; /models/<key> opens one
  models: get('/models(/:key)'),
  routing: get('/routing'),
  backups: get('/backups'),
  data: get('/data'),
  support: get('/support'),
  license: get('/license'),
  // one Settings (decision P140): General is /settings; every other section keeps the address it had as a page of its own
  // (shell/settings-sections.ts), and the two sections that never had one live under /settings
  settings: get('/settings'),
  settingsShortcuts: get('/settings/shortcuts'),
  settingsAbout: get('/settings/about'),
  // the Guide: explanations the pages leave out; one topic per path (the UI audit of 2026-10-19)
  guide: get('/guide(/:topic)'),
});
