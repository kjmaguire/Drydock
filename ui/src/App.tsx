import { useCallback, useEffect, useMemo, useState } from 'react';
import type { Agent, PresenceState } from './api/types';
import { ActivityView } from './components/Activity';
import { Empty, ErrorNote, Page, PresenceDot } from './components/common';
import { Conversation } from './components/Conversation';
import { NeedsYouView } from './components/NeedsYou';
import { ProjectCreate, ProjectSettings } from './components/ProjectPages';
import { PullRequestsView } from './components/PullRequests';
import { SettingsView } from './components/Settings';
import { Sidebar } from './components/Sidebar';
import { TaskCreate, TaskDetailView, TaskList } from './components/Tasks';
import { TeamView } from './components/Team';
import { applyTheme, loadTheme, type ThemeChoice } from './lib/theme';
import { parseRoute, type Route } from './lib/route';
import { agentName, formatCost, presenceFor } from './lib/format';
import { useClient, useStoreSnapshot } from './state/context';
import { useAgents, useLive, useNeedsYou, usePresence, useProjects, useRooms, useTaskDetail, useTasks, useUnread } from './state/hooks';
import { StatusChip } from './components/common';

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
  const client = useClient();
  const route = useRoute();
  const stream = useStoreSnapshot();
  const [theme, setTheme] = useState<ThemeChoice>(loadTheme);
  const [rightOpen, setRightOpen] = useState(true);
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
  const unread = useUnread(pid);
  const needs = useNeedsYou(pid);
  const presence = usePresence();
  const tasks = useTasks(pid);

  const agentList = agents.data ?? [];
  const roomList = rooms.data ?? [];
  const needsCount = needs.data?.length ?? 0;

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
      case 'room': {
        const room = roomList.find((r) => r.id === route.id);
        center = room ? (
          <Conversation room={room} agents={agentList} key={room.id} />
        ) : rooms.loading && !rooms.data ? (
          <p className="muted pad">Loading...</p>
        ) : (
          <Empty title="Channel not found">
            <p>It may be archived: the core lists only active channels, and a task channel is archived when its task ends.</p>
          </Empty>
        );
        break;
      }
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
        center = <TeamView projectId={project.id} agents={agentList} presence={presence.data ?? []} onCreated={agents.reload} />;
        break;
      case 'activity':
        center = <ActivityView projectId={project.id} />;
        break;
      case 'project':
        center = <ProjectSettings project={project} onChanged={projects.reload} key={`${project.id}:${project.mode}:${project.mergeStrategy}`} />;
        break;
      case 'settings':
        center = <SettingsView theme={theme} onTheme={setTheme} info={info.data} />;
        break;
      default:
        center = <p className="muted pad">Opening the project channel...</p>;
    }
  }
  if (route.name === 'settings' && !project) center = <SettingsView theme={theme} onTheme={setTheme} info={info.data} />;

  const taskContext =
    route.name === 'task' ? route.id : route.name === 'room' ? roomList.find((r) => r.id === route.id)?.taskId : undefined;

  return (
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
        {center}
      </main>
      <aside className="right" aria-label="Details">
        <button className="right-toggle" onClick={() => setRightOpen((v) => !v)} aria-expanded={rightOpen} aria-controls="right-body">
          {rightOpen ? 'Hide details' : 'Show details'}
        </button>
        {rightOpen ? (
          <div id="right-body" className="right-body">
            <RightColumn taskId={taskContext} agents={agentList} presence={presence.data ?? []} />
          </div>
        ) : null}
      </aside>
    </div>
  );
}

function RightColumn({ taskId, agents, presence }: { taskId: string | undefined; agents: Agent[]; presence: PresenceState[] }) {
  const [tab, setTab] = useState<'agents' | 'task'>('agents');
  const active = tab === 'task' && taskId ? 'task' : 'agents';
  return (
    <>
      <div role="tablist" aria-label="Details" className="tabs">
        <button role="tab" id="tab-agents" aria-selected={active === 'agents'} aria-controls="panel-agents" onClick={() => setTab('agents')}>
          Agents
        </button>
        {taskId ? (
          <button role="tab" id="tab-task" aria-selected={active === 'task'} aria-controls="panel-task" onClick={() => setTab('task')}>
            Task
          </button>
        ) : null}
      </div>
      {active === 'agents' ? (
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
                </li>
              );
            })}
          </ul>
        </div>
      ) : (
        <div role="tabpanel" id="panel-task" aria-labelledby="tab-task">
          {taskId ? <TaskSummary taskId={taskId} agents={agents} /> : null}
        </div>
      )}
    </>
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
