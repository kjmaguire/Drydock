// The top bar (Crew HQ, decision P64): the Drydock mark, the project switcher, the five tabs of a project (Overview, Tasks,
// Rooms, Crew, Knowledge) or the workspace's (Home, Chats, Library, Security scan), then search (Ctrl+K), the bell (Needs
// you, with its count), the gear (Settings) and the event stream's state. It is the app's main navigation landmark; a hub
// with sub-pages shows them under it (hubnav.tsx).

import { css, on, ref, type Handle } from 'remix/component';
import { Bell, BookOpen, ClipboardList, House, Library, MessagesSquare, Search, Settings, ShieldCheck, Users, type IconNode } from 'lucide';
import { hubOf, isWorkspaceHub, type Hub, type Route } from '../lib/route.ts';
import { routes } from '../routes.ts';
import { services, watch } from '../state/services.tsx';
import { Icon } from '../ui/icon.tsx';
import { t } from '../ui/tokens.ts';
import { ProjectSwitcher } from './project-switcher.tsx';

const barCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  flex: 'none',
  height: 'var(--topbar-height)',
  padding: `0 ${t.s5}`,
  borderBottom: `1px solid ${t.border}`,
  background: t.bgApp,
});
const navCss = css({ display: 'flex', alignItems: 'center', gap: t.s3, width: '100%', minWidth: '0' });

// the Drydock mark: a monochrome dot with the agents' eyes
const logoLink = css({ display: 'inline-flex', flex: 'none', borderRadius: t.rFull, '&:focus-visible': { boxShadow: t.focus, outline: 'none' } });
const logoCss = css({
  position: 'relative',
  display: 'inline-grid',
  placeItems: 'center',
  width: '26px',
  height: '26px',
  borderRadius: '50%',
  background: t.accent,
  color: t.onAccent,
  '& > span': { display: 'flex', gap: '3px', paddingBottom: '1px' },
  '& i': { display: 'block', width: '4px', height: '7px', borderRadius: '1.5px', background: 'currentColor' },
});

const tabsCss = css({ display: 'flex', alignItems: 'center', gap: '2px', margin: `0 0 0 ${t.s2}`, padding: '0', listStyle: 'none', minWidth: '0', overflow: 'hidden' });
const tabCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '7px',
  height: '36px',
  padding: `0 ${t.s4}`,
  borderRadius: t.rFull,
  color: t.textMuted,
  fontSize: t.textMd,
  fontWeight: '500',
  textDecoration: 'none',
  whiteSpace: 'nowrap',
  transition: `background-color ${t.fast} ${t.ease}, color ${t.fast} ${t.ease}`,
  '& svg': { opacity: '0.85' },
  '&:hover': { background: t.bgHover, color: t.text, textDecoration: 'none' },
  '&[aria-current="page"]': { background: t.accent, color: t.onAccent },
  '&[aria-current="page"] svg': { opacity: '1' },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
});

const rightCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, marginLeft: 'auto', flex: 'none' });
const searchCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: t.s2,
  width: '236px',
  height: '38px',
  padding: `0 ${t.s2} 0 ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rFull,
  background: t.bgSurface,
  color: t.textFaint,
  fontSize: t.textSm,
  fontWeight: '400',
  justifyContent: 'flex-start',
  '& > span': { flex: '1', textAlign: 'left', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& kbd': { fontSize: t.textXs },
  '&:hover': { borderColor: t.borderStrong, color: t.textMuted },
  '@media (max-width: 1100px)': { width: '38px', padding: '0', justifyContent: 'center', '& > span, & kbd': { display: 'none' } },
});
const iconLink = css({
  position: 'relative',
  display: 'inline-grid',
  placeItems: 'center',
  width: '38px',
  height: '38px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rFull,
  background: t.bgSurface,
  color: t.textMuted,
  textDecoration: 'none',
  transition: `background-color ${t.fast} ${t.ease}, color ${t.fast} ${t.ease}`,
  '&:hover': { background: t.bgHover, color: t.text, textDecoration: 'none' },
  '&[aria-current="page"]': { background: t.accent, color: t.onAccent, borderColor: t.accent },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
});
// the count on the bell: a red pill while something waits, a quiet number otherwise (the e2e suite reads it either way)
const countCss = css({
  position: 'absolute',
  top: '-5px',
  right: '-5px',
  minWidth: '18px',
  height: '18px',
  padding: '0 5px',
  borderRadius: t.rFull,
  border: `2px solid ${t.bgApp}`,
  background: t.dangerSolid,
  color: t.onAccent,
  fontSize: t.textXs,
  fontWeight: '700',
  lineHeight: '14px',
  textAlign: 'center',
  fontVariantNumeric: 'tabular-nums',
  '&[data-zero="true"]': { background: t.bgActive, color: t.textFaint, fontWeight: '600' },
});
const streamCss = css({ display: 'inline-grid', placeItems: 'center', width: '22px', height: '38px', flex: 'none', '& > span': { width: '8px', height: '8px', borderRadius: '50%' } });

interface Tab {
  id: string;
  label: string;
  icon: IconNode;
  href: string;
  hub: Hub;
}

const PROJECT_TABS: Tab[] = [
  { id: 'nav-overview', label: 'Overview', icon: House, href: routes.home.href(), hub: 'overview' },
  { id: 'nav-tasks', label: 'Tasks', icon: ClipboardList, href: routes.tasks.href(), hub: 'tasks' },
  { id: 'nav-rooms', label: 'Rooms', icon: MessagesSquare, href: routes.rooms.href(), hub: 'rooms' },
  { id: 'nav-team', label: 'Crew', icon: Users, href: routes.team.href(), hub: 'crew' },
  { id: 'nav-knowledge', label: 'Knowledge', icon: BookOpen, href: routes.knowledge.href(), hub: 'knowledge' },
];
const WORKSPACE_TABS: Tab[] = [
  { id: 'nav-workspace', label: 'Home', icon: House, href: routes.workspace.href(), hub: 'home' },
  { id: 'nav-chats', label: 'Chats', icon: MessagesSquare, href: routes.chats.href(), hub: 'chats' },
  { id: 'nav-library', label: 'Library', icon: Library, href: routes.library.href(), hub: 'library' },
  { id: 'nav-security-scan', label: 'Security scan', icon: ShieldCheck, href: routes.securityScan.href(), hub: 'scan' },
];

/** Whether the bar shows the workspace's tabs: no project is open, or the page is the workspace's own. */
export function workspaceBar(route: Route, hasProject: boolean): boolean {
  return !hasProject || isWorkspaceHub(hubOf(route));
}

export function TopBar(handle: Handle<{ route: Route }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  // the stream status dot follows the event store's snapshot
  let lastStatus = s.store.getSnapshot().status;
  const off = s.store.subscribe(() => {
    const st = s.store.getSnapshot().status;
    if (st === lastStatus) return;
    lastStatus = st;
    void handle.update();
  });
  handle.signal.addEventListener('abort', off);
  let nav: HTMLElement | undefined;

  // roving focus: the arrow keys move between the bar's links and buttons (the switcher's menu keeps its own keys)
  const onKey = (e: KeyboardEvent) => {
    if (!['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(e.key) || !nav) return;
    if (!(e.target instanceof Node) || !nav.contains(e.target)) return;
    if ((e.target as HTMLElement).getAttribute?.('aria-label') === 'Switch project') return;
    const items = Array.from(nav.querySelectorAll<HTMLElement>('[data-nav]'));
    if (items.length === 0) return;
    const i = items.indexOf(document.activeElement as HTMLElement);
    let n = i;
    if (e.key === 'ArrowRight') n = Math.min(items.length - 1, i + 1);
    else if (e.key === 'ArrowLeft') n = Math.max(0, i - 1);
    else if (e.key === 'Home') n = 0;
    else n = items.length - 1;
    e.preventDefault();
    items[n]?.focus();
  };

  return () => {
    const { route } = handle.props;
    const project = s.workspace.project;
    const hub = hubOf(route);
    const workspace = workspaceBar(route, !!project);
    const tabs = workspace ? WORKSPACE_TABS : PROJECT_TABS;
    const status = s.store.getSnapshot().status;
    const live = status === 'open';
    const needs = s.project.needsCount;
    return (
      <header mix={barCss}>
        <nav aria-label="Main" mix={[navCss, on<HTMLElement, 'keydown'>('keydown', onKey), ref((n) => (nav = n))]}>
          {/* on a project's page the mark is the way home; on a workspace page the Home tab is, and the mark only decorates */}
          <a href={routes.workspace.href()} mix={logoLink} aria-label="Drydock home" title="Home" data-nav="" data-testid={workspace ? undefined : 'nav-workspace'}>
            <span mix={logoCss} role="img" aria-label="Drydock">
              <span aria-hidden="true">
                <i />
                <i />
              </span>
            </span>
          </a>
          <ProjectSwitcher workspace={workspace} />
          <ul mix={tabsCss} role="list">
            {tabs.map((tab) => (
              <li key={tab.id}>
                <a href={tab.href} mix={tabCss} data-nav="" data-testid={tab.id} aria-current={hub === tab.hub ? 'page' : undefined}>
                  <Icon icon={tab.icon} size={16} />
                  <span>{tab.label}</span>
                </a>
              </li>
            ))}
          </ul>
          <div mix={rightCss}>
            <button type="button" mix={[searchCss, on('click', () => s.ui.setPalette(true))]} data-nav="" aria-label="Search and jump (Ctrl+K)">
              <Icon icon={Search} size={15} />
              <span>Search or ask anything</span>
              <kbd>Ctrl K</kbd>
            </button>
            {!workspace ? (
              <a
                href={routes.needsYou.href()}
                mix={iconLink}
                data-nav=""
                data-testid="nav-needs-you"
                aria-label={`Needs you: ${needs} ${needs === 1 ? 'item' : 'items'}`}
                title="Needs you"
                aria-current={route.name === 'needs-you' ? 'page' : undefined}
              >
                <Icon icon={Bell} size={17} />
                <span mix={countCss} data-testid="needs-count" data-zero={needs === 0 ? 'true' : undefined} aria-label={`${needs} items need you`}>
                  {needs}
                </span>
              </a>
            ) : null}
            <a href={routes.settings.href()} mix={iconLink} data-nav="" data-testid="nav-settings" aria-label="Settings" title="Settings" aria-current={hub === 'settings' ? 'page' : undefined}>
              <Icon icon={Settings} size={17} />
            </a>
            <span mix={streamCss} data-testid="stream" data-state={status} title={`Event stream: ${status}`}>
              <span aria-hidden="true" style={{ background: live ? t.success : status === 'reconnecting' || status === 'connecting' ? t.warning : t.textFaint }} />
              <span class="sr-only">Event stream: {status}</span>
            </span>
          </div>
        </nav>
      </header>
    );
  };
}
