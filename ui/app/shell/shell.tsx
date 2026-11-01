// The application shell (graphite, decision P97). The SPA router renders every route's content inside it (render transform
// in router.tsx), and the same Shell instance stays mounted across navigations, so the sidebar (with the project switcher),
// the dock (and a running terminal) and the details panel keep their state while the center changes. Beside the sidebar a
// hub's pills (Tasks, Knowledge...), then the page, with the Rooms rail or the Settings rail beside it when the hub has one.

import { css, navigate, on, type Handle, type RemixNode } from 'remix/component';
import { PanelLeftOpen, PlugZap } from 'lucide';
import { projectHasRepo } from '../api/types.ts';
import { ownsKeys, shellKey, shortcutOf } from '../lib/keys.ts';
import { hubOf, isWorkspacePage, isWorkspaceRoute, parseRoute, type Route } from '../lib/route.ts';
import { routes } from '../routes.ts';
import { WorkspaceHomeScreen } from '../screens/workspace/workspace-home.tsx';
import { query, services, watch } from '../state/services.tsx';
import { Button } from '../ui/button.tsx';
import { ErrorBoundary } from '../ui/error-boundary.tsx';
import { ErrorNote, Loading } from '../ui/feedback.tsx';
import { Page } from '../ui/layout.tsx';
import { t } from '../ui/tokens.ts';
import { Details } from './details.tsx';
import { Dock } from './dock.tsx';
import { RoomsRail, SettingsRail, SubNav } from './hubnav.tsx';
import { LicenseBanner } from './license-banner.tsx';
import { TermsNotice } from './terms-notice.tsx';
import { AgentConfigBanner } from './agent-config-banner.tsx';
import { RepoConfigBanner } from './repo-config-banner.tsx';
import { CommandPalette } from './palette.tsx';
import { chatOrder, focusRegion, neighbour, REGION_OF, roomOrder } from './regions.ts';
import { Sidebar } from './sidebar.tsx';

const rootCss = css({ display: 'flex', height: '100vh', overflow: 'hidden', background: t.bgApp });
const columnCss = css({ display: 'flex', flexDirection: 'column', flex: '1', minWidth: '0', minHeight: '0' });
const appCss = css({ position: 'relative', display: 'flex', flex: '1', minHeight: '0', minWidth: '0' });
const centerCss = css({ display: 'flex', flexDirection: 'column', flex: '1', minWidth: '0', minHeight: '0', background: t.bgApp });
const mainCss = css({ flex: '1', minHeight: '0', overflow: 'hidden', outline: 'none', '& > *': { height: '100%' } });
const skipCss = css({
  position: 'absolute',
  left: '-9999px',
  top: t.s2,
  zIndex: '10',
  padding: `${t.s2} ${t.s3}`,
  borderRadius: t.rFull,
  background: t.accent,
  color: t.onAccent,
  '&:focus': { left: t.s2 },
});
const skipNav = css({ position: 'absolute', top: '0', left: '0', zIndex: '10' });
// a narrow window's rail is a drawer: a slim bar with the button that opens it, and a scrim that closes it
const drawerBar = css({ display: 'flex', alignItems: 'center', gap: t.s2, flex: 'none', padding: `${t.s1} ${t.s3}`, borderBottom: `1px solid ${t.border}` });
const scrimCss = css({ position: 'absolute', inset: '0', zIndex: '29', border: '0', borderRadius: '0', padding: '0', minHeight: '0', background: 'color-mix(in srgb, black 25%, transparent)', cursor: 'default' });

/** `onNavigate` replaces the router's navigate for the shell's own keys (tests). */
export function Shell(handle: Handle<{ url: URL; children?: RemixNode; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  watch(handle, s.ui); // the narrow window's drawer
  let dmError: Error | undefined;
  /** The dock acts on a project: it is there only on a project's pages (UI review P1-8), and so are its shortcuts. */
  let dockHere = false;
  /** The agent whose page or direct message is open: Ctrl+. opens the Agent Inspector for it (decisions P129, P130). */
  let agentHere: string | undefined;
  /** The page open: Alt+Up and Alt+Down move from its room or chat (decision P132). */
  let here: Route = { name: 'not-found' };
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href));

  // Ctrl+J toggles the dock, Ctrl+Shift+D opens the diff, Ctrl+` shows or hides its terminal (decision P132; or the keys the
  // person bound them to), Ctrl+. inspects the agent of the page; not inside the terminal or the
  // diff editor, which use them
  window.addEventListener(
    'keydown',
    (e) => {
      if (shellKey(e) === 'inspect' && agentHere) {
        e.preventDefault();
        s.ui.openInspector(agentHere);
        return;
      }
      if (ownsKeys(e.target) || !dockHere) return;
      const id = shortcutOf(e);
      if (id === 'dock') {
        e.preventDefault();
        s.ui.toggleDock();
      } else if (id === 'diff') {
        e.preventDefault();
        s.ui.openDock('diff');
      } else if (id === 'terminal') {
        e.preventDefault();
        s.ui.toggleTerminal();
        if (s.ui.dock.open) handle.queueTask(() => focusRegion('dock'));
      }
    },
    { signal: handle.signal },
  );

  // the region focus model, the Inbox and the room or chat above or below (decision P132); none inside the terminal or the
  // editor (shortcutOf), and the arrows leave a dropdown's own keys alone
  window.addEventListener(
    'keydown',
    (e) => {
      const id = shortcutOf(e);
      if (!id) return;
      const region = REGION_OF[id];
      if (region) {
        if (focusRegion(region)) e.preventDefault();
      } else if (id === 'inbox') {
        e.preventDefault();
        go(routes.needsYou.href());
      } else if (id === 'room-prev' || id === 'room-next') {
        const el = e.target as HTMLElement | null;
        if (el?.closest?.('[role="combobox"], [role="listbox"], [role="menu"]')) return;
        const dir = id === 'room-prev' ? -1 : 1;
        const next =
          here.name === 'room'
            ? neighbour(roomOrder(s.project.roomList), here.id, dir)
            : here.name === 'chat'
              ? neighbour(chatOrder(), here.id, dir)
              : undefined;
        if (!next) return;
        e.preventDefault();
        go(here.name === 'room' ? routes.room.href({ id: next.id }) : routes.chat.href({ id: next.id }));
      }
    },
    { signal: handle.signal },
  );

  // the person's key bindings, kept by the core (keys.get); a change made in another window arrives as keys.changed
  const keys = query(handle, { load: () => s.client.keysGet(), when: (e) => e.type === 'keys.changed' });
  let keysSeen: unknown;

  const openDM = async (agentId: string) => {
    const pid = s.workspace.projectId;
    if (!pid) return;
    dmError = undefined;
    try {
      const r = await s.project.openDM(pid, agentId);
      await navigate(routes.room.href({ id: r.id }));
    } catch (e) {
      dmError = e as Error;
      void handle.update();
    }
  };

  let lastPath = '';
  return () => {
    const { url, children } = handle.props;
    const route = parseRoute(url.pathname);
    here = route;
    if (keys.data && keys.data !== keysSeen) {
      keysSeen = keys.data;
      const b = keys.data.bindings;
      handle.queueTask(() => s.ui.setKeys(b));
    }
    if (url.pathname !== lastPath) {
      // a navigation closes a drawer left open (the page it was opened over is gone)
      if (lastPath && s.ui.drawerOpen) handle.queueTask(() => s.ui.setDrawer(false));
      lastPath = url.pathname;
    }
    const hub = hubOf(route);
    const ws = s.workspace;
    const project = ws.project;
    const needs = ws.needsCount;
    handle.queueTask(() => {
      s.ui.setProject(project?.id); // the dock keeps its state per project
      document.title = needs > 0 ? `(${needs}) Drydock` : 'Drydock';
    });

    let center: RemixNode;
    if (ws.projects.loading && !ws.projects.data) {
      center = <Loading />;
    } else if (ws.projects.error && !ws.projects.data) {
      center = (
        <Page title="Cannot reach the Drydock core" icon={PlugZap}>
          <ErrorNote error={ws.projects.error} />
          <p>The page needs the core's address and token (open the link the core printed, which ends in ?token=...).</p>
        </Page>
      );
    } else if ((!project || ws.setupOpen) && !isWorkspaceRoute(route) && route.name !== 'project-new') {
      // pages that do not need a project open without one: a read-only install has to reach License and Backups. The first
      // run is the start screen (decision P142); it stays while a start makes the project and its first work, until it lands.
      center = <WorkspaceHomeScreen onNavigate={handle.props.onNavigate} />;
    } else {
      center = children;
    }

    // a workspace page shows nothing of a project: no repository banner, no details panel, no dock
    const inProject = !!project && !isWorkspacePage(route);
    dockHere = inProject;
    // an agent's page, and a direct message: its agent (decision P130)
    agentHere = route.name === 'agent' ? route.id : route.name === 'room' ? s.project.roomList.find((r) => r.id === route.id && r.kind === 'dm')?.agentId : undefined;
    const taskContext =
      route.name === 'task' ? route.id : route.name === 'run' ? route.taskId : route.name === 'room' ? s.project.roomList.find((r) => r.id === route.id)?.taskId : undefined;
    const fullRail = project && hub === 'rooms' ? <RoomsRail route={route} /> : hub === 'settings' ? <SettingsRail route={route} /> : null;
    const narrow = s.ui.narrowRails;
    const rail = fullRail && narrow && !s.ui.drawerOpen ? null : fullRail;
    const railName = hub === 'rooms' ? 'rooms' : 'settings sections';

    return (
      <div mix={rootCss}>
        {/* in a landmark of its own, so every piece of the page is inside one (axe "region") */}
        <nav aria-label="Skip links" mix={skipNav}>
          <a href="#main" mix={skipCss} data-rmx-document>
            Skip to content
          </a>
        </nav>
        <Sidebar route={route} onNavigate={handle.props.onNavigate} />
        <div mix={columnCss}>
        <LicenseBanner />
        <TermsNotice />
        {project || isWorkspacePage(route) ? <SubNav route={route} /> : null}
        <div mix={appCss}>
          {rail && narrow ? <button type="button" mix={[scrimCss, on('click', () => s.ui.setDrawer(false))]} aria-label={`Close the ${railName}`} /> : null}
          {rail}
          <div mix={centerCss}>
            {fullRail && narrow ? (
              <div mix={drawerBar}>
                <Button size="sm" variant="ghost" icon={PanelLeftOpen} aria-expanded={s.ui.drawerOpen ? 'true' : 'false'} mix={[on('click', () => s.ui.setDrawer(!s.ui.drawerOpen))]}>
                  {hub === 'rooms' ? 'Rooms' : 'Settings sections'}
                </Button>
              </div>
            ) : null}
            {dmError ? (
              <div style={{ padding: '8px 16px' }}>
                <ErrorNote error={dmError} what="Could not open the direct message" />
              </div>
            ) : null}
            {inProject && project && projectHasRepo(project) ? <RepoConfigBanner key={`repo:${project.id}`} projectId={project.id} /> : null}
            {inProject && project && projectHasRepo(project) ? <AgentConfigBanner key={`agents:${project.id}`} projectId={project.id} /> : null}
            <main id="main" tabIndex={-1} mix={mainCss} data-region="main">
              {/* a page that fails to draw shows what failed here and leaves the top bar, the dock and the panel working */}
              <ErrorBoundary label="this page" resetKey={url.pathname}>
                {center}
              </ErrorBoundary>
            </main>
            {inProject && project ? (
              <ErrorBoundary label="the dock">
                <Dock projectId={project.id} taskId={taskContext} />
              </ErrorBoundary>
            ) : null}
          </div>
          {inProject ? (
            <ErrorBoundary label="the details panel">
              <Details taskId={taskContext} roomId={route.name === 'room' ? route.id : undefined} page={route.name === 'room' || route.name === 'run' ? 'conversation' : 'other'} />
            </ErrorBoundary>
          ) : null}
        </div>
        </div>
        <CommandPalette onMessage={(id) => void openDM(id)} taskId={inProject ? taskContext : undefined} />
      </div>
    );
  };
}
