// The sidebar (graphite, decision P97; six places since P140): the app's main navigation landmark, on the left of every page.
// It folds to a 56px rail of icons (the toggle, or "[" outside a text field; a narrow window starts folded) and remembers the
// choice. From the top: the Drydock mark with search (Ctrl+K) and the fold, New (Ctrl+N: shell/new.ts says what it opens), Home,
// the Inbox (what waits on the person in every project; Running and My tasks are its tabs), Chats, the projects (the open one
// shows its tabs under it), and at the foot Agents (the library), Settings (every page that configures the workspace or the app
// is one of its sections), the local engine's state and the event stream's. Every other page is in Settings, in Agents or in
// the command menu. The test ids are the ones the links had, so the e2e helpers find the same links (e2e/helpers.ts).

import { css, navigate, on, type Handle } from 'remix/component';
import {
  BookOpen,
  Bot,
  ClipboardList,
  House,
  Inbox,
  LayoutDashboard,
  MessagesSquare,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Search,
  Settings,
  Users,
  type IconNode,
} from 'lucide';
import type { LocalView } from '../api/localClient.ts';
import { comboFor, ownsKeys, shellKey } from '../lib/keys.ts';
import { hubOf, isWorkspaceHub, type Hub, type Route } from '../lib/route.ts';
import { routes } from '../routes.ts';
import { query, services, watch } from '../state/services.tsx';
import { hueFor } from '../ui/data.tsx';
import { Icon } from '../ui/icon.tsx';
import { t } from '../ui/tokens.ts';
import { focusStart, isNewKey, newHref, openNew } from './new.ts';

const asideCss = css({
  display: 'flex',
  flexDirection: 'column',
  flex: 'none',
  width: 'var(--sidebar-width)',
  minHeight: '0',
  borderRight: `1px solid ${t.border}`,
  background: t.bgSidebar,
  '&[data-rail="true"]': { width: 'var(--rail-width)' },
  // a phone-sized window keeps the rail slimmer, so a conversation keeps its width
  '@media (max-width: 719px)': { '&[data-rail="true"]': { width: '44px' } },
});
const navCss = css({ display: 'flex', flexDirection: 'column', flex: '1', minHeight: '0' });
const scrollCss = css({
  display: 'flex',
  flexDirection: 'column',
  gap: '2px',
  flex: '1',
  minHeight: '0',
  overflowY: 'auto',
  overflowX: 'hidden',
  padding: `${t.s3} 10px`,
  ':is([data-rail="true"]) &': { padding: `${t.s3} 6px` },
});
const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: `2px 2px ${t.s2}`,
  ':is([data-rail="true"]) &': { flexDirection: 'column', padding: `2px 0 ${t.s2}` },
});
// the Drydock mark: a dock on the accent's square
const markCss = css({
  display: 'inline-grid',
  placeItems: 'center',
  flex: 'none',
  width: '26px',
  height: '26px',
  borderRadius: t.rMd,
  background: t.accent,
  color: t.onAccent,
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
});
// the name beside the mark; the rail shows the mark alone
const brandCss = css({ flex: '1', minWidth: '0', fontSize: t.textLg, fontWeight: '600', color: t.text, whiteSpace: 'nowrap', ':is([data-rail="true"]) &': { display: 'none' } });
const foldCss = css({
  display: 'inline-grid',
  placeItems: 'center',
  flex: 'none',
  width: '28px',
  height: '28px',
  minHeight: '0',
  padding: '0',
  border: '0',
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.textFaint,
  '&:hover': { background: t.bgHover, color: t.text },
});
// New: the one way to start something (decision P140), with its key
const newCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: '32px',
  margin: `0 2px ${t.s2}`,
  padding: `0 ${t.s2} 0 10px`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  color: t.text,
  fontSize: t.textMd,
  fontWeight: '500',
  justifyContent: 'flex-start',
  '& > .lbl': { flex: '1', textAlign: 'left', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '&:hover': { borderColor: t.borderStrong, background: t.bgHover },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
  ':is([data-rail="true"]) &': { width: '36px', margin: `0 auto ${t.s2}`, padding: '0', justifyContent: 'center', '& > .lbl, & > kbd': { display: 'none' } },
});
const groupHead = css({
  padding: `${t.s4} 10px 6px`,
  color: t.textFaint,
  fontSize: t.textXs,
  fontWeight: '500',
  letterSpacing: '0.04em',
  textTransform: 'uppercase',
  ':is([data-rail="true"]) &': { padding: `${t.s3} 0 ${t.s1}`, textAlign: 'center', '& > span': { display: 'none' }, '&::after': { content: '""', display: 'block', height: '1px', margin: '0 10px', background: t.border } },
});
const listCss = css({ display: 'grid', gap: '2px', margin: '0', padding: '0', listStyle: 'none' });
const itemCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: '10px',
  height: '30px',
  padding: '0 10px',
  borderRadius: t.rMd,
  color: t.textMuted,
  fontSize: t.textMd,
  textDecoration: 'none',
  whiteSpace: 'nowrap',
  '& > .lbl': { flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& svg': { flex: 'none' },
  '&:hover': { background: t.bgHover, color: t.text, textDecoration: 'none' },
  '&[aria-current="page"]': { background: t.bgActive, color: t.text },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
  ':is([data-rail="true"]) &': { justifyContent: 'center', padding: '0', width: '36px', height: '36px', margin: '0 auto', '& > .lbl, & > .count': { display: 'none' } },
});
// a project's own tabs, under the open project
const subItemCss = css({ paddingLeft: '30px', height: '28px', ':is([data-rail="true"]) &': { paddingLeft: '0' } });
const squareCss = css({ display: 'inline-block', flex: 'none', width: '9px', height: '9px', borderRadius: '3px' });
const countCss = css({ flex: 'none', color: t.textFaint, fontSize: t.textSm, fontVariantNumeric: 'tabular-nums' });
// the Inbox count: amber while something waits on the person, a quiet number otherwise (the e2e suite reads it either way)
const needsCss = css({
  flex: 'none',
  minWidth: '20px',
  height: '18px',
  padding: '0 6px',
  borderRadius: t.rFull,
  background: t.warning,
  color: t.onAccent,
  fontSize: t.textXs,
  fontWeight: '600',
  lineHeight: '18px',
  textAlign: 'center',
  fontVariantNumeric: 'tabular-nums',
  '&[data-zero="true"]': { background: 'transparent', color: t.textFaint, fontWeight: '500' },
  ':is([data-rail="true"]) &': { position: 'absolute', top: '1px', right: '1px', minWidth: '16px', height: '16px', padding: '0 4px', lineHeight: '16px', '&[data-zero="true"]': { display: 'none' } },
});
const footCss = css({ display: 'grid', gap: '2px', flex: 'none', padding: `${t.s2} 10px ${t.s3}`, borderTop: `1px solid ${t.border}` });
// the local engine's state, kept to one quiet line (its build is in the title)
const engineCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: '24px',
  margin: `${t.s1} 0 0`,
  padding: '0 10px',
  borderRadius: t.rMd,
  color: t.textFaint,
  fontSize: t.textSm,
  textDecoration: 'none',
  whiteSpace: 'nowrap',
  '& > .lbl': { overflow: 'hidden', textOverflow: 'ellipsis' },
  '&:hover': { background: t.bgHover, color: t.text, textDecoration: 'none' },
  ':is([data-rail="true"]) &': { display: 'none' },
});
const dotCss = css({ display: 'inline-block', flex: 'none', width: '7px', height: '7px', borderRadius: '50%' });
const streamCss = css({ display: 'inline-flex', alignItems: 'center', gap: t.s2, height: '24px', padding: '0 10px', color: t.textFaint, fontSize: t.textSm, ':is([data-rail="true"]) &': { justifyContent: 'center', padding: '0', '& > .lbl': { display: 'none' } } });

interface Item {
  id: string;
  label: string;
  icon: IconNode;
  href: string;
  current: boolean;
}

/** Whether the sidebar shows the workspace's view: no project is open, or the page is the workspace's own. */
export function workspaceView(route: Route, hasProject: boolean): boolean {
  return !hasProject || isWorkspaceHub(hubOf(route));
}

/** The engine card's words: whether the local model server runs, and which build it is ("TurboQuant build b10298 · Vulkan"). */
export function engineWords(v: LocalView | null | undefined): { running: boolean; title: string; sub: string } | undefined {
  if (!v?.supported || !v.runtime?.installed) return undefined;
  const r = v.runtime;
  const backend = v.backends?.find((b) => b.id === r.backend)?.label ?? r.backend ?? '';
  const build = [`${r.sourceName || 'Model engine'} ${(r.tag ?? '').split('-')[0]}`.trim(), backend].filter(Boolean).join(' · ');
  const running = v.server?.state === 'running';
  return { running, title: running ? 'Local engine running' : 'Local engine stopped', sub: build };
}

/** `onNavigate` replaces the router's navigate for New's key (tests). */
export function Sidebar(handle: Handle<{ route: Route; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  watch(handle, s.ui);
  // the stream's state follows the event store's snapshot
  let lastStatus = s.store.getSnapshot().status;
  const off = s.store.subscribe(() => {
    const st = s.store.getSnapshot().status;
    if (st === lastStatus) return;
    lastStatus = st;
    void handle.update();
  });
  handle.signal.addEventListener('abort', off);
  const engine = query(handle, { load: () => s.client.localGet(), when: (e) => e.type === 'local.changed' });

  // "[" folds and unfolds the sidebar, as in Linear; never while typing or inside a widget that owns its keys. Ctrl+B does too
  // (decision P129), also from a field, since it types nothing there; never inside the terminal or the editor
  window.addEventListener(
    'keydown',
    (e) => {
      if (shellKey(e) === 'sidebar') {
        e.preventDefault();
        s.ui.toggleSidebar();
        return;
      }
      if (e.key !== '[' || e.ctrlKey || e.metaKey || e.altKey || ownsKeys(e.target)) return;
      const el = e.target as HTMLElement | null;
      if (el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))) return;
      e.preventDefault();
      s.ui.toggleSidebar();
    },
    { signal: handle.signal },
  );

  // Ctrl+N is New everywhere but Chats, whose screen makes a new chat with it (decision P140)
  let onChats = false;
  window.addEventListener(
    'keydown',
    (e) => {
      if (onChats || !isNewKey(e)) return;
      e.preventDefault();
      openNew(handle.props.onNavigate ?? navigate);
    },
    { signal: handle.signal },
  );

  // roving focus: the arrow keys move up and down the sidebar's links (the switcher's menu keeps its own keys)
  const onKey = (e: KeyboardEvent) => {
    if (e.altKey || !['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key)) return; // Alt+arrows go to another room or chat
    const nav = e.currentTarget as HTMLElement;
    if (!(e.target instanceof HTMLElement) || !e.target.hasAttribute('data-nav')) return;
    const items = Array.from(nav.querySelectorAll<HTMLElement>('[data-nav]')).filter((n) => n.offsetParent !== null);
    const i = items.indexOf(e.target);
    if (i < 0) return;
    const n = e.key === 'ArrowDown' ? Math.min(items.length - 1, i + 1) : e.key === 'ArrowUp' ? Math.max(0, i - 1) : e.key === 'Home' ? 0 : items.length - 1;
    e.preventDefault();
    items[n]?.focus();
  };

  const link = (it: Item) => (
    <li key={it.id}>
      <a href={it.href} mix={itemCss} data-nav="" data-testid={it.id} title={it.label} aria-current={it.current ? 'page' : undefined}>
        <Icon icon={it.icon} size={15} />
        <span class="lbl">{it.label}</span>
      </a>
    </li>
  );

  return () => {
    const { route } = handle.props;
    const rail = s.ui.sidebarRail;
    const ws = s.workspace;
    const project = ws.project;
    const hub = hubOf(route);
    onChats = hub === 'chats';
    const workspace = workspaceView(route, !!project);
    // the Inbox counts every project's (decision P97); Running and My tasks are its tabs (P140)
    const needs = ws.needsCount;
    const inbox = route.name === 'needs-you' || route.name === 'my-tasks';
    const status = s.store.getSnapshot().status;
    const live = status === 'open';
    const eng = engineWords(engine.data);
    const is = (h: Hub) => hub === h;

    const projectTabs: Item[] = [
      { id: 'nav-overview', label: 'Overview', icon: LayoutDashboard, href: routes.home.href(), current: is('overview') },
      { id: 'nav-tasks', label: 'Tasks', icon: ClipboardList, href: routes.tasks.href(), current: is('tasks') },
      { id: 'nav-rooms', label: 'Rooms', icon: MessagesSquare, href: routes.rooms.href(), current: is('rooms') },
      { id: 'nav-team', label: 'Crew', icon: Users, href: routes.team.href(), current: is('crew') },
      { id: 'nav-knowledge', label: 'Knowledge', icon: BookOpen, href: routes.knowledge.href(), current: is('knowledge') },
    ];

    return (
      <aside mix={asideCss} data-rail={rail ? 'true' : undefined} data-testid="sidebar" data-region="sidebar">
        <nav aria-label="Main" mix={[navCss, on<HTMLElement, 'keydown'>('keydown', onKey)]}>
          <div mix={scrollCss}>
          <div mix={headCss}>
            <span mix={markCss} aria-hidden="true">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" aria-hidden="true">
                <path d="M3 17h18M6 17V8h12v9M9 8V5h6v3" />
              </svg>
            </span>
            <span mix={brandCss} data-testid="brand">
              Drydock
            </span>
            <button
              type="button"
              mix={[foldCss, on('click', () => s.ui.setPalette(true))]}
              aria-label={`Search and jump (${comboFor('palette')})`}
              title={`Search or run a command (${comboFor('palette')})`}
              data-testid="sidebar-search"
            >
              <Icon icon={Search} size={15} />
            </button>
            <button
              type="button"
              mix={[foldCss, on('click', () => s.ui.toggleSidebar())]}
              aria-label={rail ? 'Expand the sidebar ([)' : 'Collapse the sidebar ([)'}
              aria-expanded={rail ? 'false' : 'true'}
              title={rail ? 'Expand the sidebar ([)' : 'Collapse the sidebar ([)'}
              data-testid="sidebar-fold"
            >
              <Icon icon={rail ? PanelLeftOpen : PanelLeftClose} size={15} />
            </button>
          </div>
          <a
            href={newHref()}
            mix={[newCss, on('click', () => void requestAnimationFrame(() => focusStart()))]}
            data-nav=""
            data-testid="nav-new"
            title={`New (${comboFor('new-chat')})`}
            aria-keyshortcuts={comboFor('new-chat').replace('Ctrl', 'Control')}
            aria-current={route.name === 'project-new' ? 'page' : undefined}
          >
            <Icon icon={Plus} size={15} />
            <span class="lbl">New</span>
            <kbd>{comboFor('new-chat').split('+').join(' ')}</kbd>
          </a>

          <ul mix={listCss} role="list">
            {link({ id: 'nav-workspace', label: 'Home', icon: House, href: routes.workspace.href(), current: route.name === 'workspace' })}
            <li style={{ position: 'relative' }}>
              <a
                href={routes.needsYou.href()}
                mix={[itemCss, on('click', () => s.ui.setInboxTab('needs'))]}
                data-nav=""
                data-testid="nav-needs-you"
                aria-label={`Inbox: ${needs} ${needs === 1 ? 'item needs' : 'items need'} you`}
                title="Inbox"
                aria-current={inbox ? 'page' : undefined}
              >
                <Icon icon={Inbox} size={15} />
                <span class="lbl">Inbox</span>
                <span mix={needsCss} data-testid="needs-count" data-zero={needs === 0 ? 'true' : undefined} aria-label={`${needs} items need you`}>
                  {needs}
                </span>
              </a>
            </li>
            {link({ id: 'nav-chats', label: 'Chats', icon: MessagesSquare, href: routes.chats.href(), current: is('chats') })}
          </ul>

          <div mix={groupHead}>
            <span>Projects</span>
          </div>
          <ul mix={listCss} role="list" aria-label="Projects">
            {ws.projectList.map((p) => {
              const open = !workspace && p.id === project?.id;
              return (
                <li key={p.id}>
                  <a
                    href={routes.home.href()}
                    mix={[itemCss, on('click', (e) => {
                      if (p.id !== ws.projectId) s.workspace.selectProject(p.id);
                      if (open) e.preventDefault();
                    })]}
                    title={p.name}
                    data-nav=""
                    data-testid="nav-project-item"
                    data-project={p.id}
                    aria-current={open && route.name === 'home' ? 'page' : undefined}
                    aria-expanded={open ? 'true' : 'false'}
                  >
                    <span mix={squareCss} aria-hidden="true" style={{ background: `hsl(${hueFor(p.id)} 62% 62%)` }} />
                    <span class="lbl">{p.name}</span>
                    {open && s.project.taskTotal ? <span class="count" mix={countCss}>{s.project.taskTotal}</span> : null}
                  </a>
                  {open ? (
                    <ul mix={listCss} role="list" aria-label={`${p.name} pages`}>
                      {projectTabs.map((it) => (
                        <li key={it.id}>
                          <a href={it.href} mix={[itemCss, subItemCss]} data-nav="" data-testid={it.id} title={it.label} aria-current={it.current ? 'page' : undefined}>
                            <Icon icon={it.icon} size={14} />
                            <span class="lbl">{it.label}</span>
                          </a>
                        </li>
                      ))}
                    </ul>
                  ) : null}
                </li>
              );
            })}
          </ul>
          </div>
        <div mix={footCss}>
          <ul mix={listCss} role="list" aria-label="App">
            {/* one Agents library (decision P140): My agents, Starters, Skills and every project's crew */}
            {link({ id: 'nav-library', label: 'Agents', icon: Bot, href: routes.library.href(), current: is('library') })}
            {link({ id: 'nav-settings', label: 'Settings', icon: Settings, href: routes.settings.href(), current: is('settings') })}
          </ul>
          {eng ? (
            <a href={routes.models.href()} mix={engineCss} data-testid="engine-card" title={`${eng.title}: ${eng.sub}`}>
              <span mix={dotCss} aria-hidden="true" style={{ background: eng.running ? t.success : t.textFaint }} />
              <span class="lbl">{eng.title}</span>
            </a>
          ) : null}
          <span mix={streamCss} data-testid="stream" data-state={status} title={`Event stream: ${status}`}>
            <span mix={dotCss} aria-hidden="true" style={{ background: live ? t.success : status === 'reconnecting' || status === 'connecting' ? t.warning : t.textFaint }} />
            <span class="lbl">{live ? 'Connected' : status === 'reconnecting' || status === 'connecting' ? 'Connecting...' : 'Not connected'}</span>
            <span class="sr-only">Event stream: {status}</span>
          </span>
        </div>
        </nav>
      </aside>
    );
  };
}
