import { useRef, type KeyboardEvent } from 'react';
import type { Agent, PresenceState, Project, Room, Unread } from '../api/types';
import type { Route } from '../lib/route';
import { Badge, PresenceDot } from './common';

interface Props {
  projects: Project[];
  project: Project | undefined;
  onProject: (id: string) => void;
  route: Route;
  rooms: Room[];
  unread: Unread[];
  needsCount: number;
  agents: Agent[];
  presence: PresenceState[];
  stream: string;
}

function NavLink({ href, current, children, testId }: { href: string; current: boolean; children: React.ReactNode; testId?: string }) {
  return (
    <a href={href} data-nav="" aria-current={current ? 'page' : undefined} className={current ? 'nav-item current' : 'nav-item'} data-testid={testId}>
      {children}
    </a>
  );
}

export function Sidebar({ projects, project, onProject, route, rooms, unread, needsCount, agents, presence, stream }: Props) {
  const nav = useRef<HTMLElement>(null);
  const unreadOf = (id: string) => unread.find((u) => u.roomId === id)?.unread ?? 0;
  const channels = rooms.filter((r) => r.kind === 'channel');
  const taskRooms = rooms.filter((r) => r.kind === 'task');
  const dms = rooms.filter((r) => r.kind === 'dm');
  const roomLink = (r: Room) => (
    <li key={r.id}>
      <NavLink href={`#/room/${encodeURIComponent(r.id)}`} current={route.name === 'room' && route.id === r.id} testId="room-link">
        <span>
          {r.kind === 'task' ? '' : '# '}
          {r.name}
        </span>
        {unreadOf(r.id) > 0 ? <Badge tone="info" title={`${unreadOf(r.id)} unread`}>{unreadOf(r.id)}</Badge> : null}
      </NavLink>
    </li>
  );

  // roving focus: arrow keys move between sidebar links (docs/ui.md section 14)
  const onKey = (e: KeyboardEvent<HTMLElement>) => {
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key)) return;
    const items = Array.from(nav.current?.querySelectorAll<HTMLElement>('[data-nav]') ?? []);
    if (items.length === 0) return;
    const i = items.indexOf(document.activeElement as HTMLElement);
    let n = i;
    if (e.key === 'ArrowDown') n = Math.min(items.length - 1, i + 1);
    else if (e.key === 'ArrowUp') n = Math.max(0, i - 1);
    else if (e.key === 'Home') n = 0;
    else n = items.length - 1;
    e.preventDefault();
    items[n]?.focus();
  };

  return (
    <nav className="sidebar" aria-label="Main" ref={nav} onKeyDown={onKey}>
      <div className="brand">Drydock</div>
      {project ? (
        <div className="project-switch">
          <label className="sr-only" htmlFor="project-select">
            Project
          </label>
          <select id="project-select" value={project.id} onChange={(e) => onProject(e.target.value)}>
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
          <a href="#/project/new" data-nav="" className="nav-small">
            + New project
          </a>
        </div>
      ) : null}
      {project ? (
        <>
          <ul className="nav-list">
            <li>
              <NavLink href="#/needs-you" current={route.name === 'needs-you'} testId="nav-needs-you">
                <span>Needs you</span>
                <Badge tone={needsCount > 0 ? 'err' : 'muted'}>
                  <span data-testid="needs-count" aria-label={`${needsCount} items need you`}>
                    {needsCount}
                  </span>
                </Badge>
              </NavLink>
            </li>
          </ul>
          <h2 className="nav-h">Channels</h2>
          <ul className="nav-list">{channels.map(roomLink)}</ul>
          {taskRooms.length > 0 ? (
            <>
              <h2 className="nav-h">Task channels</h2>
              <ul className="nav-list">{taskRooms.map(roomLink)}</ul>
            </>
          ) : null}
          {dms.length > 0 ? (
            <>
              <h2 className="nav-h">Direct messages</h2>
              <ul className="nav-list">{dms.map(roomLink)}</ul>
            </>
          ) : null}
          <h2 className="nav-h">Project</h2>
          <ul className="nav-list">
            <li>
              <NavLink href="#/tasks" current={route.name === 'tasks' || route.name === 'task' || route.name === 'task-new'} testId="nav-tasks">
                Tasks
              </NavLink>
            </li>
            <li>
              <NavLink href="#/pulls" current={route.name === 'pulls'}>
                Pull Requests
              </NavLink>
            </li>
            <li>
              <NavLink href="#/team" current={route.name === 'team'} testId="nav-team">
                Team
              </NavLink>
            </li>
            <li>
              <NavLink href="#/activity" current={route.name === 'activity'}>
                Activity
              </NavLink>
            </li>
            <li>
              <NavLink href="#/project" current={route.name === 'project'}>
                Project settings
              </NavLink>
            </li>
          </ul>
          {agents.length > 0 ? (
            <>
              <h2 className="nav-h">Agents</h2>
              <ul className="nav-list agents-mini">
                {agents.map((a) => (
                  <li key={a.id} className="agent-mini">
                    <span>{a.name}</span>
                    <PresenceDot presence={presence.find((p) => p.agentId === a.id)} />
                  </li>
                ))}
              </ul>
            </>
          ) : null}
        </>
      ) : null}
      <div className="sidebar-foot">
        <ul className="nav-list">
          <li>
            <NavLink href="#/settings" current={route.name === 'settings'}>
              Settings and About
            </NavLink>
          </li>
        </ul>
        <div className="muted stream" data-testid="stream" data-state={stream}>
          Event stream: {stream}
        </div>
      </div>
    </nav>
  );
}
