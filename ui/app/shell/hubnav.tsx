// What a hub shows under the top bar (Crew HQ, decision P64): a row of pills for a hub's sub-pages (Overview, Tasks,
// Knowledge, Library), the Settings rail (every settings page, grouped: this project, the workspace, the app), and the
// Rooms rail (the project's channels, task rooms and direct messages). Every page the old sidebar linked is reached from
// one of these, with the same test id.

import { css, type Handle, type RemixNode } from 'remix/component';
import type { Room } from '../api/types.ts';
import { roomLabel } from '../lib/format.ts';
import { hubOf, type Route } from '../lib/route.ts';
import { routes } from '../routes.ts';
import { services, watch } from '../state/services.tsx';
import { Avatar, presenceColor } from '../ui/data.tsx';
import { Icon, roomIcon } from '../ui/icon.tsx';
import { t } from '../ui/tokens.ts';

const subCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: '4px',
  flex: 'none',
  padding: `${t.s2} ${t.s5}`,
  borderBottom: `1px solid ${t.border}`,
  background: t.bgApp,
  overflowX: 'auto',
});
const pillCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  height: '30px',
  padding: `0 ${t.s3}`,
  borderRadius: t.rFull,
  color: t.textMuted,
  fontSize: t.textSm,
  fontWeight: '550',
  textDecoration: 'none',
  whiteSpace: 'nowrap',
  border: '1px solid transparent',
  '&:hover': { background: t.bgHover, color: t.text, textDecoration: 'none' },
  '&[aria-current="page"]': { background: t.bgSurface, color: t.text, borderColor: t.border, boxShadow: t.shadowSm },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
});

interface Pill {
  label: string;
  href: string;
  testId: string;
  /** The route names this pill is current on. */
  on: Route['name'][];
}

const PILLS: Partial<Record<ReturnType<typeof hubOf>, Pill[]>> = {
  overview: [
    { label: 'Overview', href: routes.home.href(), testId: 'nav-overview-home', on: ['home'] },
    { label: 'Activity', href: routes.activity.href(), testId: 'nav-activity', on: ['activity'] },
    { label: 'Budgets', href: routes.budgets.href(), testId: 'nav-budgets', on: ['budgets'] },
    { label: 'Evals report', href: routes.evals.href(), testId: 'nav-evals', on: ['evals'] },
    { label: 'Security', href: routes.security.href(), testId: 'nav-security', on: ['security'] },
  ],
  tasks: [
    { label: 'Board', href: routes.tasks.href(), testId: 'nav-tasks-board', on: ['tasks', 'task', 'task-new', 'run'] },
    { label: 'Plan', href: routes.plan.href(), testId: 'nav-plan', on: ['plan'] },
    { label: 'Pull requests', href: routes.pulls.href(), testId: 'nav-pulls', on: ['pulls'] },
    { label: 'Milestones', href: routes.milestones.href(), testId: 'nav-milestones', on: ['milestones'] },
  ],
  knowledge: [
    { label: 'Overview', href: routes.knowledge.href(), testId: 'nav-knowledge-home', on: ['knowledge'] },
    { label: 'Requirements', href: routes.requirements.href(), testId: 'nav-requirements', on: ['requirements'] },
    { label: 'Expert packs', href: routes.packs.href(), testId: 'nav-packs', on: ['packs'] },
    { label: 'Skills', href: routes.skills.href(), testId: 'nav-skills', on: ['skills'] },
    { label: 'Memory', href: routes.memory.href(), testId: 'nav-memory', on: ['memory'] },
    { label: 'Repo map', href: routes.repoMap.href(), testId: 'nav-repo-map', on: ['repo-map'] },
  ],
  library: [
    { label: 'Agents and MCP servers', href: routes.library.href(), testId: 'nav-library-home', on: ['library'] },
    { label: 'Skills', href: routes.librarySkills.href(), testId: 'nav-library-skills', on: ['library-skills'] },
  ],
};

/** The pills of the route's hub, or nothing when the hub has one page. */
export function SubNav(handle: Handle<{ route: Route }>) {
  return () => {
    const { route } = handle.props;
    const pills = PILLS[hubOf(route)];
    if (!pills) return null;
    return (
      <nav aria-label="Section" mix={subCss}>
        {pills.map((p) => (
          <a key={p.testId} href={p.href} mix={pillCss} data-testid={p.testId} aria-current={p.on.includes(route.name) ? 'page' : undefined}>
            {p.label}
          </a>
        ))}
      </nav>
    );
  };
}

// --- rails -------------------------------------------------------------------------------------------------------------

const railCss = css({
  display: 'flex',
  flexDirection: 'column',
  gap: '2px',
  flex: 'none',
  width: 'var(--sidebar-width)',
  minHeight: '0',
  overflowY: 'auto',
  padding: `${t.s4} ${t.s3}`,
  borderRight: `1px solid ${t.border}`,
  background: t.bgSidebar,
});
const groupHead = css({ margin: `${t.s3} ${t.s2} ${t.s1}`, color: t.textFaint, fontSize: t.textXs, fontWeight: '650', letterSpacing: '0.06em', textTransform: 'uppercase', '&:first-child': { marginTop: '0' } });
const listCss = css({ display: 'grid', gap: '2px', margin: '0', padding: '0', listStyle: 'none' });
const itemCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: '34px',
  padding: `0 ${t.s2}`,
  borderRadius: t.rMd,
  color: t.textMuted,
  fontSize: t.textMd,
  fontWeight: '500',
  textDecoration: 'none',
  whiteSpace: 'nowrap',
  minWidth: '0',
  '& > .label': { flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& > .room-icon': { display: 'inline-grid', placeItems: 'center', width: '26px', height: '26px', flex: 'none', borderRadius: '8px', background: t.bgActive, color: t.textMuted },
  '&:hover': { background: t.bgHover, color: t.text, textDecoration: 'none' },
  '&[aria-current="page"]': { background: t.bgSurface, color: t.text, fontWeight: '600', boxShadow: t.shadowSm },
  '&[data-unread="true"]': { color: t.text, fontWeight: '600' },
  '&:focus-visible': { boxShadow: `inset ${t.focus}`, outline: 'none' },
});
// the unread count is a quiet number, read once ("general, 3 unread")
const countCss = css({ minWidth: '18px', height: '18px', padding: '0 5px', borderRadius: t.rFull, background: t.accent, color: t.onAccent, fontSize: t.textXs, fontWeight: '700', lineHeight: '18px', textAlign: 'center', fontVariantNumeric: 'tabular-nums' });

function Group(handle: Handle<{ title: string; children?: RemixNode }>) {
  return () => (
    <div>
      <h2 mix={groupHead}>{handle.props.title}</h2>
      <ul mix={listCss}>{handle.props.children}</ul>
    </div>
  );
}

function RailLink(handle: Handle<{ href: string; current: boolean; testId: string; label: string }>) {
  return () => {
    const { href, current, testId, label } = handle.props;
    return (
      <li>
        <a href={href} mix={itemCss} data-testid={testId} aria-current={current ? 'page' : undefined}>
          <span class="label">{label}</span>
        </a>
      </li>
    );
  };
}

/** The Settings rail: this project's pages (when one is open), the workspace's and the app's. */
export function SettingsRail(handle: Handle<{ route: Route }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  return () => {
    const { route } = handle.props;
    const at = (...names: Route['name'][]) => names.includes(route.name);
    const project = s.workspace.project;
    return (
      <nav aria-label="Settings" mix={railCss}>
        {project ? (
          <Group title={`This project · ${project.name}`}>
            <RailLink href={routes.project.href()} current={at('project')} testId="nav-project" label="Project settings" />
            <RailLink href={routes.projectIntegrations.href()} current={at('project-integrations')} testId="nav-project-integrations" label="Integrations" />
          </Group>
        ) : null}
        <Group title="Workspace">
          <RailLink href={routes.integrations.href()} current={at('integrations', 'mcp')} testId="nav-integrations" label="Integrations" />
          <RailLink href={routes.connections.href()} current={at('connections')} testId="nav-connections" label="Connections" />
          <RailLink href={routes.providers.href()} current={at('providers')} testId="nav-providers" label="Providers" />
          <RailLink href={routes.routing.href()} current={at('routing')} testId="nav-routing" label="Routing" />
        </Group>
        <Group title="App">
          <RailLink href={routes.settings.href()} current={at('settings')} testId="nav-settings-about" label="Settings and About" />
          <RailLink href={routes.license.href()} current={at('license')} testId="nav-license" label="License" />
          <RailLink href={routes.backups.href()} current={at('backups')} testId="nav-backups" label="Backups" />
          <RailLink href={routes.data.href()} current={at('data')} testId="nav-data" label="Export and import" />
          <RailLink href={routes.support.href()} current={at('support')} testId="nav-support" label="Support bundle" />
        </Group>
      </nav>
    );
  };
}

/**
 * A room's link: an icon (or the agent's dot for a direct message), the name, and a quiet unread count. Rooms are named
 * without "#", so the link's accessible name starts with the room name. Shared by the Rooms rail and the Overview's rooms card.
 */
export function RoomLink(handle: Handle<{ room: Room; current: boolean }>) {
  const s = services(handle);
  watch(handle, s.project);
  return () => {
    const { room: r, current } = handle.props;
    const p = s.project;
    const agents = p.agentList;
    const n = (p.unread.data ?? []).find((u) => u.roomId === r.id)?.unread ?? 0;
    // a direct message shows the agent, a task room its writer (both as dots); a channel its icon
    const agentId = r.kind === 'dm' ? r.name.slice(r.name.lastIndexOf('+') + 1) : r.kind === 'task' ? (p.taskList.find((x) => x.id === r.taskId)?.assignee ?? '') : '';
    const agent = agentId ? agents.find((a) => a.id === agentId) : undefined;
    const label = roomLabel(r, agents);
    return (
      <li>
        <a href={routes.room.href({ id: r.id })} mix={itemCss} data-testid="room-link" aria-current={current ? 'page' : undefined} data-unread={n > 0 ? 'true' : undefined}>
          {r.kind === 'dm' || (r.kind === 'task' && agent) ? (
            <Avatar id={agentId} name={agent?.name ?? label} size={24} kind="agent" status={presenceColor(p.presence.data?.find((x) => x.agentId === agentId))} />
          ) : (
            <span class="room-icon">
              <Icon icon={roomIcon(r.kind)} size={14} />
            </span>
          )}
          <span class="label">{label}</span>
          {n > 0 ? (
            <span mix={countCss} title={`${n} unread`} data-testid="unread-count">
              <span aria-hidden="true">{n}</span>
              <span class="sr-only">, {n} unread</span>
            </span>
          ) : null}
        </a>
      </li>
    );
  };
}

/** The Rooms rail: team rooms, task rooms and direct messages of the open project. */
export function RoomsRail(handle: Handle<{ route: Route }>) {
  const s = services(handle);
  watch(handle, s.project);
  return () => {
    const { route } = handle.props;
    const rooms = s.project.roomList.filter((r) => !r.archived);
    const current = (r: Room) => route.name === 'room' && route.id === r.id;
    const of = (kind: Room['kind']) => rooms.filter((r) => r.kind === kind).map((r) => <RoomLink key={r.id} room={r} current={current(r)} />);
    return (
      <nav aria-label="Rooms" mix={railCss} data-rooms="">
        <Group title="Team rooms">{of('channel')}</Group>
        {rooms.some((r) => r.kind === 'task') ? <Group title="Task rooms">{of('task')}</Group> : null}
        {rooms.some((r) => r.kind === 'dm') ? <Group title="Direct messages">{of('dm')}</Group> : null}
      </nav>
    );
  };
}
