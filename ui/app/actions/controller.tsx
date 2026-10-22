// Route actions: each loads its screen module on demand (one chunk per screen) and renders it. Screens read their data
// through live queries, so an action needs nothing but the route params. Keys remount a screen when its params change.

import { createController } from 'remix/router';
import type { AppContext } from '../router.tsx';
import { routes } from '../routes.ts';

/** The Integrations page. */
async function integrations({ render }: Pick<AppContext, 'render'>) {
  const { IntegrationsScreen } = await import('../screens/integrations/integrations-screen.tsx');
  return render(<IntegrationsScreen />);
}

export const controller = createController(routes, {
  actions: {
    async home({ render }) {
      const { OverviewScreen } = await import('../screens/overview/overview-screen.tsx');
      return render(<OverviewScreen />);
    },
    async rooms({ render }) {
      const { RoomsScreen } = await import('../screens/rooms/rooms-screen.tsx');
      return render(<RoomsScreen />);
    },
    async knowledge({ render }) {
      const { KnowledgeScreen } = await import('../screens/knowledge/knowledge-screen.tsx');
      return render(<KnowledgeScreen />);
    },
    async room({ render, params }) {
      const { RoomScreen } = await import('../screens/room/room-screen.tsx');
      return render(<RoomScreen key={params.id} id={params.id} />);
    },
    async needsYou({ render, url }) {
      const { NeedsYouScreen } = await import('../screens/needs-you/needs-you-screen.tsx');
      return render(<NeedsYouScreen tab={url.searchParams.get('tab') ?? undefined} />);
    },
    async myTasks({ render }) {
      const { MyTasksScreen } = await import('../screens/needs-you/my-tasks-screen.tsx');
      return render(<MyTasksScreen />);
    },
    async tasks({ render }) {
      const { TaskListScreen } = await import('../screens/tasks/task-list-screen.tsx');
      return render(<TaskListScreen />);
    },
    async taskNew({ render }) {
      const { TaskCreateScreen } = await import('../screens/tasks/task-create-screen.tsx');
      return render(<TaskCreateScreen />);
    },
    async task({ render, params }) {
      const { TaskDetailScreen } = await import('../screens/tasks/task-detail-screen.tsx');
      return render(<TaskDetailScreen key={params.id} id={params.id} />);
    },
    async run({ render, params }) {
      const { RunScreen } = await import('../screens/run/run-screen.tsx');
      return render(<RunScreen key={`${params.id}:${params.runId ?? ''}`} taskId={params.id} runId={params.runId} />);
    },
    async pulls({ render }) {
      const { PullsScreen } = await import('../screens/pulls/pulls-screen.tsx');
      return render(<PullsScreen />);
    },
    async team({ render }) {
      const { TeamScreen } = await import('../screens/team/team-screen.tsx');
      return render(<TeamScreen />);
    },
    async crew({ render }) {
      const { WorkspaceCrewScreen } = await import('../screens/team/team-screen.tsx');
      return render(<WorkspaceCrewScreen />);
    },
    async agent({ render, params }) {
      const { AgentScreen } = await import('../screens/team/agent-screen.tsx');
      return render(<AgentScreen key={params.id} id={params.id} />);
    },
    async activity({ render }) {
      const { ActivityScreen } = await import('../screens/activity/activity-screen.tsx');
      return render(<ActivityScreen />);
    },
    async project({ render }) {
      const { ProjectSettingsScreen } = await import('../screens/project/project-settings-screen.tsx');
      return render(<ProjectSettingsScreen />);
    },
    async projectIntegrations({ render }) {
      const { ProjectIntegrationsScreen } = await import('../screens/integrations/project-integrations-screen.tsx');
      return render(<ProjectIntegrationsScreen />);
    },
    async projectNew({ render }) {
      const { ProjectCreateScreen } = await import('../screens/project/project-create-screen.tsx');
      return render(<ProjectCreateScreen />);
    },
    async plan({ render }) {
      const { PlanScreen } = await import('../screens/plan/plan-screen.tsx');
      return render(<PlanScreen />);
    },
    async requirements({ render }) {
      const { RequirementsScreen } = await import('../screens/requirements/requirements-screen.tsx');
      return render(<RequirementsScreen />);
    },
    async milestones({ render }) {
      const { MilestonesScreen } = await import('../screens/milestones/milestones-screen.tsx');
      return render(<MilestonesScreen />);
    },
    async memory({ render }) {
      const { MemoryScreen } = await import('../screens/memory/memory-screen.tsx');
      return render(<MemoryScreen />);
    },
    async skills({ render }) {
      const { SkillsScreen } = await import('../screens/skills/skills-screen.tsx');
      return render(<SkillsScreen />);
    },
    async packs({ render }) {
      const { PacksScreen } = await import('../screens/packs/packs-screen.tsx');
      return render(<PacksScreen />);
    },
    // /mcp, the former MCP servers page merged into Integrations (decision P47), is an old address: it opens the one page's own address
    async mcp({ render }) {
      const { Redirect } = await import('../shell/redirect.tsx');
      return render(<Redirect to={routes.integrations.href()} />);
    },
    async budgets({ render }) {
      const { BudgetsScreen } = await import('../screens/budgets/budgets-screen.tsx');
      return render(<BudgetsScreen />);
    },
    async evals({ render }) {
      const { EvalsScreen } = await import('../screens/evals/evals-screen.tsx');
      return render(<EvalsScreen />);
    },
    async security({ render }) {
      const { SecurityScreen } = await import('../screens/security/security-screen.tsx');
      return render(<SecurityScreen />);
    },
    async repoMap({ render }) {
      const { RepoMapScreen } = await import('../screens/repomap/repomap-screen.tsx');
      return render(<RepoMapScreen />);
    },
    async chats({ render }) {
      const { ChatsScreen } = await import('../screens/chats/chats-screen.tsx');
      // one key for the list and every chat, so switching chats keeps the list (and its search) mounted
      return render(<ChatsScreen key="chats" />);
    },
    async chat({ render, params }) {
      const { ChatsScreen } = await import('../screens/chats/chats-screen.tsx');
      return render(<ChatsScreen key="chats" id={params.id} />);
    },
    async workspace({ render }) {
      const { WorkspaceHomeScreen } = await import('../screens/workspace/workspace-home.tsx');
      return render(<WorkspaceHomeScreen />);
    },
    async library({ render }) {
      const { LibraryScreen } = await import('../screens/library/library-screen.tsx');
      return render(<LibraryScreen />);
    },
    async librarySkills({ render }) {
      const { SkillLibraryScreen } = await import('../screens/skills/skill-library-screen.tsx');
      return render(<SkillLibraryScreen />);
    },
    integrations,
    async connections({ render }) {
      const { ConnectionsScreen } = await import('../screens/connections/connections-screen.tsx');
      return render(<ConnectionsScreen />);
    },
    async github({ render }) {
      const { GitHubScreen } = await import('../screens/github/github-screen.tsx');
      return render(<GitHubScreen />);
    },
    async securityScan({ render }) {
      const { WorkspaceSecurityScreen } = await import('../screens/security/workspace-security-screen.tsx');
      return render(<WorkspaceSecurityScreen />);
    },
    async providers({ render }) {
      const { ProvidersScreen } = await import('../screens/providers/providers-screen.tsx');
      return render(<ProvidersScreen />);
    },
    async routing({ render }) {
      const { RoutingScreen } = await import('../screens/routing/routing-screen.tsx');
      return render(<RoutingScreen />);
    },
    async backups({ render }) {
      const { BackupsScreen } = await import('../screens/backups/backups-screen.tsx');
      return render(<BackupsScreen />);
    },
    async data({ render }) {
      const { DataScreen } = await import('../screens/data/data-screen.tsx');
      return render(<DataScreen />);
    },
    async guide({ render, params }) {
      const { GuideScreen } = await import('../screens/guide/guide-screen.tsx');
      return render(<GuideScreen key={params.topic ?? ''} topic={params.topic} />);
    },
    async support({ render }) {
      const { SupportScreen } = await import('../screens/support/support-screen.tsx');
      return render(<SupportScreen />);
    },
    async license({ render }) {
      const { LicenseScreen } = await import('../screens/license/license-screen.tsx');
      return render(<LicenseScreen />);
    },
    async settings({ render }) {
      const { SettingsScreen } = await import('../screens/settings/settings-screen.tsx');
      return render(<SettingsScreen />);
    },
  },
});
