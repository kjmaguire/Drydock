// The sidebar (Slack's channel list with Linear's density): the project header, quick links (Needs you, Tasks, ...),
// channels, task channels, direct messages, then collapsible project, knowledge and workspace sections, and the event
// stream status. Arrow keys move between items (docs/ui.md section 14).

import { css, on, ref, type Handle, type RemixNode } from 'remix/component';
import {
  Activity,
  BookOpen,
  Brain,
  ChevronDown,
  CircleDot,
  ClipboardList,
  Cpu,
  Database,
  FileText,
  Flag,
  GitPullRequest,
  HardDriveDownload,
  Inbox,
  KeyRound,
  LifeBuoy,
  Map,
  MessagesSquare,
  Network,
  PiggyBank,
  Plug,
  Route as RouteIcon,
  Search,
  Server,
  Settings2,
  Sparkles,
  Users,
  Gauge,
  Library,
  Link2,
} from 'lucide';
import type { Room } from '../api/types.ts';
import { roomLabel } from '../lib/format.ts';
import type { Route } from '../lib/route.ts';
import { routes } from '../routes.ts';
import { services, watch } from '../state/services.tsx';
import { Avatar, presenceColor } from '../ui/data.tsx';
import { Icon, type IconNode } from '../ui/icon.tsx';
import { t } from '../ui/tokens.ts';

const sideCss = css({
  display: 'flex',
  flexDirection: 'column',
  width: 'var(--sidebar-width)',
  minHeight: '0',
  background: t.bgSidebar,
  borderRight: `1px solid ${t.border}`,
});

const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: 'var(--header-height)',
  padding: `0 ${t.s3}`,
  borderBottom: `1px solid ${t.border}`,
  flex: 'none',
  '& strong': { flex: '1', minWidth: '0', fontSize: t.textLg, fontWeight: '600', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
});

const searchBtn = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  width: 'calc(100% - 16px)',
  height: '30px',
  margin: `${t.s2} ${t.s2} ${t.s1}`,
  padding: `0 ${t.s2}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  color: t.textFaint,
  fontSize: t.textMd,
  fontWeight: '400',
  justifyContent: 'flex-start',
  '&:hover': { background: t.bgApp, color: t.textMuted, borderColor: t.borderStrong },
  '& kbd': { marginLeft: 'auto' },
});

const scrollCss = css({ flex: '1', minHeight: '0', overflowY: 'auto', padding: `${t.s1} ${t.s2} ${t.s4}` });

const itemCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: '28px',
  padding: `0 ${t.s2}`,
  borderRadius: t.rMd,
  color: t.textMuted,
  fontSize: t.textMd,
  fontWeight: '450',
  textDecoration: 'none',
  whiteSpace: 'nowrap',
  minWidth: '0',
  '& > .label': { flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& svg': { flex: 'none', opacity: '0.8' },
  '&:hover': { background: t.bgHover, color: t.text, textDecoration: 'none' },
  '&[aria-current="page"]': { background: t.bgActive, color: t.text },
  '&[data-unread="true"]': { color: t.text, fontWeight: '600' },
  '&:focus-visible': { boxShadow: `inset ${t.focus}` },
});

const hashCss = css({ width: '16px', textAlign: 'center', color: t.textFaint, fontWeight: '500', flex: 'none' });

const countCss = css({
  minWidth: '18px',
  height: '18px',
  padding: '0 5px',
  borderRadius: t.rFull,
  background: t.danger,
  color: '#fff',
  fontSize: t.textXs,
  fontWeight: '600',
  lineHeight: '18px',
  textAlign: 'center',
});
const countMuted = css({ background: t.bgActive, color: t.textMuted });
const unreadDot = css({ minWidth: '18px', background: t.accent });

const groupHead = css({
  display: 'flex',
  alignItems: 'center',
  gap: '4px',
  width: '100%',
  height: '26px',
  marginTop: t.s3,
  padding: `0 ${t.s1}`,
  border: '0',
  borderRadius: t.rSm,
  background: 'transparent',
  color: t.textFaint,
  fontSize: t.textXs,
  fontWeight: '600',
  letterSpacing: '0.04em',
  textTransform: 'uppercase',
  justifyContent: 'flex-start',
  '&:hover': { background: 'transparent', color: t.textMuted },
  '& svg': { transition: `transform ${t.fast} ${t.ease}` },
  '&[aria-expanded="false"] svg': { transform: 'rotate(-90deg)' },
});

const listCss = css({ display: 'grid', gap: '1px', margin: '0', padding: '0', listStyle: 'none' });

const footCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: `${t.s2} ${t.s3}`,
  borderTop: `1px solid ${t.border}`,
  color: t.textFaint,
  fontSize: t.textSm,
  flex: 'none',
});
const liveDot = css({ width: '7px', height: '7px', borderRadius: '50%', flex: 'none' });

function NavItem(handle: Handle<{ href: string; current: boolean; icon?: IconNode; lead?: RemixNode; label: RemixNode; badge?: RemixNode; testId?: string; unread?: boolean }>) {
  return () => {
    const { href, current, icon, lead, label, badge, testId, unread } = handle.props;
    return (
      <li>
        <a href={href} data-nav="" aria-current={current ? 'page' : undefined} data-testid={testId} data-unread={unread ? 'true' : undefined} mix={itemCss}>
          {lead ?? (icon ? <Icon icon={icon} size={16} /> : null)}
          <span class="label">{label}</span>
          {badge}
        </a>
      </li>
    );
  };
}

const COLLAPSE_KEY = 'drydock.sidebar.collapsed';
function readCollapsed(): Record<string, boolean> {
  try {
    return JSON.parse(localStorage.getItem(COLLAPSE_KEY) ?? '{}') as Record<string, boolean>;
  } catch {
    return {};
  }
}

export function Sidebar(handle: Handle<{ route: Route }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  // the stream status line follows the event store's snapshot
  let lastStatus = s.store.getSnapshot().status;
  const off = s.store.subscribe(() => {
    const st = s.store.getSnapshot().status;
    if (st === lastStatus) return;
    lastStatus = st;
    void handle.update();
  });
  handle.signal.addEventListener('abort', off);
  let collapsed = readCollapsed();
  let nav: HTMLElement | undefined;

  const toggle = (id: string) => {
    collapsed = { ...collapsed, [id]: !collapsed[id] };
    try {
      localStorage.setItem(COLLAPSE_KEY, JSON.stringify(collapsed));
    } catch {
      /* convenience */
    }
    void handle.update();
  };

  const group = (id: string, title: string, body: RemixNode, action?: RemixNode) => (
    <div>
      <div style={{ display: 'flex', alignItems: 'center' }}>
        <button type="button" mix={[groupHead, on('click', () => toggle(id))]} aria-expanded={!collapsed[id]}>
          <Icon icon={ChevronDown} size={12} />
          <span>{title}</span>
        </button>
        {action}
      </div>
      {collapsed[id] ? null : <ul mix={listCss}>{body}</ul>}
    </div>
  );

  // roving focus: arrow keys move between sidebar links
  const onKey = (e: KeyboardEvent) => {
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key) || !nav) return;
    const items = Array.from(nav.querySelectorAll<HTMLElement>('[data-nav]'));
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

  return () => {
    const { route } = handle.props;
    const project = s.workspace.project;
    const p = s.project;
    const agents = p.agentList;
    const rooms = p.roomList;
    const unread = p.unread.data ?? [];
    const unreadOf = (id: string) => unread.find((u) => u.roomId === id)?.unread ?? 0;
    const at = (...names: Route['name'][]) => names.includes(route.name);
    const roomItem = (r: Room) => {
      const n = unreadOf(r.id);
      const agentId = r.kind === 'dm' ? r.name.slice(r.name.lastIndexOf('+') + 1) : '';
      const label = roomLabel(r, agents);
      return (
        <NavItem
          key={r.id}
          href={routes.room.href({ id: r.id })}
          current={route.name === 'room' && route.id === r.id}
          testId="room-link"
          unread={n > 0}
          lead={
            r.kind === 'dm' ? (
              <Avatar id={agentId} name={label} size={18} status={presenceColor(p.presence.data?.find((x) => x.agentId === agentId))} />
            ) : (
              <span mix={hashCss}>#</span>
            )
          }
          label={r.kind === 'dm' ? label : ` ${label}`}
          badge={n > 0 ? <span mix={[countCss, unreadDot]} title={`${n} unread`}>{n}</span> : null}
        />
      );
    };
    const status = s.store.getSnapshot().status;
    const live = status === 'open';
    const needs = p.needsCount;

    return (
      <nav mix={[sideCss, on<HTMLElement, 'keydown'>('keydown', onKey)]} aria-label="Main">
        <div mix={headCss}>
          <strong title={project?.name}>{project?.name ?? 'Drydock'}</strong>
        </div>
        <button type="button" mix={[searchBtn, on('click', () => s.ui.setPalette(true))]} aria-label="Search and jump (Ctrl+K)">
          <Icon icon={Search} size={14} />
          <span>Search or jump to...</span>
          <kbd>Ctrl K</kbd>
        </button>
        <div
          mix={[
            scrollCss,
            // the element whose links arrow keys move between
            ref((n) => (nav = n)),
          ]}
        >
          {project ? (
            <>
              <ul mix={listCss} style={{ marginTop: '4px' }}>
                <NavItem
                  href={routes.needsYou.href()}
                  current={at('needs-you')}
                  testId="nav-needs-you"
                  icon={Inbox}
                  label="Needs you"
                  badge={
                    <span mix={[countCss, needs > 0 ? undefined : countMuted]}>
                      <span data-testid="needs-count" aria-label={`${needs} items need you`}>
                        {needs}
                      </span>
                    </span>
                  }
                />
                <NavItem href={routes.tasks.href()} current={at('tasks', 'task', 'task-new', 'run')} testId="nav-tasks" icon={ClipboardList} label="Tasks" />
                <NavItem href={routes.pulls.href()} current={at('pulls')} testId="nav-pulls" icon={GitPullRequest} label="Pull Requests" />
                <NavItem href={routes.plan.href()} current={at('plan')} testId="nav-plan" icon={Sparkles} label="Plan" />
                <NavItem href={routes.team.href()} current={at('team')} testId="nav-team" icon={Users} label="Team" />
                <NavItem href={routes.activity.href()} current={at('activity')} testId="nav-activity" icon={Activity} label="Activity" />
              </ul>
              {group('channels', 'Channels', rooms.filter((r) => r.kind === 'channel').map(roomItem))}
              {rooms.some((r) => r.kind === 'task') ? group('task-channels', 'Task channels', rooms.filter((r) => r.kind === 'task').map(roomItem)) : null}
              {rooms.some((r) => r.kind === 'dm') ? group('dms', 'Direct messages', rooms.filter((r) => r.kind === 'dm').map(roomItem)) : null}
              {group(
                'project',
                'Project',
                <>
                  <NavItem href={routes.milestones.href()} current={at('milestones')} testId="nav-milestones" icon={Flag} label="Milestones" />
                  <NavItem href={routes.budgets.href()} current={at('budgets')} testId="nav-budgets" icon={PiggyBank} label="Budgets" />
                  <NavItem href={routes.evals.href()} current={at('evals')} testId="nav-evals" icon={Gauge} label="Evals report" />
                  <NavItem href={routes.mcp.href()} current={at('mcp')} testId="nav-mcp" icon={Server} label="MCP servers" />
                  <NavItem href={routes.project.href()} current={at('project')} testId="nav-project" icon={Settings2} label="Project settings" />
                </>,
              )}
              {group(
                'knowledge',
                'Knowledge',
                <>
                  <NavItem href={routes.packs.href()} current={at('packs')} testId="nav-packs" icon={BookOpen} label="Expert packs" />
                  <NavItem href={routes.requirements.href()} current={at('requirements')} testId="nav-requirements" icon={FileText} label="Requirements" />
                  <NavItem href={routes.skills.href()} current={at('skills')} testId="nav-skills" icon={Cpu} label="Skills" />
                  <NavItem href={routes.memory.href()} current={at('memory')} testId="nav-memory" icon={Brain} label="Memory" />
                </>,
              )}
            </>
          ) : null}
          {group(
            'workspace',
            'Workspace',
            <>
              <NavItem href={routes.library.href()} current={at('library')} testId="nav-library" icon={Library} label="Library" />
              <NavItem href={routes.integrations.href()} current={at('integrations')} testId="nav-integrations" icon={Plug} label="Integrations" />
              <NavItem href={routes.connections.href()} current={at('connections')} testId="nav-connections" icon={Link2} label="Connections" />
              <NavItem href={routes.providers.href()} current={at('providers')} testId="nav-providers" icon={Network} label="Providers" />
              <NavItem href={routes.routing.href()} current={at('routing')} testId="nav-routing" icon={RouteIcon} label="Routing" />
              <NavItem href={routes.license.href()} current={at('license')} testId="nav-license" icon={KeyRound} label="License" />
              <NavItem href={routes.backups.href()} current={at('backups')} testId="nav-backups" icon={HardDriveDownload} label="Backups" />
              <NavItem href={routes.data.href()} current={at('data')} testId="nav-data" icon={Database} label="Export and import" />
              <NavItem href={routes.support.href()} current={at('support')} testId="nav-support" icon={LifeBuoy} label="Support bundle" />
              <NavItem href={routes.settings.href()} current={at('settings')} testId="nav-settings" icon={Map} label="Settings and About" />
            </>,
          )}
          {agents.length > 0 && project
            ? group(
                'agents',
                'Agents',
                agents.map((a) => {
                  const pr = p.presence.data?.find((x) => x.agentId === a.id);
                  return (
                    <li key={a.id}>
                      <button
                        type="button"
                        data-nav=""
                        mix={[itemCss, agentBtn, on('click', () => s.ui.openInspector(a.id))]}
                        aria-label={`Inspect ${a.name}`}
                        title={pr?.activity ?? pr?.presence ?? ''}
                      >
                        <Avatar id={a.id} name={a.name} size={18} status={presenceColor(pr)} />
                        <span class="label">{a.name}</span>
                        <span style={{ color: 'var(--text-faint)', fontSize: 'var(--text-xs)' }}>{a.role}</span>
                      </button>
                    </li>
                  );
                }),
              )
            : null}
        </div>
        <div mix={footCss} data-testid="stream" data-state={status}>
          <span mix={liveDot} aria-hidden="true" style={{ background: live ? 'var(--success)' : status === 'reconnecting' || status === 'connecting' ? 'var(--warning)' : 'var(--text-faint)' }} />
          <span>{live ? 'Live' : `Event stream: ${status}`}</span>
          <span style={{ marginLeft: 'auto', display: 'inline-flex' }} title={`Event stream: ${status}`}>
            <Icon icon={live ? CircleDot : MessagesSquare} size={12} />
          </span>
          <span class="sr-only">Event stream: {status}</span>
        </div>
      </nav>
    );
  };
}

const agentBtn = css({ width: '100%', border: '0', background: 'transparent', justifyContent: 'flex-start', minHeight: '28px', fontWeight: '450' });
