import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import type { Agent, AppInfo, PresenceState, Project, Room } from './api/types';
import { ActivityView } from './components/Activity';
import { AgentInspector } from './components/Inspector';
import { ThreadPanel } from './components/Thread';
import { UiProvider, useUi } from './state/ui';
import { Empty, ErrorNote, Page, PresenceDot } from './components/common';
import { Conversation } from './components/Conversation';
import { NeedsYouView } from './components/NeedsYou';
import { ProjectCreate, ProjectSettings } from './components/ProjectPages';
import { PullRequestsView } from './components/PullRequests';
import { SettingsView } from './components/Settings';
import { Sidebar } from './components/Sidebar';
import { TaskCreate, TaskDetailView, TaskList } from './components/Tasks';
import { TeamView } from './components/Team';
import { LicenseBanner, LicenseProvider } from './state/license';
import { applyTheme, loadTheme, type ThemeChoice } from './lib/theme';
import { parseRoute, type Route } from './lib/route';
import { agentName, formatCost, presenceFor } from './lib/format';
import { useClient, useStoreSnapshot } from './state/context';
import { useAgents, useLive, useNeedsYou, usePresence, useProjects, useRooms, useTaskDetail, useTasks, useUnread } from './state/hooks';
import { StatusChip } from './components/common';

// Pages that are not on the first screen load on demand, so the first paint stays small.
const ConnectionsView = lazy(() => import('./components/Connections').then((m) => ({ default: m.ConnectionsView })));
const ProvidersView = lazy(() => import('./components/Providers').then((m) => ({ default: m.ProvidersView })));
const BudgetsView = lazy(() => import('./pages/Budgets'));
const EvalsView = lazy(() => import('./pages/Evals'));
const RoutingView = lazy(() => import('./pages/Routing'));
const PacksView = lazy(() => import('./pages/Packs'));
const RequirementsView = lazy(() => import('./pages/Requirements'));
const MilestonesView = lazy(() => import('./pages/Milestones'));
const SkillsView = lazy(() => import('./pages/Skills'));
const MemoryView = lazy(() => import('./pages/Memory'));
const PlanView = lazy(() => import('./pages/Plan'));
const IntegrationsView = lazy(() => import('./pages/Integrations'));
const LicenseView = lazy(() => import('./pages/License'));
const BackupsView = lazy(() => import('./pages/Backups'));
const DataView = lazy(() => import('./pages/Data'));
const SupportView = lazy(() => import('./pages/Support'));

const PROJECT_KEY = 'drydock.project';
const readProject = (): string | undefined => {
  try {
    return localStorage.getItem(PROJECT_KEY) ?? undefined;
  } catch {
    return undefined;
  }
};

function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() => parseRoute(window.location.hash));
  useEffect(() => {
    const on = () => setRoute(parseRoute(window.location.hash));
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);
  return route;
}

export function App() {
  const [rightOpen, setRightOpen] = useState(true);
  const reveal = useCallback(() => setRightOpen(true), []);
  return (
    <UiProvider onReveal={reveal}>
      <LicenseProvider>
        <AppShell rightOpen={rightOpen} setRightOpen={setRightOpen} />
      </LicenseProvider>
    </UiProvider>
  );
}

/** Routes that are about the whole workspace, not one project. They open even when no project exists yet. */
const WORKSPACE_ROUTES: Route['name'][] = ['settings', 'license', 'backups', 'data', 'support', 'connections', 'providers', 'routing'];

function workspaceCenter(name: Route['name'], projects: Project[], current: Project | undefined, theme: ThemeChoice, setTheme: (t: ThemeChoice) => void, info: AppInfo | undefined): React.ReactNode {
  switch (name) {
    case 'settings':
      return <SettingsView theme={theme} onTheme={setTheme} info={info} />;
    case 'license':
      return <LicenseView />;
    case 'backups':
      return <BackupsView />;
    case 'data':
      return <DataView projects={projects} current={current} />;
    case 'support':
      return <SupportView />;
    case 'connections':
      return <ConnectionsView />;
    case 'providers':
      return <ProvidersView />;
    case 'routing':
      return <RoutingView />;
    default:
      return null;
  }
}

function AppShell({ rightOpen, setRightOpen }: { rightOpen: boolean; setRightOpen: (f: (v: boolean) => boolean) => void }) {
  const client = useClient();
  const route = useRoute();
  const stream = useStoreSnapshot();
  const [theme, setTheme] = useState<ThemeChoice>(loadTheme);
  useEffect(() => applyTheme(theme), [theme]);

  const info = useLive(() => client.appInfo(), [], () => false);
  const projects = useProjects();
  const [chosen, setChosen] = useState<string | undefined>(readProject);
  const projectList = useMemo(() => projects.data ?? [], [projects.data]);
  const project = projectList.find((p) => p.id === chosen) ?? projectList[0];
  const pid = project?.id;
  const selectProject = useCallback((id: string) => {
    setChosen(id);
    try {
      localStorage.setItem(PROJECT_KEY, id);
    } catch {
      /* optional convenience */
    }
    window.location.hash = '#/';
  }, []);

  const agents = useAgents(pid);
  const rooms = useRooms(pid);
  const ui = useUi();
  const unread = useUnread(pid);
  const needs = useNeedsYou(pid);
  const presence = usePresence();
  const tasks = useTasks(pid);

  const agentList = agents.data ?? [];
  const roomList = rooms.data ?? [];
  const needsCount = needs.data?.length ?? 0;
  const [dmError, setDmError] = useState<Error | undefined>();
  const openDM = useCallback(
    async (agentId: string) => {
      if (!pid) return;
      setDmError(undefined);
      try {
        const r = await client.roomCreateDM(pid, agentId);
        window.location.hash = `#/room/${encodeURIComponent(r.id)}`;
      } catch (e) {
        setDmError(e as Error);
      }
    },
    [client, pid],
  );

  useEffect(() => {
    document.title = needsCount > 0 ? `(${needsCount}) Drydock` : 'Drydock';
  }, [needsCount]);

  // home: open the project's first channel
  useEffect(() => {
    if (route.name !== 'home' || !project) return;
    const first = roomList.find((r) => r.kind === 'channel');
    if (first) window.location.replace(`#/room/${encodeURIComponent(first.id)}`);
  }, [route, project, roomList]);

  let center: React.ReactNode;
  if (projects.loading && !projects.data) {
    center = <p className="muted pad">Loading...</p>;
  } else if (projects.error && !projects.data) {
    center = (
      <Page title="Cannot reach the Drydock core">
        <ErrorNote error={projects.error} />
        <p>The page needs the core's address and token (open the link the core printed, which ends in ?token=...).</p>
      </Page>
    );
  } else if (!project && WORKSPACE_ROUTES.includes(route.name)) {
    // pages that do not need a project must open without one: a read-only install has to reach License and Backups
    center = workspaceCenter(route.name, projectList, undefined, theme, setTheme, info.data);
  } else if (!project || route.name === 'project-new') {
    center = (
      <ProjectCreate
        first={!project}
        onCreated={(p) => {
          void projects.reload();
          selectProject(p.id);
        }}
      />
    );
  } else {
    switch (route.name) {
      case 'needs-you':
        center = <NeedsYouView projectId={project.id} agents={agentList} />;
        break;
      case 'room':
        center = <RoomRoute id={route.id} rooms={roomList} loadingRooms={rooms.loading && !rooms.data} agents={agentList} />;
        break;
      case 'tasks':
        center = <TaskList projectId={project.id} agents={agentList} />;
        break;
      case 'task-new':
        center = <TaskCreate projectId={project.id} />;
        break;
      case 'task':
        center = <TaskDetailView taskId={route.id} agents={agentList} key={route.id} />;
        break;
      case 'pulls':
        center = <PullRequestsView projectId={project.id} tasks={tasks.data ?? []} />;
        break;
      case 'team':
        center = (
          <TeamView
            projectId={project.id}
            agents={agentList}
            presence={presence.data ?? []}
            onCreated={agents.reload}
            onInspect={ui.openInspector}
            onMessage={openDM}
          />
        );
        break;
      case 'connections':
      case 'providers':
      case 'routing':
      case 'license':
      case 'backups':
      case 'data':
      case 'support':
        center = workspaceCenter(route.name, projectList, project, theme, setTheme, info.data);
        break;
      case 'budgets':
        center = <BudgetsView projectId={project.id} />;
        break;
      case 'evals':
        center = <EvalsView projectId={project.id} />;
        break;
      case 'packs':
        center = <PacksView projectId={project.id} />;
        break;
      case 'requirements':
        center = <RequirementsView projectId={project.id} />;
        break;
      case 'milestones':
        center = <MilestonesView projectId={project.id} />;
        break;
      case 'skills':
        center = <SkillsView projectId={project.id} />;
        break;
      case 'memory':
        center = <MemoryView projectId={project.id} />;
        break;
      case 'plan':
        center = <PlanView projectId={project.id} agents={agentList} />;
        break;
      case 'integrations':
        center = <IntegrationsView projects={projectList} />;
        break;
      case 'activity':
        center = <ActivityView projectId={project.id} />;
        break;
      case 'project':
        center = <ProjectSettings project={project} onChanged={projects.reload} key={`${project.id}:${project.mode}:${project.mergeStrategy}`} />;
        break;
      case 'settings':
        center = workspaceCenter('settings', projectList, project, theme, setTheme, info.data);
        break;
      default:
        center = <p className="muted pad">Opening the project channel...</p>;
    }
  }

  const taskContext =
    route.name === 'task' ? route.id : route.name === 'room' ? roomList.find((r) => r.id === route.id)?.taskId : undefined;

  return (
    <div className="app-root">
    <LicenseBanner />
    <div className={rightOpen ? 'app' : 'app right-closed'}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <Sidebar
        projects={projectList}
        project={project}
        onProject={selectProject}
        route={route}
        rooms={roomList}
        unread={unread.data ?? []}
        needsCount={needsCount}
        agents={agentList}
        presence={presence.data ?? []}
        stream={stream.status}
      />
      <main id="main" className="center" tabIndex={-1}>
        <ErrorNote error={dmError} what="Could not open the direct message" />
        <Suspense fallback={<p className="muted pad">Loading...</p>}>{center}</Suspense>
      </main>
      <aside className="right" aria-label="Details">
        <button className="right-toggle" onClick={() => setRightOpen((v) => !v)} aria-expanded={rightOpen} aria-controls="right-body">
          {rightOpen ? 'Hide details' : 'Show details'}
        </button>
        {rightOpen ? (
          <div id="right-body" className="right-body">
            <RightColumn taskId={taskContext} agents={agentList} presence={presence.data ?? []} onMessage={openDM} />
          </div>
        ) : null}
      </aside>
    </div>
    </div>
  );
}

function RightColumn({ taskId, agents, presence, onMessage }: { taskId: string | undefined; agents: Agent[]; presence: PresenceState[]; onMessage: (agentId: string) => void }) {
  const ui = useUi();
  const tab = ui.tab === 'task' && !taskId ? 'agents' : ui.tab === 'thread' && !ui.thread ? 'agents' : ui.tab === 'inspector' && !ui.inspectId ? 'agents' : ui.tab;
  const tabs: { id: 'agents' | 'task' | 'thread' | 'inspector'; label: string; show: boolean }[] = [
    { id: 'agents', label: 'Agents', show: true },
    { id: 'task', label: 'Task', show: !!taskId },
    { id: 'thread', label: 'Thread', show: !!ui.thread },
    { id: 'inspector', label: 'Inspector', show: !!ui.inspectId },
  ];
  return (
    <>
      <div role="tablist" aria-label="Details" className="tabs">
        {tabs
          .filter((t) => t.show)
          .map((t) => (
            <button key={t.id} role="tab" id={`tab-${t.id}`} aria-selected={tab === t.id} aria-controls={`panel-${t.id}`} onClick={() => ui.setTab(t.id)}>
              {t.label}
            </button>
          ))}
      </div>
      {tab === 'agents' ? (
        <div role="tabpanel" id="panel-agents" aria-labelledby="tab-agents">
          {agents.length === 0 ? <p className="muted">No agents yet.</p> : null}
          <ul className="agent-list">
            {agents.map((a) => {
              const p = presenceFor(a.id, presence);
              return (
                <li key={a.id}>
                  <strong>{a.name}</strong> <span className="muted">{a.role}</span>
                  <div>
                    <PresenceDot presence={p} />
                  </div>
                  {p?.activity ? <div className="muted">{p.activity}</div> : null}
                  <button className="link-button" onClick={() => ui.openInspector(a.id)}>
                    Inspect
                  </button>
                </li>
              );
            })}
          </ul>
        </div>
      ) : null}
      {tab === 'task' && taskId ? (
        <div role="tabpanel" id="panel-task" aria-labelledby="tab-task">
          <TaskSummary taskId={taskId} agents={agents} />
        </div>
      ) : null}
      {tab === 'thread' && ui.thread ? (
        <div role="tabpanel" id="panel-thread" aria-labelledby="tab-thread">
          <ThreadPanel roomId={ui.thread.roomId} rootId={ui.thread.rootId} agents={agents} onClose={ui.closeThread} />
        </div>
      ) : null}
      {tab === 'inspector' ? (
        <div role="tabpanel" id="panel-inspector" aria-labelledby="tab-inspector">
          <AgentInspector agentId={ui.inspectId} presence={presence} onMessage={onMessage} />
        </div>
      ) : null}
    </>
  );
}

/** A room route: the active list first, else room.get, so an archived task channel still opens. */
function RoomRoute({ id, rooms, loadingRooms, agents }: { id: string; rooms: Room[]; loadingRooms: boolean; agents: Agent[] }) {
  const client = useClient();
  const inList = rooms.find((r) => r.id === id);
  const fetched = useLive(() => client.roomGet(id), [id], (e) => e.type.startsWith('room.archived') || e.type.startsWith('room.created'), !inList && !loadingRooms);
  const room = inList ?? fetched.data;
  if (room) return <Conversation room={room} agents={agents} key={room.id} />;
  if (loadingRooms || fetched.loading) return <p className="muted pad">Loading...</p>;
  return (
    <Empty title="Channel not found">
      <ErrorNote error={fetched.error} />
    </Empty>
  );
}

function TaskSummary({ taskId, agents }: { taskId: string; agents: Agent[] }) {
  const d = useTaskDetail(taskId).data;
  if (!d) return <p className="muted">Loading...</p>;
  const passed = d.evidence.filter((e) => e.passed === true).length;
  const failed = d.evidence.filter((e) => e.passed === false).length;
  return (
    <div>
      <p>
        <a href={`#/task/${encodeURIComponent(d.task.id)}`}>
          #{d.task.seq} {d.task.title}
        </a>
      </p>
      <StatusChip status={d.task.status} />
      <dl className="kv">
        <dt>Worker</dt>
        <dd>{agentName(d.task.assignee, agents) || 'unassigned'}</dd>
        <dt>Reviewer</dt>
        <dd>{agentName(d.task.reviewer, agents) || 'unassigned'}</dd>
        <dt>Evidence</dt>
        <dd>
          {passed} passed, {failed} failed
        </dd>
        <dt>Cost</dt>
        <dd>{formatCost(d.task.costUsd)}</dd>
      </dl>
    </div>
  );
}
